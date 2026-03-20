// Command stress-test discovers small public Go repositories via the GitHub
// Search API and runs Vexil scans at escalating concurrency levels (1→2→4→8→16→32)
// to find the system's throughput ceiling and saturation point.
//
// GitHub API Safety:
//   - Authenticated search: 30 requests/minute (we use 3 pages × 30 results = 90 repos)
//   - Clone rate: 1 clone/second with jitter (well below abuse threshold)
//   - Only targets small repos (< 1MB size filter) for fast cloning
//   - Repos are cached — cloned once and reused across all concurrency runs
//   - Total network activity: ~90 clones at ~1/s = ~90 seconds of cloning
//
// Usage:
//
//	go run ./cmd/stress-test --vexil-bin /path/to/vexil --gh-token ghp_xxx
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
)

type repo struct {
	Name     string `json:"full_name"`
	CloneURL string `json:"clone_url"`
	Size     int    `json:"size"` // KB
}

type searchResponse struct {
	TotalCount int    `json:"total_count"`
	Items      []repo `json:"items"`
}

type scanResult struct {
	Repo        string  `json:"repo"`
	ScanSec     float64 `json:"scan_sec"`
	Findings    int     `json:"findings"`
	OutputBytes int     `json:"output_bytes"`
	Error       string  `json:"error,omitempty"`
}

type benchRun struct {
	Workers       int           `json:"workers"`
	TotalRepos    int           `json:"total_repos"`
	WallClockSec  float64       `json:"wall_clock_sec"`
	AvgPerRepoSec float64       `json:"avg_per_repo_sec"`
	ReposPerHour  float64       `json:"repos_per_hour"`
	Speedup       float64       `json:"speedup"`
	CPUUsage      int           `json:"cpu_cores_available"`
	Errors        int           `json:"errors"`
}

