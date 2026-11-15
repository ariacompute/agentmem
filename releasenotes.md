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
- **Web console, compiled in**: a dependency-free ES module page with a status
  view and an in-app HTTP API reference: 9 method+path combinations,
  request/response JSON samples, conventions and error/status codes. Vite
  writes straight into `internal/webui/dist` and `//go:embed` compiles it into
  the binary, so `serve` answers `GET /` and `/assets/*` on the same listener as
  the API — no reverse proxy, document root or second process. The page CSP is
  assembled at startup from the inline theme script's SHA-256 while JSON
  answers keep `default-src 'none'`; hashed assets are `immutable` and the page
  is `no-cache`. Prose is fully translated into 13 languages (English fallback,
  Arabic RTL), and a monochrome black/white/grey light/dark theme follows the OS
  until explicitly chosen. Offline `node:test` suites guard copy coverage,
  endpoint drift and the palette.
- **Interactive API reference**: every endpoint row expands into a workbench
  that fires a real same-origin request — editable query parameters and JSON
  body, `Ctrl/Cmd+Enter` to send, then status code, duration, response headers
  and a pretty-printed body. The bearer token lives in `sessionStorage` only and
  travels as an `Authorization` header (never in a URL, a draft or a log), and
  the three state-changing routes require an explicit confirmation before they
  fire. Drafts survive a re-render; results stay in memory.
- **Web rendering checks (impeccable)**: adopted
  [pbakaus/impeccable](https://github.com/pbakaus/impeccable) for design and
  anti-pattern detection. `make web-audit` runs a source-level `impeccable
  detect` over `web/` with no browser, while `bun run audit:live` covers the
  full element-level pass (contrast, tiny-text, spacing) against a running app
  with local Chrome/Edge. The monochrome invariant is preserved: the `.notice`
  side-accent border was removed as a flagged AI-slop tell and tertiary-text
  contrast was raised to meet WCAG AA.
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
- **Offline testability**: every external dependency now sits behind an
  injectable seam — embedded manifests and SQL migrations, the GORM open call,
  temp-file creation in the bundle, the plugin table, and the `os.Exit` /
  assembly / serve-loop entry points — so branches that used to need live
  PostgreSQL, a 100k-entry bundle or a broken manifest are exercised by plain
  `go test ./...` with no database, network or credentials. Helpers such as
  `safepath.Clean`, `nodeFromYAML` and the commit-key derivation in `vcs`
  replaced duplicated path/rel logic, and defensive branches that cannot fail
  were deleted instead of left untested. The router package holds 99.6%
  statement coverage; its two remaining statements are provably unreachable.

## Known limitations

- Python SDK clients are not linked into the Go binary: a sidecar implements
  the line protocol declared by each plugin's `sdk_boundary`.
- Attested Computation covers all five runtimes. `postgres` always executes
  in-process; `bigquery`, `dbt`, `python` and `looker` execute through an
  outbound executor declared in `AGENTMEM_COMPUTE_SIDECARS` (`http` endpoint or
  bare `cli` command, protocol in `docs/compute-sidecar.md`). A runtime with no
  configured sidecar stays fail-closed and returns `Unsupported`; every sidecar
  receipt must echo the SHA-256 of the body it ran or no value is displayed.
- The embedded console is a build artifact: UI changes need `make web` (or
  `bun run build`) so `internal/webui/dist` is refreshed and committed. CI fails
  on a stale copy and `make web-verify` reproduces the check locally. Try-it
  results are in-memory only and are dropped on reload or a theme switch.
- Tokens are static: rotation means editing `.env` and restarting. Distributed
  rate limiting, OIDC/SSO, mTLS and full commit-SHA pinning of actions/images
  remain follow-ups documented in `SECURITY.md`.
- `002_pgvector.sql` is best-effort; the metadata layer starts without the
  extension and logs a warning.
