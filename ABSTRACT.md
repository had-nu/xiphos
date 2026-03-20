# Project Xiphos (Ξίφος)
**Status:** Research & Architecture Design  
**Core Objective:** Massive-scale secret intelligence harvesting using the Vexil entropic engine.

## The "Blade" Concept
Xiphos (a short sword used by the hoplites) represents the philosophy of this project: a secondary, razor-sharp instrument designed for close-quarters efficiency. In the context of cybersecurity, it is the tool that slices through the noise (false positives) to find the high-value cryptographic targets that others miss.

## Key Research Pillars

### 1. The Entropic Radar
Leveraging **Shannon Entropy** to detect high-entropy cryptographic keys (BTC/ETH Private Keys, ECDSA, Ed25519) that lack static prefixes. Instead of searching for "what it looks like", Xiphos searches for "what it behaves like" (mathematical randomness). Vexil already implements `shannonEntropy()` with empirical confidence thresholds (`Low` < 3.8, `Medium` < 4.2, `High` < 4.6, `Critical` ≥ 4.6 bits/char) and structural validators for offline format verification.

### 2. Micro-Container Infrastructure
Decoupling the detector from the environment. By using ultra-lightweight **Alpine/Go Micro-containers** (< 20MB), the system achieves sub-second startup times, making it suitable for ephemeral serverless execution. Vexil's multi-stage Dockerfile (Alpine 3.19 + statically-linked Go binary) already produces images in this range.

### 3. Serverless Parallelism (The Swarm)
A scalable architecture where Vexil instances run in parallel to process code repositories at scale.
- **Managed stack:** AWS Lambda / Google Cloud Run (auto-scaling, but vendor-dependent).
- **Open-source stack:** NATS JetStream (message queue) + KEDA (event-driven autoscaler) + Go workers on Kubernetes. Alternative: HashiCorp Nomad for lighter operational overhead.
- **Boundary conditions:**
  - AWS Lambda default concurrency is **1,000 per region per account** — reaching 10,000 requires a formal quota increase request.
  - The **1,000 repos in 30 seconds** target is achievable for small repositories (< 1MB) analyzed via the GitHub Content API. For full clones with history (`--git-aware`), the bottleneck shifts to bandwidth and GitHub API rate limits (5,000 requests/hour per authenticated token).
  - **TB/h** refers to processing capacity, not ingestion capacity. The GitHub Events API generates approximately 500GB–1TB of event metadata per day — not per hour — and events contain metadata, not file content. Processing actual code content at TB/h requires access to the GitHub Archive Program, a partnership, or distributed crawling respecting rate limits.

### 4. High-Fidelity Filtering (Spatial Exposure Model)
Utilizing Vexil's **Spatial Exposure** classifier to automatically categorize findings by structural context (`application_code`, `ci_config`, `infra_config`, `test_fixture`, `example_file`), allowing analysts to focus on high-risk exposure contexts. Combined with compliance enrichment (ISO 27001, NIS2, DORA, IEC 62443) and blast radius assessment, this reduces analyst cognitive load to actionable intelligence only.

---
*Developed as the intelligence-at-scale extension of the Vexil entropic detection engine.*
