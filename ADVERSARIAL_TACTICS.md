# Xiphos: Threat Modelling & Distributed Deployment Scenarios

This document describes plausible deployment models and operational patterns for large-scale secret scanning systems. Understanding these scenarios is essential for building effective defenses and for threat intelligence analysts evaluating the risk surface of exposed credentials.

## 1. CI/CD Resource Utilisation (Infrastructure Reuse)
Scanning systems can leverage existing compute infrastructure rather than provisioning dedicated resources.
- **Pipeline-Based Execution:** Automated workflows (GitHub Actions, GitLab Runners) can be configured to run Vexil as a scan step across multiple repositories. Each workflow job clones a target repository, executes the scan, and reports results via structured output (JSON, SARIF).
- **Free-Tier Considerations:** Many CI/CD platforms offer generous compute quotas for open-source projects, making large-scale scanning operationally inexpensive when properly configured within platform terms of service.

## 2. Ephemeral Container Deployment
The Vexil micro-container (< 20MB Alpine image, statically-linked Go binary) enables highly flexible deployment:
- **On-Demand Orchestration:** Using Kubernetes (with KEDA autoscaling based on NATS JetStream queue depth) or HashiCorp Nomad, scan workloads can scale from zero to thousands of concurrent instances based on demand.
- **Multi-Architecture Support:** Since Vexil is written in Go, it compiles natively for multiple architectures (AMD64, ARM64, ARM/MIPS) without runtime dependencies, enabling deployment across heterogeneous infrastructure.

## 3. Distributed Work Coordination
Scaling beyond single-node deployment requires coordination strategies:
- **Queue-Based Distribution:** NATS JetStream provides durable, at-least-once delivery of scan tasks. Each worker consumes repository URLs from the queue, performs the scan, and publishes findings back to a results stream.
- **Deduplication:** Vexil's `value_hash` (SHA-256 truncated to 16 hex chars) enables automated deduplication of findings across workers, preventing duplicate intelligence entries when the same credential appears in multiple repositories.
- **Result Aggregation:** Findings can be consolidated into a central intelligence database or distributed to downstream consumers via structured formats (JSON envelope with `scan_metadata`, or SARIF for integration with security platforms).

## 4. Rate-Limit Management & Operational Considerations
Scanning at scale requires careful management of API constraints:
- **GitHub API Limits:** Authenticated tokens provide 5,000 requests/hour. Scanning 1,000+ repositories requires token rotation or content-based scanning (analyzing pushed event payloads rather than cloning full repositories).
- **Request Distribution:** Load spreading across multiple authenticated identities and staggered request timing helps maintain sustainable throughput without exceeding platform policies.
- **Ingestion vs. Processing:** The GitHub Events API generates approximately 500GB–1TB of event metadata per day. Processing the actual code content at scale requires access to the GitHub Archive Program or a carefully designed crawling strategy.

## 5. Cross-Repository Correlation via ValueHash
The primary intelligence value of large-scale scanning is not individual findings, but **cross-repository correlation**:
- **Credential Reuse Detection:** Using Vexil's `value_hash` and `duplicate_across_files` fields to identify cases where the same private key or token is used across multiple organisations.
- **Exposure Context Analysis:** Combining `ExposureContext` classification (application_code vs. test_fixture) with cross-repository correlation to distinguish genuine production leaks from harmless test data reuse.

---
*Xiphos: Intelligence-at-scale through entropic detection.*
