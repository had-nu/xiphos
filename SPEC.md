# Project Xiphos (Ξίφος) — Technical Specification
<!-- Version: 0.1 | Status: Draft | Author: hadnu | Date: 2026-03-20 -->

> **RFC 2119 Convention:** The key words "MUST", "MUST NOT", "REQUIRED", "SHALL", "SHOULD", "SHOULD NOT", "MAY", and "OPTIONAL" in this document are to be interpreted as described in [RFC 2119](https://www.ietf.org/rfc/rfc2119.txt).

---

## 1. Overview

### 1.1 Problem Statement
Cryptographic secrets (private keys, mnemonics, IAM tokens) are routinely leaked in public code repositories. Existing secret scanners focus on pattern-matching with known prefixes (e.g., `AKIA` for AWS keys) and miss high-entropy secrets that lack structural markers — such as raw EVM private keys, Ed25519 keys, and BIP-39 mnemonic phrases.

Furthermore, scanning at the scale of the entire GitHub public surface requires an architecture that can:
- Process full repository histories (not just HEAD), since deleted secrets remain exploitable.
- Correlate findings cross-repository to detect credential reuse across organisations.
- Classify findings by structural risk context to reduce analyst noise.

No existing open-source tool combines entropic detection, spatial exposure classification, compliance enrichment, and cross-repository correlation in a single distributed pipeline.

### 1.2 Proposed Solution
Xiphos is a **distributed secret intelligence pipeline** that orchestrates thousands of ephemeral Vexil instances to scan public code repositories at massive scale. Each worker performs a full clone (with history), runs Vexil's entropic detection engine, and publishes structured findings to a central intelligence database for cross-repository correlation.

### 1.3 Scope
**In scope:**
- Distributed work queue and worker orchestration
- Full-clone repository scanning with git history (`--git-aware`)
- Cross-repository `value_hash` correlation
- Central intelligence database for findings storage and querying
- GitHub Events API ingestion

**Out of scope:**
- Modifications to the Vexil detection engine itself (consumed as a binary dependency)
- Web UI or dashboard (downstream consumer)
- Notification/alerting system (downstream consumer)

---

## 2. Goals & Non-Goals

### 2.1 Goals
1. Scan **10,000+ public repositories per hour** with full git history.
2. Achieve **< 5% false positive rate** on `application_code` findings with `High` or `Critical` confidence.
3. Detect **cross-repository credential reuse** via `value_hash` correlation within 60 seconds of ingestion.
4. Operate on a fully **open-source, self-hosted stack** (no cloud vendor dependency).
5. Produce **structured intelligence** (JSON with compliance metadata) suitable for downstream consumption by SIEM, Wardex, or analyst tools.

### 2.2 Non-Goals
1. Real-time push notification (webhook-based alerting is a downstream concern).
2. Shallow clone scanning (incompatible with historical archaeology goal).
3. CSV/flat-file output as primary storage (incompatible with cross-repository correlation at scale).
4. Scanning private repositories (requires authentication beyond scope).
5. Modifying or forking the Vexil binary (consumed as-is).

---

## 3. Architecture

### 3.1 System Diagram
```mermaid
graph TD
    subgraph Ingestion
        GH[GitHub Events API] --> ING[Ingestor Service]
        ING --> NJ[NATS JetStream<br>work.scan queue]
    end

    subgraph Orchestration
        NJ --> KEDA[KEDA Autoscaler]
        KEDA --> |scale 0..N| WORKERS
    end

    subgraph Execution
        WORKERS[Worker Pod Pool] --> |git clone --no-shallow| REPO[Full Clone]
        REPO --> VEXIL[Vexil Binary<br>--git-aware --format json]
        VEXIL --> NJ_RES[NATS JetStream<br>work.results queue]
    end

    subgraph Intelligence
        NJ_RES --> COLL[Collector Service]
        COLL --> DB[(PostgreSQL<br>Intelligence DB)]
        DB --> CORR[Correlator<br>value_hash joins]
    end
```

### 3.2 Component Inventory
| Component | Responsibility | Technology | Notes |
|-----------|---------------|------------|-------|
| **Ingestor** | Consume GitHub Events API, extract push events, publish repo URLs to work queue | Go service | MUST handle API rate limits (5,000 req/h per token) |
| **NATS JetStream** | Durable message queue for work distribution and result collection | NATS Server v2.10+ | Two streams: `work.scan` (input), `work.results` (output) |
| **KEDA** | Autoscale worker pods based on `work.scan` queue depth | KEDA v2.x on K8s | Scale-to-zero when queue is empty |
| **Worker** | Clone repo, run Vexil, publish findings | Alpine container + Vexil binary + git | Ephemeral; MUST NOT persist state |
| **Collector** | Consume findings from `work.results`, normalise, insert into DB | Go service | Idempotent inserts (dedup by `value_hash` + `file_path`) |
| **PostgreSQL** | Persistent storage for findings, cross-repo correlation | PostgreSQL 16+ | Indexed on `value_hash`, `confidence`, `exposure_context` |
| **Correlator** | Periodic or triggered cross-repository `value_hash` analysis | SQL query / Go service | Identifies credential reuse across orgs |

### 3.3 Data Flow
1. **Ingestor** polls GitHub Events API for `PushEvent` entries.
2. For each event, Ingestor publishes a message `{ "repo_url": "...", "clone_url": "..." }` to `work.scan`.
3. **KEDA** observes `work.scan` queue depth and scales worker pods accordingly.
4. Each **Worker** pod:
   - Consumes one message from `work.scan`.
   - Performs `git clone` (full, no shallow).
   - Executes `vexil --dir ./repo --git-aware --format json --concurrency 16`.
   - Parses Vexil JSON output.
   - Publishes each finding as a message to `work.results`.
   - Acknowledges the `work.scan` message.
   - Exits (pod is terminated by K8s).
5. **Collector** consumes from `work.results` and inserts findings into PostgreSQL.
6. **Correlator** runs `value_hash` group queries to flag cross-repo reuse.

### 3.4 External Dependencies
| Dependency | Version | Purpose | Risk if Unavailable |
|-----------|---------|---------|---------------------|
| GitHub Events API | v3 | Source of push event metadata | Pipeline stalls; no new repos to scan |
| Vexil binary | v2.6.0+ | Secret detection engine | Workers produce no findings |
| NATS Server | v2.10+ | Message queue | Workers cannot receive/publish work |
| PostgreSQL | 16+ | Intelligence storage | Findings lost (buffered in NATS) |
| Git | 2.40+ | Repository cloning | Workers cannot clone repos |
| KEDA | v2.x | Autoscaling | Manual scaling required |

---

## 4. Data Model

### 4.1 Core Entities

#### Finding (from Vexil output)
```json
{
  "file_path": "src/config/db.go",
  "line_number": 42,
  "secret_type": "AWS Secret Access Key",
  "secret_class": "token",
  "value_hash": "a1b2c3d4e5f6g7h8",
  "redacted_value": "aws_secret_access_key= [REDACTED]",
  "entropy": 4.72,
  "structural_valid": true,
  "confidence": "Critical",
  "exposure_context": "application_code",
  "recency_tier": "active",
  "compliance_controls": ["ISO27001:A.8.12", "NIS2:Art.21(2)(e)"],
  "blast_radius": "runtime",
  "remediation_steps": ["..."]
}
```

#### ScanJob (Xiphos-managed)
```json
{
  "job_id": "uuid",
  "repo_url": "https://github.com/org/repo",
  "clone_url": "https://github.com/org/repo.git",
  "status": "pending | running | completed | failed",
  "worker_id": "pod-name",
  "queued_at": "ISO8601",
  "started_at": "ISO8601",
  "completed_at": "ISO8601",
  "findings_count": 12,
  "files_scanned": 340,
  "error": null
}
```

### 4.2 Storage
- **Engine:** PostgreSQL 16+
- **Rationale:** Relational model supports `value_hash` GROUP BY for cross-repo correlation, JSONB for flexible finding metadata, and mature indexing for query performance.
- **Key indexes:** `(value_hash)`, `(confidence, exposure_context)`, `(repo_url)`, `(queued_at)`.

### 4.3 Data Lifecycle
- Findings MUST be retained for a minimum of **2 years** (aligns with `stale` recency tier boundary).
- ScanJob metadata SHOULD be retained for **90 days** after completion.
- Raw Vexil JSON output MAY be archived to object storage (S3-compatible) for audit trail.

---

## 5. Interfaces

### 5.1 CLI / API Surface

#### Ingestor
```bash
xiphos-ingestor \
  --github-tokens /path/to/tokens.txt \
  --nats-url nats://localhost:4222 \
  --stream work.scan \
  --poll-interval 30s
```

#### Worker
```bash
xiphos-worker \
  --nats-url nats://localhost:4222 \
  --scan-stream work.scan \
  --results-stream work.results \
  --vexil-bin /usr/local/bin/vexil \
  --clone-dir /tmp/repos
```

#### Collector
```bash
xiphos-collector \
  --nats-url nats://localhost:4222 \
  --results-stream work.results \
  --db-dsn "postgres://user:pass@localhost:5432/xiphos"
```

### 5.2 Configuration
| Variable | Default | Description |
|----------|---------|-------------|
| `XIPHOS_NATS_URL` | `nats://localhost:4222` | NATS server connection URL |
| `XIPHOS_DB_DSN` | — | PostgreSQL connection string (REQUIRED) |
| `XIPHOS_GITHUB_TOKENS` | — | Path to file with one GitHub token per line |
| `XIPHOS_VEXIL_CONCURRENCY` | `16` | Vexil internal worker pool size |
| `XIPHOS_CLONE_TIMEOUT` | `120s` | Maximum time for git clone operation |
| `XIPHOS_SCAN_TIMEOUT` | `300s` | Maximum time for Vexil scan per repo |

### 5.3 Output Formats
- Workers produce Vexil JSON envelope (`scan_metadata` + `findings[]`).
- Collector inserts structured records into PostgreSQL.
- Correlator produces intelligence reports as JSON for downstream consumers.

---

## 6. Performance & Capacity

### 6.1 Targets
| Metric | Target | Boundary Condition |
|--------|--------|--------------------|
| Repos scanned per hour | 10,000+ | Full clone with history; repos ≤ 50MB |
| Finding latency (push → DB) | < 5 min | Dependent on KEDA scale-up time (~30s) |
| Worker cold-start | < 2s | Alpine container + Vexil binary + git (~50MB image); collector/ingestor ~20MB |
| Cross-repo correlation | < 60s | SQL GROUP BY on indexed `value_hash` |
| Concurrent workers | 100–1,000 | Limited by K8s cluster capacity and GitHub rate limits |

### 6.2 Bottlenecks & Limits
| Bottleneck | Limit | Mitigation |
|------------|-------|------------|
| GitHub API rate limit | 5,000 req/h per token | Token rotation pool; content-based scanning for push events |
| Clone bandwidth | ~50MB/repo × 10,000/h = ~500GB/h | Cluster egress capacity; geographic distribution |
| NATS throughput | ~1M msgs/s | Not a bottleneck at this scale |
| PostgreSQL write throughput | ~10K inserts/s | Batch inserts; connection pooling (PgBouncer) |

### 6.3 Scaling Strategy
- **Horizontal:** KEDA scales worker pods 0→N based on `work.scan` queue depth.
- **Ingestion:** Multiple Ingestor instances with partitioned token pools.
- **Database:** Read replicas for Correlator queries; primary for Collector writes.

---

## 7. Security & Compliance

### 7.1 Threat Model
- Workers handle **public** repository content only. No authentication tokens for private repos.
- Vexil outputs MUST NOT include raw secret values (`Value` field is `json:"-"`). Only `redacted_value` and `value_hash` are persisted.
- GitHub tokens used by Ingestor MUST be stored in a secrets manager (Vault, K8s Secrets), never in environment variables directly.

### 7.2 Data Handling
- Findings in PostgreSQL contain `value_hash` (one-way) and `redacted_value` only.
- The intelligence database itself is a sensitive asset: access MUST be restricted to authorised analysts.
- Database connections MUST use TLS.

### 7.3 Compliance Requirements
- Vexil findings include compliance mappings (ISO 27001, NIS2, DORA, IEC 62443). These are metadata annotations, not certifications of the Xiphos system itself.

---

## 8. Deployment & Operations

### 8.1 Infrastructure

#### Option A: Kubernetes + KEDA (Primary)
```yaml
# Cluster requirements
Nodes: 3+ (worker nodes auto-scaled by cluster autoscaler)
NATS: Deployed via Helm chart (nats/nats)
KEDA: Deployed via Helm chart (kedacore/keda)
PostgreSQL: Managed (e.g., CloudNativePG) or external
```

#### Option B: HashiCorp Nomad (Alternative)
```hcl
# Lighter alternative for smaller deployments
# Nomad replaces K8s; native NATS integration
# No KEDA required — Nomad has built-in scaling policies
```

### 8.2 Build & Release
- All Xiphos components (Ingestor, Worker, Collector) MUST be built as statically-linked Go binaries.
- Container images MUST use Alpine 3.19+ as base.
- CI pipeline: `go test ./...` → `go build` → `docker build` → push to registry.

### 8.3 Monitoring & Observability
| Signal | Tool | What to Monitor |
|--------|------|-----------------|
| Queue depth | NATS metrics + Grafana | `work.scan` and `work.results` consumer lag |
| Worker health | K8s pod metrics | OOMKills, CrashLoopBackOff, restart count |
| Scan throughput | Custom metrics (Prometheus) | Repos/hour, findings/hour, error rate |
| DB performance | pg_stat_statements | Query latency, connection count, disk usage |

### 8.4 Disaster Recovery
- NATS JetStream provides **durable message replay**. If workers crash, unacknowledged messages are redelivered.
- PostgreSQL backups: daily automated with 30-day retention.
- RTO: 1 hour (redeploy from Helm charts). RPO: 24 hours (last backup).

---

## 9. Testing Strategy

### 9.1 Unit Tests
- All Go components MUST have ≥ 80% line coverage.
- Mock NATS and PostgreSQL interfaces for isolated testing.

### 9.2 Integration Tests
- Docker Compose environment with NATS + PostgreSQL + all three services.
- Test: Ingestor → NATS → Worker → NATS → Collector → PostgreSQL roundtrip.
- Validate: finding arrives in DB with correct `value_hash` and `exposure_context`.

### 9.3 End-to-End / Acceptance Tests
- Scan a curated set of 100 known-vulnerable repositories.
- Validate: ≥ 95% recall on known secrets, < 5% false positive rate on `application_code` findings.

### 9.4 Performance / Load Tests
- Benchmark: 1,000 repos through full pipeline in < 10 minutes.
- Measure: NATS consumer lag, worker utilisation, DB write latency.

---

## 10. Milestones & Deliverables

| Phase | Deliverable | Success Criteria | Target Date |
|-------|------------|------------------|-------------|
| **M0: Foundation** | Go module, project structure, CI pipeline | `go build` + `go test` pass | TBD |
| **M1: Worker** | Standalone worker: clone → vexil → JSON output | Scans 1 repo end-to-end | TBD |
| **M2: Queue** | NATS JetStream integration (publish/consume) | Worker receives job from queue | TBD |
| **M3: Collector** | PostgreSQL ingestion + deduplication | Findings in DB with correct schema | TBD |
| **M4: Ingestor** | GitHub Events API consumer | Push events → work queue | TBD |
| **M5: Orchestration** | KEDA autoscaling on K8s | Scale 0→10→0 based on queue depth | TBD |
| **M6: Correlation** | Cross-repo `value_hash` analysis | Detect reuse across ≥ 2 repos | TBD |
| **M7: Validation** | End-to-end test with 1,000+ repos | Meets performance targets | TBD |

---

## 11. Risks & Mitigations

| Risk | Impact | Probability | Mitigation |
|------|--------|-------------|------------|
| GitHub rate limiting blocks ingestion | Pipeline starves | High | Token rotation pool; exponential backoff |
| Large repos (> 1GB) cause worker OOM | Worker crash loops | Medium | Clone timeout + repo size pre-check via API |
| NATS message loss | Findings dropped | Low | JetStream durable consumers with AckWait |
| PostgreSQL disk exhaustion | Pipeline halts | Medium | Monitoring alerts; data retention policies |
| Vexil false positive flood | DB noise | Medium | Filter by confidence ≥ High before DB insert |

---

## 12. Decision Log

| ID | Decision | Rationale | Date | Status |
|----|----------|-----------|------|--------|
| D-001 | Full clone only (no shallow) | Historical archaeology requires complete git log | 2026-03-20 | Accepted |
| D-002 | PostgreSQL over flat files | Cross-repo `value_hash` correlation requires indexed joins | 2026-03-20 | Accepted |
| D-003 | NATS JetStream as message queue | Durable, lightweight, Go-native; pairs with KEDA for autoscaling | 2026-03-20 | Accepted |
| D-004 | Vexil consumed as binary (not library) | Avoids tight coupling; Vexil evolves independently | 2026-03-20 | Accepted |
| D-005 | Workers are stateless and ephemeral | Simplifies orchestration; no persistent volumes needed | 2026-03-20 | Accepted |

---

## 13. Open Questions

- [ ] Token rotation strategy: single pool or partitioned by Ingestor instance?
- [ ] Repo size pre-check: use GitHub API `size` field or perform a `HEAD` request?
- [ ] Correlator cadence: real-time (trigger on insert) or batch (cron)?
- [ ] Should Worker filter findings by confidence before publishing, or push all to Collector?
- [ ] Nomad vs Kubernetes: which is the primary deployment target?

---

## Appendices

### A. Glossary
| Term | Definition |
|------|-----------|
| **Entropic Detection** | Using Shannon entropy to identify high-randomness strings likely to be cryptographic material |
| **Spatial Exposure** | Classification of a file's structural risk context based on its path |
| **value_hash** | SHA-256 digest (truncated to 16 hex chars) of a detected secret's raw value |
| **Recency Tier** | Temporal classification of a finding based on last commit date: active, recent, stale, archived |
| **Blast Radius** | Assessment of the scope of impact if a finding is a real leak |

### B. References
- [Vexil Repository](https://github.com/had-nu/vexil)
- [NATS JetStream Documentation](https://docs.nats.io/nats-concepts/jetstream)
- [KEDA Documentation](https://keda.sh/docs/)
- [RFC 2119 — Key words for use in RFCs](https://www.ietf.org/rfc/rfc2119.txt)
- [GitHub Events API](https://docs.github.com/en/rest/activity/events)
