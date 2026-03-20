package queue

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/nats-io/nats.go"
)

const (
	// StreamScan is the NATS JetStream stream for scan work distribution.
	StreamScan = "WORK_SCAN"
	// SubjectScan is the subject workers subscribe to for scan jobs.
	SubjectScan = "work.scan"

	// StreamResults is the NATS JetStream stream for scan result collection.
	StreamResults = "WORK_RESULTS"
	// SubjectResults is the subject collectors subscribe to for findings.
	SubjectResults = "work.results"
)

// Connect establishes a connection to the NATS server and returns a
// JetStream context. The caller is responsible for closing the connection.
func Connect(url string) (*nats.Conn, nats.JetStreamContext, error) {
	nc, err := nats.Connect(url,
		nats.RetryOnFailedConnect(true),
		nats.MaxReconnects(10),
		nats.ReconnectWait(2*time.Second),
		nats.DisconnectErrHandler(func(_ *nats.Conn, err error) {
			slog.Warn("nats disconnected", "error", err)
		}),
		nats.ReconnectHandler(func(nc *nats.Conn) {
			slog.Info("nats reconnected", "url", nc.ConnectedUrl())
		}),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("nats connect: %w", err)
	}

	js, err := nc.JetStream()
	if err != nil {
		nc.Close()
		return nil, nil, fmt.Errorf("nats jetstream: %w", err)
	}

	return nc, js, nil
}

// EnsureStreams creates the work.scan and work.results streams if they
// do not already exist. Idempotent — safe to call on every startup.
func EnsureStreams(js nats.JetStreamContext) error {
	streams := []struct {
		name     string
		subjects []string
	}{
		{StreamScan, []string{SubjectScan}},
		{StreamResults, []string{SubjectResults}},
	}

	for _, s := range streams {
		_, err := js.StreamInfo(s.name)
		if err == nil {
			slog.Info("stream exists", "stream", s.name)
			continue
		}

		_, err = js.AddStream(&nats.StreamConfig{
			Name:      s.name,
			Subjects:  s.subjects,
			Retention: nats.WorkQueuePolicy,
			MaxAge:    24 * time.Hour,
			Storage:   nats.FileStorage,
		})
		if err != nil {
			return fmt.Errorf("create stream %s: %w", s.name, err)
		}
		slog.Info("stream created", "stream", s.name)
	}

	return nil
}