func main() {
	ghToken := flag.String("gh-token", os.Getenv("GITHUB_TOKEN"), "GitHub personal access token")
	vexilBin := flag.String("vexil-bin", "vexil", "Path to Vexil binary")
	cloneBase := flag.String("clone-dir", "/tmp/xiphos-stress", "Base directory for cached clones")
	repoCount := flag.Int("repos", 60, "Number of repos to discover (max 90)")
	flag.Parse()

	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})))

	if *ghToken == "" {
		fmt.Fprintln(os.Stderr, "ERROR: --gh-token or GITHUB_TOKEN required")
		os.Exit(1)
	}

	if *repoCount > 90 {
		*repoCount = 90
	}

	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "╔═══════════════════════════════════════════════════════════════╗")
	fmt.Fprintln(os.Stderr, "║  Xiphos Stress Test — Wild Benchmark                         ║")
	fmt.Fprintln(os.Stderr, "║  Source: Real public Go repos via GitHub Search API           ║")
	fmt.Fprintln(os.Stderr, "║  Safety: 1 clone/s, small repos only (< 1MB), full clones    ║")
	fmt.Fprintln(os.Stderr, "╚═══════════════════════════════════════════════════════════════╝")
	fmt.Fprintln(os.Stderr, "")

	// Phase 1: Discover repos via Search API.
	fmt.Fprintf(os.Stderr, "Phase 1: Discovering %d small Go repos via GitHub Search API...\n", *repoCount)
	repos, err := discoverRepos(*ghToken, *repoCount)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "  Found %d repos (avg size: %dKB)\n", len(repos), avgSize(repos))

	// Phase 2: Pre-clone all repos (sequential, rate-limited, cached).
	fmt.Fprintf(os.Stderr, "\nPhase 2: Pre-cloning %d repos (1 clone/s, cached)...\n", len(repos))
	cloned := 0
	cached := 0
	for _, r := range repos {
		dest := filepath.Join(*cloneBase, "repos", sanitizeName(r.Name))
		if _, err := os.Stat(filepath.Join(dest, ".git")); err == nil {
			cached++
			continue
		}
		_ = os.RemoveAll(dest)

		// Full clone — matches production behaviour (SPEC D-001: full clone only).
		cmd := exec.Command("git", "clone", "--quiet", r.CloneURL, dest)
		if err := cmd.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "  ✗ %s: %v\n", r.Name, err)
			continue
		}
		cloned++

		// Rate limit: 1 clone/second + random jitter (0-500ms).
		jitter := time.Duration(rand.Intn(500)) * time.Millisecond
		time.Sleep(1*time.Second + jitter)
	}
	fmt.Fprintf(os.Stderr, "  Cloned: %d, Cached: %d, Total: %d\n", cloned, cached, cloned+cached)

	// Filter to repos that were successfully cloned.
	var validRepos []repo
	for _, r := range repos {
		dest := filepath.Join(*cloneBase, "repos", sanitizeName(r.Name))
		if _, err := os.Stat(filepath.Join(dest, ".git")); err == nil {
			validRepos = append(validRepos, r)
		}
	}
	fmt.Fprintf(os.Stderr, "  Valid repos for scan: %d\n", len(validRepos))

	// Phase 3: Escalating concurrency stress test.
	concurrencyLevels := []int{1, 2, 4, 8, 16, 32}
	var runs []benchRun
	var baselineWall float64

	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintf(os.Stderr, "Phase 3: Stress test across %d repos (CPU cores: %d)\n", len(validRepos), runtime.NumCPU())
	fmt.Fprintln(os.Stderr, "")

	for _, workers := range concurrencyLevels {
		if workers > len(validRepos) {
			break
		}

		run := runStressTest(workers, validRepos, *vexilBin, *cloneBase)

		if workers == 1 {
			baselineWall = run.WallClockSec
			run.Speedup = 1.0
		} else if baselineWall > 0 {
			run.Speedup = baselineWall / run.WallClockSec
		}

		runs = append(runs, run)

		fmt.Fprintf(os.Stderr, "  W=%2d | Wall: %6.1fs | Avg: %5.2fs/repo | %6.0f repos/h | ↑%.2fx | Err: %d\n",
			run.Workers, run.WallClockSec, run.AvgPerRepoSec,
			run.ReposPerHour, run.Speedup, run.Errors)
	}

	// Phase 4: Output JSON results.
	fmt.Fprintln(os.Stderr, "")
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(runs)

	// Phase 5: Summary table.
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "┌─────────┬──────────┬───────────┬───────────┬─────────┬────────┐")
	fmt.Fprintln(os.Stderr, "│ Workers │ Wall (s) │ Avg/repo  │  Repos/h  │ Speedup │ Errors │")
	fmt.Fprintln(os.Stderr, "├─────────┼──────────┼───────────┼───────────┼─────────┼────────┤")
	for _, r := range runs {
		fmt.Fprintf(os.Stderr, "│ %7d │ %8.1f │ %7.2fs  │ %9.0f │ %6.2fx │ %6d │\n",
			r.Workers, r.WallClockSec, r.AvgPerRepoSec, r.ReposPerHour, r.Speedup, r.Errors)
	}
	fmt.Fprintln(os.Stderr, "└─────────┴──────────┴───────────┴───────────┴─────────┴────────┘")
	fmt.Fprintf(os.Stderr, "\nCPU cores: %d | Repos tested: %d\n", runtime.NumCPU(), len(validRepos))
}

