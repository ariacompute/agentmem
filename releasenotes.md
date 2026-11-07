# agentmem release notes

A Go knowledge hub built on OKF+Git, PostgreSQL/GORM, pluggable RAG adapters
and a rule-first smart router.

## Highlights

- **OKF v0.2 + Git**: permissive frontmatter conformance, progressive
  `index.md`, `log.md`, link graph, trust tiers, Attested Computation contract
  and execution, versioned bodies via go-git.
- **PostgreSQL + GORM**: metadata-only store with versioned SQL migrations
  (`003_computations.sql`), transactional upserts, ACL, decision audit,
  computation verdicts, migration cursors and embedding fingerprints. No
  knowledge bodies, SQL text or result values in the database.
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
  commands (including `compute` / `computations`) and pure-function ranking
  and routing evaluation.
- **Web console**: a dependency-free ES module page with a status view and an
  in-app HTTP API reference: 9 method+path combinations, request/response JSON
  samples, conventions and error/status codes. Prose is fully translated into
  13 languages (English fallback, Arabic RTL), and a monochrome black/white/
  grey light/dark theme follows the OS until explicitly chosen. Offline
  `node:test` suites guard copy coverage, endpoint drift and the palette.
- **`.env` configuration**: a single gitignored `.env` file (template:
  `.env.example`) with a closed `AGENTMEM_*` key whitelist, process-env
  overrides and zero-dependency parsing. Docker Compose consumes the same file
  via `env_file` and `${...}` interpolation, with no credentials in versioned
  files.

## Known limitations

- Python SDK clients are not linked into the Go binary: a sidecar implements
  the line protocol declared by each plugin's `sdk_boundary`.
- `postgres` is the only Attested Computation runtime that executes; the other
  four runtimes (`bigquery`, `dbt`, `python`, `looker`) are formal contract
  skeletons returning `Unsupported`.
- The web console is a static page served by a reverse proxy or any file
  server; the Go binary does not embed it. The API reference is read-only, with
  no interactive request builder.
- `002_pgvector.sql` is best-effort; the metadata layer starts without the
  extension and logs a warning.
