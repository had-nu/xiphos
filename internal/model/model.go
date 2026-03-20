package model

import (
	"fmt"
	"time"
)

// Job status constants.
const (
	StatusPending   = "pending"
	StatusRunning   = "running"
	StatusCompleted = "completed"
	StatusFailed    = "failed"
)

// ScanJob represents a unit of work: one repository to scan.
type ScanJob struct {
	JobID      string    `json:"job_id"`
	RepoURL    string    `json:"repo_url"`
	CloneURL   string    `json:"clone_url"`
	Status     string    `json:"status"`
	WorkerID   string    `json:"worker_id,omitempty"`
	QueuedAt   time.Time `json:"queued_at"`
	StartedAt  time.Time `json:"started_at,omitempty"`
	CompletedAt time.Time `json:"completed_at,omitempty"`
	FindingsCount int    `json:"findings_count"`
	FilesScanned  int    `json:"files_scanned"`
	Error      string    `json:"error,omitempty"`
}

// Finding represents a single secret detected by Vexil.
// Fields mirror the Vexil v2.6.0 output schema.
// Value is intentionally absent — only value_hash and redacted_value are stored.
type Finding struct {
	RepoURL            string   `json:"repo_url"`
	FilePath           string   `json:"file_path"`
	LineNumber         int      `json:"line_number"`
	SecretType         string   `json:"secret_type"`
	SecretClass        string   `json:"secret_class"`
	ValueHash          string   `json:"value_hash"`
	RedactedValue      string   `json:"redacted_value"`
	Entropy            float64  `json:"entropy"`
	StructuralValid    *bool    `json:"structural_valid,omitempty"`
	Confidence         string   `json:"confidence"`
	ExposureContext    string   `json:"exposure_context"`
	RecencyTier        string   `json:"recency_tier,omitempty"`
	ComplianceControls []string `json:"compliance_controls,omitempty"`
	BlastRadius        string   `json:"blast_radius,omitempty"`
	RemediationSteps   []string `json:"remediation_steps,omitempty"`
}

// VexilScanMetadata mirrors the scan_metadata object in Vexil's JSON envelope.
type VexilScanMetadata struct {
	Tool                    string `json:"tool"`
	Version                 string `json:"version"`
	FilesScanned            int    `json:"files_scanned"`
	WorstConfidence         string `json:"worst_confidence"`
	CredentialReuseDetected bool   `json:"credential_reuse_detected"`
	FilesWithFindings       int    `json:"files_with_findings"`
}

// VexilEnvelope is the top-level JSON structure produced by `vexil --format json`.
type VexilEnvelope struct {
	ScanMetadata VexilScanMetadata `json:"scan_metadata"`
	Findings     []VexilFinding    `json:"findings"`
}

// VexilFinding mirrors a single finding as emitted by Vexil.
// Separate from Finding because the Vexil output does not include repo_url.
type VexilFinding struct {
	FilePath           string   `json:"file_path"`
	LineNumber         int      `json:"line_number"`
	SecretType         string   `json:"secret_type"`
	SecretClass        string   `json:"secret_class"`
	ValueHash          string   `json:"value_hash"`
	RedactedValue      string   `json:"redacted_value"`
	Entropy            float64  `json:"entropy"`
	StructuralValid    *bool    `json:"structural_valid,omitempty"`
	Confidence         string   `json:"confidence"`
	ExposureContext    string   `json:"exposure_context"`
	RecencyTier        string   `json:"recency_tier,omitempty"`
	ComplianceControls []string `json:"compliance_controls,omitempty"`
	BlastRadius        string   `json:"blast_radius,omitempty"`
	RemediationSteps   []string `json:"remediation_steps,omitempty"`
}

// ToFinding converts a VexilFinding to a Finding, attaching the repo URL.
func (vf VexilFinding) ToFinding(repoURL string) Finding {
	return Finding{
		RepoURL:            repoURL,
		FilePath:           vf.FilePath,
		LineNumber:         vf.LineNumber,
		SecretType:         vf.SecretType,
		SecretClass:        vf.SecretClass,
		ValueHash:          vf.ValueHash,
		RedactedValue:      vf.RedactedValue,
		Entropy:            vf.Entropy,
		StructuralValid:    vf.StructuralValid,
		Confidence:         vf.Confidence,
		ExposureContext:    vf.ExposureContext,
		RecencyTier:        vf.RecencyTier,
		ComplianceControls: vf.ComplianceControls,
		BlastRadius:        vf.BlastRadius,
		RemediationSteps:   vf.RemediationSteps,
	}
}

// Validate checks that required fields are present on a ScanJob.
func (j *ScanJob) Validate() error {
	if j.JobID == "" {
		return fmt.Errorf("scan job: missing job_id")
	}
	if j.CloneURL == "" {
		return fmt.Errorf("scan job: missing clone_url")
	}
	if j.RepoURL == "" {
		return fmt.Errorf("scan job: missing repo_url")
	}
	return nil
}