// discoverRepos uses the GitHub Search API to find small, recently-pushed Go repos.
func discoverRepos(token string, count int) ([]repo, error) {
	var all []repo
	perPage := 30
	pages := (count + perPage - 1) / perPage
	if pages > 3 {
		pages = 3 // Search API limit: 1000 results, but we stay well under
	}

	for page := 1; page <= pages; page++ {
		// Search for small Go repos pushed recently — diverse and real-world.
		query := "language:go size:<1000 pushed:>2025-01-01"
		u := fmt.Sprintf("https://api.github.com/search/repositories?q=%s&sort=updated&order=desc&per_page=%d&page=%d",
			url.QueryEscape(query), perPage, page)

		req, err := http.NewRequest(http.MethodGet, u, nil)
		if err != nil {
			return nil, fmt.Errorf("new request: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Accept", "application/vnd.github+json")

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("search api: %w", err)
		}

		body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		resp.Body.Close()

		if resp.StatusCode != 200 {
			return nil, fmt.Errorf("search api %d: %s", resp.StatusCode, string(body[:min(len(body), 200)]))
		}

		remaining := resp.Header.Get("X-RateLimit-Remaining")
		fmt.Fprintf(os.Stderr, "  Page %d: status=%d, rate-remaining=%s\n", page, resp.StatusCode, remaining)

		var sr searchResponse
		if err := json.Unmarshal(body, &sr); err != nil {
			return nil, fmt.Errorf("parse search: %w", err)
		}

		all = append(all, sr.Items...)

		// Respect search rate limit: 10 req/min for search.
		if page < pages {
			time.Sleep(7 * time.Second)
		}
	}

	if len(all) > count {
		all = all[:count]
	}
	return all, nil
}

func runStressTest(workers int, repos []repo, vexilBin, cloneBase string) benchRun {
	results := make([]scanResult, len(repos))
	var wg sync.WaitGroup
	sem := make(chan struct{}, workers)
	var errCount atomic.Int64

	start := time.Now()

	for i, r := range repos {
		wg.Add(1)
		go func(idx int, r repo) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			results[idx] = scanRepo(r, vexilBin, cloneBase)
			if results[idx].Error != "" {
				errCount.Add(1)
			}
		}(i, r)
	}

	wg.Wait()
	wallClock := time.Since(start)

	var totalScan float64
	for _, r := range results {
		totalScan += r.ScanSec
	}

	wallSec := wallClock.Seconds()
	avgPerRepo := totalScan / float64(len(repos))
	reposPerHour := float64(len(repos)) / (wallSec / 3600.0)

	return benchRun{
		Workers:       workers,
		TotalRepos:    len(repos),
		WallClockSec:  wallSec,
		AvgPerRepoSec: avgPerRepo,
		ReposPerHour:  reposPerHour,
		CPUUsage:      runtime.NumCPU(),
		Errors:        int(errCount.Load()),
	}
}

func scanRepo(r repo, vexilBin, cloneBase string) scanResult {
	repoDir := filepath.Join(cloneBase, "repos", sanitizeName(r.Name))
	result := scanResult{Repo: r.Name}

	if _, err := os.Stat(filepath.Join(repoDir, ".git")); err != nil {
		result.Error = "clone not found"
		return result
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	scanStart := time.Now()
	cmd := exec.CommandContext(ctx, vexilBin, "--dir", repoDir, "--format", "json")
	output, err := cmd.Output()
	result.ScanSec = time.Since(scanStart).Seconds()
	result.OutputBytes = len(output)

	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			if exitErr.ExitCode() != 1 && exitErr.ExitCode() != 2 {
				result.Error = fmt.Sprintf("exit %d", exitErr.ExitCode())
				return result
			}
		} else {
			result.Error = fmt.Sprintf("exec: %v", err)
			return result
		}
	}

	var envelope struct {
		Findings []json.RawMessage `json:"findings"`
	}
	if err := json.Unmarshal(output, &envelope); err == nil {
		result.Findings = len(envelope.Findings)
	}

	return result
}

func sanitizeName(name string) string {
	// Replace / with _ for filesystem safety.
	out := make([]byte, len(name))
	for i, c := range name {
		if c == '/' {
			out[i] = '_'
		} else {
			out[i] = byte(c)
		}
	}
	return string(out)
}

func avgSize(repos []repo) int {
	if len(repos) == 0 {
		return 0
	}
	total := 0
	for _, r := range repos {
		total += r.Size
	}
	return total / len(repos)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
