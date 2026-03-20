-- Xiphos Intelligence Database Schema
-- PostgreSQL 16+

-- scan_jobs tracks each repository scan task.
CREATE TABLE IF NOT EXISTS scan_jobs (
    job_id       TEXT PRIMARY KEY,
    repo_url     TEXT NOT NULL,
    clone_url    TEXT NOT NULL,
    status       TEXT NOT NULL DEFAULT 'pending',
    worker_id    TEXT,
    queued_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    started_at   TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,
    findings_count INTEGER NOT NULL DEFAULT 0,
    files_scanned  INTEGER NOT NULL DEFAULT 0,
    error        TEXT
);

CREATE INDEX IF NOT EXISTS idx_scan_jobs_status ON scan_jobs (status);
CREATE INDEX IF NOT EXISTS idx_scan_jobs_repo_url ON scan_jobs (repo_url);
CREATE INDEX IF NOT EXISTS idx_scan_jobs_queued_at ON scan_jobs (queued_at);

-- findings stores each detected secret.
-- Deduplication uses (value_hash, file_path, repo_url) to prevent
-- re-inserting the same secret from the same location on re-scan.
CREATE TABLE IF NOT EXISTS findings (
    id                  BIGSERIAL PRIMARY KEY,
    repo_url            TEXT NOT NULL,
    file_path           TEXT NOT NULL,
    line_number         INTEGER NOT NULL,
    secret_type         TEXT NOT NULL,
    secret_class        TEXT NOT NULL,
    value_hash          TEXT NOT NULL,
    redacted_value      TEXT NOT NULL,
    entropy             DOUBLE PRECISION NOT NULL,
    structural_valid    BOOLEAN,
    confidence          TEXT NOT NULL,
    exposure_context    TEXT NOT NULL,
    recency_tier        TEXT,
    compliance_controls JSONB,
    blast_radius        TEXT,
    remediation_steps   JSONB,
    ingested_at         TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Primary correlation index: enables GROUP BY value_hash for cross-repo analysis.
CREATE INDEX IF NOT EXISTS idx_findings_value_hash ON findings (value_hash);

-- Analyst query index: filter by risk level and exposure context.
CREATE INDEX IF NOT EXISTS idx_findings_confidence_context ON findings (confidence, exposure_context);

-- Repository-level query index.
CREATE INDEX IF NOT EXISTS idx_findings_repo_url ON findings (repo_url);

-- Deduplication constraint: same secret in same file in same repo is a single finding.
CREATE UNIQUE INDEX IF NOT EXISTS idx_findings_dedup
    ON findings (value_hash, file_path, repo_url);
