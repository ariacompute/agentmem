# agentmem v0.1.0

First release. A Go knowledge hub built on OKF+Git, PostgreSQL/GORM, pluggable
RAG adapters and a rule-first smart router.

## Highlights

- **OKF v0.2 + Git**: permissive frontmatter conformance, progressive
  `index.md`, `log.md`, link graph, trust tiers, Attested Computation contract
  modelling, versioned bodies via go-git.
- **PostgreSQL + GORM**: metadata-only store with versioned SQL migrations,
  transactional upserts, ACL, decision audit, migration cursors and embedding
  fingerprints. No knowledge bodies in the database.
- **Engine plugins**: unified `Adapter` port, built-in `okf`/`memory`/
  `pgvector`, and four first-class Go plugin packages (LlamaIndex, PageIndex,
  GraphRAG, LightRAG) with validated `plugin.yaml` manifests, a declarative
  `sdk_boundary`, lifecycle/health checks and versioned protocol fingerprints.
  All remain offline-mockable.
- **Attested Computation**: parameter binding, read-only `postgres` execution,
  receipt, deterministic Go attester (SQL provenance + display fidelity),
  rejection on failure, and a versioned `computation_runs` audit.
- **Smart routing**: deterministic `R1`–`R7` rules first, AFM-D System One
  fallback with 5000 ms timeout, 256-entry cache, low-score protection and
  fail-closed behaviour. Dual-path reads/writes plus resumable migration.
- **Interfaces**: JSON HTTP API (including `/v1/computations`), ten CLI
  commands (including `compute` / `computations`), pure-function ranking and
  routing evaluation, minimal Vite status page.

## Verification

`go test ./...`, `go vet ./...`, `go build ./cmd/agentmem` and
`make check-boundaries` all pass offline.

## Known limitations

- Python SDK clients are not linked into the Go binary: a sidecar implements
  the line protocol declared by each plugin's `sdk_boundary`.
- `postgres` is the only Attested Computation runtime that executes; the other
  four runtimes (`bigquery`, `dbt`, `python`, `looker`) are formal contract
  skeletons returning `Unsupported`.
- The Vite page is template-level only (health + decisions).
- `002_pgvector.sql` is best-effort; the metadata layer starts without the
  extension and logs a warning.
