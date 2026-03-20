# Threat Intelligence Goals: The Crypto-Harvest

## 1. Goal: Proactive Detection of Sovereign Keys
Most secret scanners are tuned for Corporate Cloud (AWS/Azure). Xiphos will extend Vexil's detection engine to target **Sovereign Cryptography**:
- **BIP-39 Mnemonics:** Detecting 12/24 word sequences with entropic validation against the BIP-39 wordlist.
- **Ethereum/Bitcoin Private Keys:** Identifying 64-character hex strings. Vexil's `ExposureContext` classifier distinguishes `application_code` findings (critical) from `test_fixture` findings (likely dummy data).
- **Wallet Configuration Files:** Targeting `.json` and `.key` files, prioritised by Vexil's `pathScore()` heuristic which boosts files in `config/`, `secrets/`, and similar high-value directories.

> **Ingestion constraint:** Proactive detection ("seconds after a push") requires real-time event consumption. The GitHub Events API provides push event metadata but not file content — the worker must clone or fetch the modified files within the API rate limit (5,000 req/hour per token).

## 2. Goal: Cross-Repository Credential Reuse Detection
Utilizing Vexil's `value_hash` (SHA-256 truncated to 16 hex chars) to track if the same private key is reused across multiple GitHub organisations. This identifies:
- Development teams sharing production keys across repositories.
- Common breach vectors where one repository leak compromises an entire ecosystem.
- Vexil already flags `duplicate_across_files: true` and sets `credential_reuse_detected` in `scan_metadata` when hash collisions are detected within a single scan. Xiphos extends this to **cross-scan correlation** across a central intelligence database.

## 3. Goal: Contextual Confidence Scoring
Combining Vexil's multi-signal confidence model with keyword proximity analysis:
- **Entropy-based scoring:** `Low` (< 3.8), `Medium` (< 4.2), `High` (< 4.6), `Critical` (≥ 4.6 bits/char).
- **Structural validation:** Offline format verification (JWT header parsing, AWS key prefix validation, GitHub token length checks) raises or lowers confidence by one tier.
- **Keyword proximity:** Augmenting confidence when findings appear near contextual keywords (e.g., "prod", "mainnet", "wallet", "seed") — a planned extension to the existing classifier.
- **Compliance enrichment:** Automatic mapping to ISO 27001, NIS2, DORA, and IEC 62443 controls, with blast radius assessment (`pipeline`, `infrastructure`, `runtime`, `contained`, `minimal`).

---
*Xiphos: Slicing through the noise of the decentralised web.*
