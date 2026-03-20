// Command benchmark measures Xiphos worker throughput at different concurrency
// levels using the owner's own public repositories as targets.
//
// Safety measures against GitHub rate limiting:
//   - Uses ONLY repos owned by the authenticated user (no scraping)
//   - Adds a configurable delay between clone operations
//   - Caps maximum concurrent workers at 8
//   - Clones are cached: each repo is cloned once and reused across runs
//   - Total repos scanned is small and bounded
//
// Usage:
//
//	go run ./cmd/benchmark --vexil-bin /path/to/vexil
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
)

// repo defines a target repository for benchmarking.
type repo struct {
	Name     string `json:"name"`
	CloneURL string `json:"clone_url"`
}

// scanResult captures the outcome of a single repo scan.
type scanResult struct {
	Repo        string        `json:"repo"`
	CloneDur    time.Duration `json:"clone_ms"`
	ScanDur     time.Duration `json:"scan_ms"`
	TotalDur    time.Duration `json:"total_ms"`
	Findings    int           `json:"findings"`
	OutputBytes int           `json:"output_bytes"`
	Error       string        `json:"error,omitempty"`
}

// benchRun captures the results of a single benchmark run at a given concurrency.
type benchRun struct {
	Workers      int             `json:"workers"`
	TotalRepos   int             `json:"total_repos"`
	WallClock    time.Duration   `json:"wall_clock_ms"`
	AvgPerRepo   time.Duration   `json:"avg_per_repo_ms"`
	ReposPerHour float64         `json:"repos_per_hour"`
	Speedup      float64         `json:"speedup_vs_single"`
	Results      []scanResult    `json:"results"`
}

// ownRepos are the user's own public repos — safe to clone without abuse risk.
// Listed from smallest to largest to keep benchmark fast.
var ownRepos = []repo{
	{"had-nu", "https://github.com/had-nu/had-nu.git"},
	{"hadnu.github.io", "https://github.com/had-nu/hadnu.github.io.git"},
	{"xiphos", "https://github.com/had-nu/xiphos.git"},
	{"nullcal", "https://github.com/had-nu/nullcal.git"},
	{"lazy.go", "https://github.com/had-nu/lazy.go.git"},
	{"masthead", "https://github.com/had-nu/masthead.git"},
	{"wardex", "https://github.com/had-nu/wardex.git"},
	{"wardex-foundry", "https://github.com/had-nu/wardex-foundry.git"},
	{"vexil", "https://github.com/had-nu/vexil.git"},
	{"astra-pathfinder", "https://github.com/had-nu/astra-pathfinder.git"},
}

func main() {
	vexilBin := flag.String("vexil-bin", "vexil", "Path to Vexil binary")
	cloneBase := flag.String("clone-dir", "/tmp/xiphos-bench", "Base directory for cached clones")
	cloneDelay := flag.Duration("clone-delay", 500*time.Millisecond, "Delay between clone operations (rate limiting)")
	flag.Parse()

	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})))

	// Phase 1: Pre-clone all repos (sequential, with delay, cached).
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "╔══════════════════════════════════════════════════════════╗")
	fmt.Fprintln(os.Stderr, "║  Xiphos Benchmark — Scalability Test                     ║")
	fmt.Fprintln(os.Stderr, "║  Repos: owner's public repos only (no scraping)          ║")
	fmt.Fprintln(os.Stderr, "║  Rate limit: clone delay between operations              ║")
	fmt.Fprintln(os.Stderr, "╚══════════════════════════════════════════════════════════╝")
	fmt.Fprintln(os.Stderr, "")

	fmt.Fprintf(os.Stderr, "Phase 1: Pre-cloning %d repos (cached, sequential)...\n", len(ownRepos))
	for _, r := range ownRepos {
		dest := filepath.Join(*cloneBase, "repos", r.Name)
		if _, err := os.Stat(filepath.Join(dest, ".git")); err == nil {
			fmt.Fprintf(os.Stderr, "  ✓ %s (cached)\n", r.Name)
			continue
		}
		_ = os.RemoveAll(dest)
		cmd := exec.Command("git", "clone", "--quiet", r.CloneURL, dest)
		if err := cmd.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "  ✗ %s: %v\n", r.Name, err)
			continue
		}
		fmt.Fprintf(os.Stderr, "  ✓ %s (cloned)\n", r.Name)
		time.Sleep(*cloneDelay)
	}

	// Phase 2: Benchmark at different concurrency levels.
	concurrencyLevels := []int{1, 2, 4, 8}
	var runs []benchRun
	var baselineWall time.Duration

	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Phase 2: Running Vexil scans at different concurrency levels...")
	fmt.Fprintln(os.Stderr, "")

	for _, workers := range concurrencyLevels {
		run := runBenchmark(workers, ownRepos, *vexilBin, *cloneBase)

		if workers == 1 {
			baselineWall = run.WallClock
			run.Speedup = 1.0
		} else if baselineWall > 0 {
			run.Speedup = float64(baselineWall) / float64(run.WallClock)
		}

		runs = append(runs, run)

		fmt.Fprintf(os.Stderr, "  Workers: %d | Wall: %6.1fs | Avg/repo: %5.1fs | Repos/h: %7.0f | Speedup: %.2fx\n",
			run.Workers,
			run.WallClock.Seconds(),
			run.AvgPerRepo.Seconds(),
			run.ReposPerHour,
			run.Speedup,
		)
	}

	// Phase 3: Output results as JSON.
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Phase 3: Results (JSON on stdout)")

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(runs); err != nil {
		slog.Error("encode results", "error", err)
	}

	// Phase 4: Summary table.
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "┌──────────┬──────────┬───────────┬───────────┬─────────┐")
	fmt.Fprintln(os.Stderr, "│ Workers  │ Wall (s) │ Avg/repo  │ Repos/h   │ Speedup │")
	fmt.Fprintln(os.Stderr, "├──────────┼──────────┼───────────┼───────────┼─────────┤")
	for _, r := range runs {
		fmt.Fprintf(os.Stderr, "│ %8d │ %8.1f │ %7.1fs  │ %9.0f │ %6.2fx │\n",
			r.Workers, r.WallClock.Seconds(), r.AvgPerRepo.Seconds(), r.ReposPerHour, r.Speedup)
	}
	fmt.Fprintln(os.Stderr, "└──────────┴──────────┴───────────┴───────────┴─────────┘")
}

