package ingestor

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/had-nu/xiphos/internal/model"
	"github.com/had-nu/xiphos/internal/queue"
)

// Config holds the ingestor's runtime configuration.
type Config struct {
	TokenFile    string
	PollInterval time.Duration
}

// githubEvent represents a minimal GitHub Events API response item.
type githubEvent struct {
	Type string `json:"type"`
	Repo struct {
		Name string `json:"name"`
	} `json:"repo"`
}

// tokenPool manages round-robin access to GitHub API tokens.
type tokenPool struct {
	tokens []string
	idx    atomic.Int64
}

func newTokenPool(path string) (*tokenPool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read token file: %w", err)
	}

	var tokens []string
	for _, line := range strings.Split(string(data), "\n") {
		t := strings.TrimSpace(line)
		if t != "" && !strings.HasPrefix(t, "#") {
			tokens = append(tokens, t)
		}
	}

	if len(tokens) == 0 {
		return nil, fmt.Errorf("token file %q contains no tokens", path)
	}

	slog.Info("token pool loaded", "count", len(tokens))
	return &tokenPool{tokens: tokens}, nil
}

// next returns the next token in round-robin order.
func (tp *tokenPool) next() string {
	idx := tp.idx.Add(1)
	return tp.tokens[int(idx-1)%len(tp.tokens)]
}

// Run polls the GitHub Events API and publishes scan jobs to NATS.
// It polls until ctx is cancelled.
func Run(ctx context.Context, js nats.JetStreamContext, cfg Config) error {
	pool, err := newTokenPool(cfg.TokenFile)
	if err != nil {
		return err
	}

	// In-memory dedup set with simple string map. Repos seen in this session
	// are skipped. This is intentionally not persistent — a restart re-scans.
	seen := make(map[string]struct{})

	slog.Info("ingestor started", "poll_interval", cfg.PollInterval)

	ticker := time.NewTicker(cfg.PollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			slog.Info("ingestor stopping", "reason", ctx.Err())
			return nil
		case <-ticker.C:
			if err := pollEvents(ctx, js, pool, seen); err != nil {
				slog.Error("poll error", "error", err)
			}
		}
	}
}

const githubEventsURL = "https://api.github.com/events?per_page=100"

// pollEvents fetches one page of public events and publishes PushEvent repos as jobs.
func pollEvents(ctx context.Context, js nats.JetStreamContext, pool *tokenPool, seen map[string]struct{}) error {
	token := pool.next()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, githubEventsURL, nil)
	if err != nil {
		return fmt.Errorf("new request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("github api: %w", err)
	}
	defer resp.Body.Close()

	// Rate limit handling.
	if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests {
		resetStr := resp.Header.Get("X-RateLimit-Reset")
		if resetStr != "" {
			resetUnix, _ := strconv.ParseInt(resetStr, 10, 64)
			waitUntil := time.Unix(resetUnix, 0)
			slog.Warn("rate limited", "reset_at", waitUntil)
		}
		return fmt.Errorf("rate limited: %d", resp.StatusCode)
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("github api status: %d", resp.StatusCode)
	}

	// Limit response body to 10MB (go-security: JSON envelope pattern).
	body, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}

	var events []githubEvent
	if err := json.Unmarshal(body, &events); err != nil {
		return fmt.Errorf("parse events: %w", err)
	}

	published := 0
	for _, ev := range events {
		if ev.Type != "PushEvent" {
			continue
		}
		repoName := ev.Repo.Name
		if _, exists := seen[repoName]; exists {
			continue
		}
		seen[repoName] = struct{}{}

		job := model.ScanJob{
			JobID:    fmt.Sprintf("%s-%d", repoName, time.Now().UnixNano()),
			RepoURL:  "https://github.com/" + repoName,
			CloneURL: "https://github.com/" + repoName + ".git",
			Status:   model.StatusPending,
			QueuedAt: time.Now(),
		}

		data, err := json.Marshal(job)
		if err != nil {
			slog.Error("marshal job", "error", err)
			continue
		}

		if _, err := js.Publish(queue.SubjectScan, data); err != nil {
			slog.Error("publish job", "repo", repoName, "error", err)
			continue
		}
		published++
	}

	if published > 0 {
		slog.Info("events published",
			"push_events", published,
			"total_events", len(events),
			"remaining", resp.Header.Get("X-RateLimit-Remaining"),
		)
	}

	return nil
}
