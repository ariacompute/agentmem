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
- **Security hardening (breaking)**: a request can no longer assert its own
  identity. Bearer tokens are configured as SHA-256 digests, map to a subject
  and scopes, and the five-scope permission matrix covers all nine routes
  (`401` / `403` / `429` are now part of the contract). Private bodies are
  readable only by their owner, and a denied read is indistinguishable from a
  missing document. Error bodies are redacted, outbound calls go through a host
  allow-list with per-hop redirect re-validation, bundle writes are
  symlink-safe and atomic at mode `0600`, OKF/YAML parsing and computation
  execution are bounded, dangerous SQL functions are refused, and the CLI
  bridge only runs bare executable names. Production fails closed without auth,
  without tokens, with `sslmode=disable`, or without an egress allow-list.
  Containers run unprivileged with loopback-only published ports, CI/release use
  least-privilege `permissions`, and `make check-trust-store` scans CA bundles
  offline for the eMudhra (emSign) weak-certificate fingerprints.

## Known limitations

- Python SDK clients are not linked into the Go binary: a sidecar implements
  the line protocol declared by each plugin's `sdk_boundary`.
- `postgres` is the only Attested Computation runtime that executes; the other
  four runtimes (`bigquery`, `dbt`, `python`, `looker`) are formal contract
  skeletons returning `Unsupported`.
- The web console is a static page served by a reverse proxy or any file
  server; the Go binary does not embed it. The API reference is read-only, with
  no interactive request builder.
- Tokens are static: rotation means editing `.env` and restarting. Distributed
  rate limiting, OIDC/SSO, mTLS and full commit-SHA pinning of actions/images
  remain follow-ups documented in `SECURITY.md`.
- `002_pgvector.sql` is best-effort; the metadata layer starts without the
  extension and logs a warning.
