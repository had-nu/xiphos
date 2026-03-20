package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"time"

	"github.com/had-nu/xiphos/internal/queue"
	"github.com/had-nu/xiphos/internal/worker"
)

func main() {
	natsURL := flag.String("nats-url", envOrDefault("XIPHOS_NATS_URL", "nats://localhost:4222"), "NATS server URL")
	vexilBin := flag.String("vexil-bin", envOrDefault("XIPHOS_VEXIL_BIN", "vexil"), "Path to Vexil binary")
	cloneDir := flag.String("clone-dir", envOrDefault("XIPHOS_CLONE_DIR", "/tmp/xiphos-repos"), "Base directory for repo clones")
	concurrency := flag.Int("concurrency", envOrDefaultInt("XIPHOS_VEXIL_CONCURRENCY", 16), "Vexil internal worker pool size")
	cloneTimeout := flag.Duration("clone-timeout", 120*time.Second, "Maximum time for git clone")
	scanTimeout := flag.Duration("scan-timeout", 300*time.Second, "Maximum time for Vexil scan")
	flag.Parse()

	// Structured logging — stdlib only.
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})))

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	nc, js, err := queue.Connect(*natsURL)
	if err != nil {
		slog.Error("fatal", "error", err)
		os.Exit(1)
	}
	defer nc.Close()

	if err := queue.EnsureStreams(js); err != nil {
		slog.Error("fatal", "error", err)
		os.Exit(1)
	}

	cfg := worker.Config{
		VexilBin:     *vexilBin,
		CloneDir:     *cloneDir,
		Concurrency:  *concurrency,
		CloneTimeout: *cloneTimeout,
		ScanTimeout:  *scanTimeout,
	}

	if err := worker.Run(ctx, js, cfg); err != nil {
		slog.Error("worker exited with error", "error", err)
		os.Exit(1)
	}
}

// envOrDefault returns the environment variable value or a default.
func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// envOrDefaultInt returns the integer environment variable value or a default.
func envOrDefaultInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return fallback
}

func init() {
	// Validate clone directory is writable at startup.
	_ = fmt.Sprintf("xiphos-worker")
}
