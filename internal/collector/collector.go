package collector

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/had-nu/xiphos/internal/db"
	"github.com/had-nu/xiphos/internal/model"
	"github.com/had-nu/xiphos/internal/queue"
)

// Config holds the collector's runtime configuration.
type Config struct {
	BatchSize    int
	FlushTimeout time.Duration
}

// Run subscribes to the results stream and inserts findings into PostgreSQL.
// It processes messages until ctx is cancelled.
func Run(ctx context.Context, js nats.JetStreamContext, database *sql.DB, cfg Config) error {
	sub, err := js.PullSubscribe(queue.SubjectResults, "xiphos-collectors",
		nats.AckWait(30*time.Second),
	)
	if err != nil {
		return fmt.Errorf("subscribe %s: %w", queue.SubjectResults, err)
	}

	slog.Info("collector started", "batch_size", cfg.BatchSize)

	for {
		select {
		case <-ctx.Done():
			slog.Info("collector stopping", "reason", ctx.Err())
			return nil
		default:
		}

		msgs, err := sub.Fetch(cfg.BatchSize, nats.MaxWait(cfg.FlushTimeout))
		if err != nil {
			if err == nats.ErrTimeout {
				continue
			}
			slog.Error("fetch error", "error", err)
			continue
		}

		ingested := 0
		for _, msg := range msgs {
			var finding model.Finding
			if err := json.Unmarshal(msg.Data, &finding); err != nil {
				slog.Error("unmarshal finding", "error", err)
				// Ack to prevent poison message loop.
				if ackErr := msg.Ack(); ackErr != nil {
					slog.Error("ack failed", "error", ackErr)
				}
				continue
			}

			if err := db.InsertFinding(ctx, database, finding); err != nil {
				slog.Error("insert finding",
					"value_hash", finding.ValueHash,
					"repo", finding.RepoURL,
					"error", err,
				)
				if nakErr := msg.Nak(); nakErr != nil {
					slog.Error("nak failed", "error", nakErr)
				}
				continue
			}

			if err := msg.Ack(); err != nil {
				slog.Error("ack failed", "error", err)
			}
			ingested++
		}

		if ingested > 0 {
			slog.Info("batch ingested", "count", ingested)
		}
	}
}
