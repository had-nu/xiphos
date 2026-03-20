package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"time"

	"github.com/had-nu/xiphos/internal/ingestor"
	"github.com/had-nu/xiphos/internal/queue"
)

func main() {
	natsURL := flag.String("nats-url", envOrDefault("XIPHOS_NATS_URL", "nats://localhost:4222"), "NATS server URL")
	tokenFile := flag.String("github-tokens", os.Getenv("XIPHOS_GITHUB_TOKENS"), "Path to file with GitHub tokens (one per line)")
	pollInterval := flag.Duration("poll-interval", 30*time.Second, "GitHub Events API poll interval")
	flag.Parse()

	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})))

	// Fail fast if token file is not configured.
	if *tokenFile == "" {
		slog.Error("fatal: --github-tokens or XIPHOS_GITHUB_TOKENS is required")
		os.Exit(1)
	}

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

	cfg := ingestor.Config{
		TokenFile:    *tokenFile,
		PollInterval: *pollInterval,
	}

	if err := ingestor.Run(ctx, js, cfg); err != nil {
		slog.Error("ingestor exited with error", "error", err)
		os.Exit(1)
	}
}

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