// runBenchmark executes Vexil against all repos with the given number of workers.
func runBenchmark(workers int, repos []repo, vexilBin, cloneBase string) benchRun {
	results := make([]scanResult, len(repos))
	var wg sync.WaitGroup
	sem := make(chan struct{}, workers)
	var completed atomic.Int64

	start := time.Now()

	for i, r := range repos {
		wg.Add(1)
		go func(idx int, r repo) {
			defer wg.Done()
			sem <- struct{}{}        // acquire
			defer func() { <-sem }() // release

			results[idx] = scanRepo(r, vexilBin, cloneBase)
			done := completed.Add(1)
			_ = done // suppress unused warning
		}(i, r)
	}

	wg.Wait()
	wallClock := time.Since(start)

	// Calculate averages.
	var totalScan time.Duration
	totalFindings := 0
	for _, r := range results {
		totalScan += r.TotalDur
		totalFindings += r.Findings
	}
	// AvgPerRepo: wall clock divided by repo count — the throughput an operator sees.
	// (Summing per-worker durations would inflate this by the concurrency factor.)
	avgPerRepo := wallClock / time.Duration(len(repos))
	reposPerHour := float64(len(repos)) / wallClock.Hours()

	return benchRun{
		Workers:      workers,
		TotalRepos:   len(repos),
		WallClock:    wallClock,
		AvgPerRepo:   avgPerRepo,
		ReposPerHour: reposPerHour,
		Results:      results,
	}
}

// scanRepo runs Vexil against a single pre-cloned repo and returns timing metrics.
func scanRepo(r repo, vexilBin, cloneBase string) scanResult {
	repoDir := filepath.Join(cloneBase, "repos", r.Name)
	result := scanResult{Repo: r.Name}

	// Verify clone exists.
	if _, err := os.Stat(filepath.Join(repoDir, ".git")); err != nil {
		result.Error = fmt.Sprintf("clone not found: %s", repoDir)
		return result
	}

	// Run Vexil.
	scanStart := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(ctx, vexilBin,
		"--dir", repoDir,
		"--git-aware",
		"--format", "json",
	)

	output, err := cmd.Output()
	result.ScanDur = time.Since(scanStart)
	result.TotalDur = result.ScanDur
	result.OutputBytes = len(output)

	if err != nil {
		// Vexil exits 1/2 on findings — not an error.
		if exitErr, ok := err.(*exec.ExitError); ok {
			if exitErr.ExitCode() != 1 && exitErr.ExitCode() != 2 {
				result.Error = fmt.Sprintf("vexil exit %d", exitErr.ExitCode())
				return result
			}
		} else {
			result.Error = fmt.Sprintf("vexil exec: %v", err)
			return result
		}
	}

	// Count findings without full parsing — just count the array elements.
	var envelope struct {
		Findings []json.RawMessage `json:"findings"`
	}
	if err := json.Unmarshal(output, &envelope); err == nil {
		result.Findings = len(envelope.Findings)
	}

	return result
}
