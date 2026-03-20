package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	_ "github.com/lib/pq" // PostgreSQL driver

	"github.com/had-nu/xiphos/internal/model"
)

// Connect opens a PostgreSQL connection and verifies it with a ping.
// The caller is responsible for closing the returned *sql.DB.
func Connect(dsn string) (*sql.DB, error) {
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, fmt.Errorf("db open: %w", err)
	}

	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(5 * time.Minute)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("db ping: %w", err)
	}

	slog.Info("database connected")
	return db, nil
}

// InsertFinding inserts a finding into the database.
// Idempotent: uses ON CONFLICT to skip duplicates (same value_hash + file_path + repo_url).
func InsertFinding(ctx context.Context, db *sql.DB, f model.Finding) error {
	controls, err := json.Marshal(f.ComplianceControls)
	if err != nil {
		return fmt.Errorf("marshal compliance_controls: %w", err)
	}
	steps, err := json.Marshal(f.RemediationSteps)
	if err != nil {
		return fmt.Errorf("marshal remediation_steps: %w", err)
	}

	_, err = db.ExecContext(ctx, `
		INSERT INTO findings (
			repo_url, file_path, line_number, secret_type, secret_class,
			value_hash, redacted_value, entropy, structural_valid, confidence,
			exposure_context, recency_tier, compliance_controls, blast_radius,
			remediation_steps
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)
		ON CONFLICT (value_hash, file_path, repo_url) DO NOTHING`,
		f.RepoURL, f.FilePath, f.LineNumber, f.SecretType, f.SecretClass,
		f.ValueHash, f.RedactedValue, f.Entropy, f.StructuralValid, f.Confidence,
		f.ExposureContext, f.RecencyTier, controls, f.BlastRadius, steps,
	)
	if err != nil {
		return fmt.Errorf("insert finding: %w", err)
	}
	return nil
}

// UpdateJobStatus updates the status of a scan job.
func UpdateJobStatus(ctx context.Context, db *sql.DB, jobID, status string, findingsCount, filesScanned int, scanErr string) error {
	var completedAt *time.Time
	if status == model.StatusCompleted || status == model.StatusFailed {
		now := time.Now()
		completedAt = &now
	}

	_, err := db.ExecContext(ctx, `
		UPDATE scan_jobs
		SET status = $2, completed_at = $3, findings_count = $4,
		    files_scanned = $5, error = $6
		WHERE job_id = $1`,
		jobID, status, completedAt, findingsCount, filesScanned, scanErr,
	)
	if err != nil {
		return fmt.Errorf("update job %s: %w", jobID, err)
	}
	return nil
}

// InsertJob inserts a new scan job into the database.
func InsertJob(ctx context.Context, db *sql.DB, job model.ScanJob) error {
	_, err := db.ExecContext(ctx, `
		INSERT INTO scan_jobs (job_id, repo_url, clone_url, status, queued_at)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (job_id) DO NOTHING`,
		job.JobID, job.RepoURL, job.CloneURL, job.Status, job.QueuedAt,
	)
	if err != nil {
		return fmt.Errorf("insert job: %w", err)
	}
	return nil
}
