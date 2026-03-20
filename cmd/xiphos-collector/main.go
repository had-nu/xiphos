package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"time"

	"github.com/had-nu/xiphos/internal/collector"
	"github.com/had-nu/xiphos/internal/db"
	"github.com/had-nu/xiphos/internal/queue"
)

func main() {
	natsURL := flag.String("nats-url", envOrDefault("XIPHOS_NATS_URL", "nats://localhost:4222"), "NATS server URL")
	dbDSN := flag.String("db-dsn", os.Getenv("XIPHOS_DB_DSN"), "PostgreSQL connection string")
	batchSize := flag.Int("batch-size", 50, "Number of findings to fetch per batch")
	flushTimeout := flag.Duration("flush-timeout", 5*time.Second, "Max wait time per batch fetch")
	flag.Parse()

	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})))

	// Fail fast if DB DSN is not configured (go-security: secrets at startup).
	if *dbDSN == "" {
		slog.Error("fatal: XIPHOS_DB_DSN is required")
		os.Exit(1)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	database, err := db.Connect(*dbDSN)
	if err != nil {
		slog.Error("fatal", "error", err)
		os.Exit(1)
	}
	defer database.Close()

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

	cfg := collector.Config{
		BatchSize:    *batchSize,
		FlushTimeout: *flushTimeout,
	}

	if err := collector.Run(ctx, js, database, cfg); err != nil {
		slog.Error("collector exited with error", "error", err)
		os.Exit(1)
	}
}

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
