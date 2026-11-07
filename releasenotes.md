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
- **Plugin RAG**: unified `Adapter` port and registry; built-in `okf`,
  `memory`, `pgvector`; HTTP/CLI bridge declarations for LlamaIndex,
  PageIndex, GraphRAG and LightRAG, all offline-mockable.
- **Smart routing**: deterministic `R1`–`R7` rules first, AFM-D System One
  fallback with 5000 ms timeout, 256-entry cache, low-score protection and
  fail-closed behaviour. Dual-path reads/writes plus resumable migration.
- **Interfaces**: JSON HTTP API, eight CLI commands, pure-function ranking
  and routing evaluation, minimal Vite status page.

## Verification

`go test ./...`, `go vet ./...`, `go build ./cmd/agentmem` all pass offline.

## Known limitations

- Four Python-ecosystem engines are bridge contracts with deterministic mocks,
  not native SDK integrations (`TODO(real)`).
- Attested Computations are modelled but never executed.
- The Vite page is template-level only (health + decisions).
- `002_pgvector.sql` is best-effort; the metadata layer starts without the
  extension and logs a warning.
