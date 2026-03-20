package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/had-nu/xiphos/internal/model"
	"github.com/had-nu/xiphos/internal/queue"
)

// Config holds the worker's runtime configuration.
type Config struct {
	VexilBin     string
	CloneDir     string
	Concurrency  int
	CloneTimeout time.Duration
	ScanTimeout  time.Duration
}

// Run subscribes to the scan queue and processes jobs until ctx is cancelled.
// Each job: clone repo → run vexil → publish findings → cleanup.
func Run(ctx context.Context, js nats.JetStreamContext, cfg Config) error {
	sub, err := js.PullSubscribe(queue.SubjectScan, "xiphos-workers",
		nats.AckWait(cfg.ScanTimeout+cfg.CloneTimeout+30*time.Second),
		nats.MaxDeliver(5),
		nats.MaxAckPending(1),
	)
	if err != nil {
		return fmt.Errorf("subscribe %s: %w", queue.SubjectScan, err)
	}

	slog.Info("worker started", "clone_dir", cfg.CloneDir, "vexil_bin", cfg.VexilBin)

	for {
		select {
		case <-ctx.Done():
			slog.Info("worker stopping", "reason", ctx.Err())
			return nil
		default:
		}

		msgs, err := sub.Fetch(1, nats.MaxWait(5*time.Second))
		if err != nil {
			// Timeout is expected when queue is empty.
			if err == nats.ErrTimeout {
				continue
			}
			slog.Error("fetch error", "error", err)
			continue
		}

		for _, msg := range msgs {
			if err := processJob(ctx, js, msg, cfg); err != nil {
				slog.Error("job failed", "error", err)
				// Nak so NATS can redeliver to another worker.
				if nakErr := msg.Nak(); nakErr != nil {
					slog.Error("nak failed", "error", nakErr)
				}
				continue
			}
			if err := msg.Ack(); err != nil {
				slog.Error("ack failed", "error", err)
			}
		}
	}
}

// processJob handles a single scan job: clone → vexil → publish.
func processJob(ctx context.Context, js nats.JetStreamContext, msg *nats.Msg, cfg Config) error {
	var job model.ScanJob
	if err := json.Unmarshal(msg.Data, &job); err != nil {
		return fmt.Errorf("unmarshal job: %w", err)
	}
	if err := job.Validate(); err != nil {
		return err
	}

	slog.Info("processing job", "job_id", job.JobID, "repo", job.RepoURL)

	// Create unique clone directory per job.
	cloneDir := filepath.Join(cfg.CloneDir, job.JobID)

	// Clean up any stale directory from a previous retry attempt.
	_ = os.RemoveAll(cloneDir)

	defer func() {
		if err := os.RemoveAll(cloneDir); err != nil {
			slog.Warn("cleanup failed", "dir", cloneDir, "error", err)
		}
	}()

	// Clone the repository (full, no shallow).
	if err := cloneRepo(ctx, job.CloneURL, cloneDir, cfg.CloneTimeout); err != nil {
		return fmt.Errorf("clone %s: %w", job.CloneURL, err)
	}

	// Run Vexil against the cloned repo.
	envelope, err := runVexil(ctx, cloneDir, cfg.VexilBin, cfg.Concurrency, cfg.ScanTimeout)
	if err != nil {
		return fmt.Errorf("vexil %s: %w", job.RepoURL, err)
	}

	slog.Info("scan complete",
		"job_id", job.JobID,
		"findings", len(envelope.Findings),
		"files_scanned", envelope.ScanMetadata.FilesScanned,
	)

	// Publish each finding to the results stream.
	for _, vf := range envelope.Findings {
		finding := vf.ToFinding(job.RepoURL)
		data, err := json.Marshal(finding)
		if err != nil {
			slog.Error("marshal finding", "error", err)
			continue
		}
		if _, err := js.Publish(queue.SubjectResults, data); err != nil {
			slog.Error("publish finding", "error", err)
			continue
		}
	}

	return nil
}

// cloneRepo performs a full git clone with timeout.
// Arguments are passed separately to exec.CommandContext — never shell-expanded.
func cloneRepo(ctx context.Context, cloneURL, dest string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Separate arguments — no shell injection possible (go-security: Command Injection).
	// A plain git clone performs a full clone by default — no --depth flag means full history.
	cmd := exec.CommandContext(ctx, "git", "clone", cloneURL, dest)
	cmd.Stdout = nil
	cmd.Stderr = nil

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git clone: %w", err)
	}
	return nil
}

// runVexil executes the Vexil binary and parses its JSON output.
func runVexil(ctx context.Context, repoDir, vexilBin string, concurrency int, timeout time.Duration) (*model.VexilEnvelope, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	args := []string{
		"--dir", repoDir,
		"--git-aware",
		"--format", "json",
	}
	if concurrency > 0 {
		args = append(args, "--concurrency", strconv.Itoa(concurrency))
	}

	// Separate arguments — no shell injection possible.
	cmd := exec.CommandContext(ctx, vexilBin, args...)

	var stderrBuf bytes.Buffer
	cmd.Stderr = &stderrBuf

	output, err := cmd.Output()
	slog.Info("vexil executed", "output_len", len(output), "stderr_len", stderrBuf.Len())
	if err != nil {
		// Vexil exits 1 when findings are present — that is not an error for us.
		if exitErr, ok := err.(*exec.ExitError); ok {
			// Exit code 1 (warn) or 2 (block) means findings were found — expected.
			if exitErr.ExitCode() == 1 || exitErr.ExitCode() == 2 {
				// Continue with output parsing.
			} else {
				return nil, fmt.Errorf("vexil exit %d: %s", exitErr.ExitCode(), string(exitErr.Stderr))
			}
		} else {
			return nil, fmt.Errorf("vexil exec: %w", err)
		}
	}

	// Parse Vexil JSON envelope with size limit (go-security: JSON envelope pattern).
	var envelope model.VexilEnvelope
	if len(output) == 0 {
		return nil, fmt.Errorf("vexil produced no output; stderr: %s", stderrBuf.String())
	}
	if err := json.Unmarshal(output, &envelope); err != nil {
		return nil, fmt.Errorf("parse vexil output (%d bytes): %w", len(output), err)
	}

	return &envelope, nil
}
