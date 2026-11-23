# agentmem engineering context

Read this before modifying the repository. Detailed docs live under `docs/`.

## What this is

A Go 1.27.3 service: OKF+Git canonical knowledge bodies, PostgreSQL/GORM for
server-side structured state, pluggable RAG adapters, and a rule-first smart
router with an AFM-D System One fallback. `web/` is a dependency-free ES module
console (status + HTTP API docs + try-it workbench) bundled into
`internal/webui/dist` and embedded in the binary, which serves it on the same
listener as the API. `AGENTMEM_CONSOLE_ENABLED=false` answers JSON `404` for the
page and its assets, which is the supported pure-API posture.

The same binary is also an MCP server: `internal/mcp` holds the JSON-RPC core,
tool registry and session table, `POST`/`GET`/`DELETE /mcp` is its Streamable
HTTP transport, and `agentmem mcp` is its stdio transport. Both share one
`Backend` port implemented in `internal/service`. See `docs/mcp.md`.

## Layering (single direction, no upward imports)

```
auth / safepath / netsafe / okf / meta / config (L0, zero internal deps)
  -> vcs / adapter / bridges
  -> plugins (engine packages + manifest)
  -> router / eval / compute / graph
  -> mcp (protocol core; consumes ports, imports nothing above service)
  -> service
  -> handlers / cli
  -> cmd/agentmem
```

Ports are defined where they are consumed. `gorm` may only be imported inside
`internal/meta`; no `*gorm.DB` in any port signature. `compute.SQLExecutor` is a
`meta` port so its GORM implementation stays in L1 without an upward import.
Attested Computation bodies live in OKF+Git; PostgreSQL stores only verdicts.

Three security leaf packages are standard-library only, so they never create an
upward import: `internal/auth` (consumed by `handlers` / `cli`),
`internal/safepath` (consumed by `okf` / `vcs`) and `internal/netsafe`
(consumed by `bridges` / `router`).

## Hard rules

- **Offline tests are a hard constraint.** `go test ./...` must pass without
  Postgres, network, or credentials. Every external dependency has a fake.
- **OKF conformance is permissive.** Only `type` is required. Never reject a
  bundle for unknown keys, unknown types, broken links, or a missing index.
- **Git is the only authority for bodies.** PostgreSQL stores metadata only,
  linked by `content_hash` + `commit_sha`. Conflicts resolve OKF-first and are
  surfaced, never hidden.
- **The graph is a projection.** Nodes and typed edges are declared in the
  `graph` / `graph_schema` frontmatter; `graph_nodes` / `graph_edges` are
  discardable and rebuilt from Git. Only a declared edge type with mismatched
  endpoint types is rejected (`422`) — unknown types and dangling targets warn.
  Graph reads never bypass `CheckACL`, and `/mcp` reads never bypass it either.
- **MCP write tools are off by default.** `/mcp` (Streamable HTTP) and
  `agentmem mcp` (stdio) share one protocol core in `internal/mcp`. Read tools
  are always listed; write tools are registered only when
  `AGENTMEM_MCP_WRITE=true`, and every call still checks its own scope
  (`knowledge:write` / `graph:write` / `compute:run` / `migrate:run`) *before*
  reaching `service`, so a forbidden call creates no Git commit. A hidden write
  tool must answer `-32602 unknown tool`, never a distinct status that would
  advertise the disabled surface.
- **Config is `.env` only.** `config.Load` reads `.env` (`--env` /
  `AGENTMEM_ENV_FILE` override the path), then layers process env on top;
  a missing file falls back to `Default()`. Docker Compose consumes the same
  `.env` via `env_file` and `${...}` interpolation.
- **Config validation fails closed.** A closed whitelist of `AGENTMEM_*`
  keys is enforced: unknown keys, malformed values and unimplemented
  capabilities error (the `.env` equivalent of `KnownFields(true)`).
  Non-`AGENTMEM_*` keys such as `POSTGRES_*` belong to Compose and are ignored.
- **Rules run before System One.** A probabilistic choice must never override
  a deterministic authoritative decision. Timeouts/error → fail closed to OKF.
- **No secrets in files or logs.** `.env` is gitignored; `.env.example` holds
  placeholders only. `AGENTMEM_PG_DSN` may be injected by the process env;
  never log bodies, PII, credentials, or GORM SQL parameters. `AGENTMEM_AUTH_TOKENS`
  stores SHA-256 digests only — never a raw bearer value.
- **Identity is established once, at the Handler boundary.** The security
  middleware turns `Authorization: Bearer` into a `Principal`, injects it into
  the `context`, and handlers pass a plain subject string downstream. A subject
  in a request body or query string is ignored, never trusted, and any new route
  must be added to `routePermissions` in `internal/handlers/security.go`.
- **Errors are redacted at the edge.** Clients see a fixed class string and a
  stable status code; internal logs may record the error type (`%T`) but never
  bodies, DSNs, SQL parameters or external stderr.
- **Production fails closed.** `AGENTMEM_MODE=production` requires
  `AGENTMEM_AUTH_ENABLED=true`, at least one token, a DSN without
  `sslmode=disable`, and `AGENTMEM_EGRESS_ALLOWED_HOSTS` whenever an outbound
  endpoint is configured.
- **Schema source is SQL migrations**, not `AutoMigrate`. Movement is versioned
  in `schema_migrations` with an advisory lock.
- **Explicit plugin registry**, not Go `.so` plugins.
- **Web is dependency-free, monochrome and fully localised.** `web/` ships plain
  ES modules with no runtime framework, external CSS or CDN assets. Vite only
  bundles it, and the output directory *is* the embedded package
  (`internal/webui/dist`, set in `web/vite.config.js`); that directory is
  committed so `go build`/`go test ./...` never need bun. Every colour sits on
  the black/white/grey luminance axis (`web/tests/style.test.mjs` enforces it).
  All 13 languages need real translations in `web/src/lib/copy/`; methods,
  paths, field names, status codes and JSON samples are machine data and are
  never translated.
- **The console is a public page; the API is not.** `GET /` and `/assets/*` are
  anonymous like `GET /healthz` (`handlers.Console.Public` is the closed
  allowlist). Every attempt to call the API from the page still needs a bearer
  token, which the try-it workbench keeps in `sessionStorage` only — never in a
  URL, a draft, a log line or `localStorage`. `AGENTMEM_CONSOLE_ENABLED=false`
  is the one supported way to remove that public surface: `cmdServe` then
  leaves `handlers.Options.Console` nil (never a typed-nil `*webui.Handler`) and
  the mux answers JSON `404` for `/` and `/assets/*` while every API route,
  the permission matrix and `/healthz` stay identical.
- **Web rendering is checked with impeccable.** `make web-audit` runs
  `impeccable detect` on the `web/` source (no browser required) and fails the
  build on any anti-pattern. The CLI version is pinned exactly in
  `web/package.json` (`devDependencies`, mirrored in `bun.lock`); never run
  `impeccable@latest` — CI installs from the lockfile and then runs
  `bun run audit`. The full element-level pass — contrast, tiny-text, spacing —
  needs a running app plus a local Chrome/Edge: `cd web && bun run dev` then
  `bun run audit:live`. Keep the monochrome invariant; the `.notice` side-accent
  border is a known slop tell and is intentionally avoided. Shared config lives
  in `web/.impeccable/config.json` (telemetry disabled).
- **Outbound computation is a declared sidecar, never an in-process SDK.**
  `postgres` always executes locally through `meta.SQLExecutor`. `bigquery`,
  `dbt`, `python` and `looker` execute only through a sidecar declared in
  `AGENTMEM_COMPUTE_SIDECARS` (`http` endpoint or bare `cli` command, protocol in
  `docs/compute-sidecar.md`); with no sidecar they stay fail-closed and return
  `Unsupported`. A sidecar receipt must echo the SHA-256 of the body it ran, and
  job id, result and display value are always recomputed in Go.
- **The web page carries its own CSP.** `web/index.html` is served as a static
  file, so the policy is a `Content-Security-Policy` meta with
  `default-src 'none'` and the single inline theme script admitted by SHA-256.
  Editing that script invalidates the digest: rerun `web/tests/security.test.mjs`
  and update the hash — or move the script to `web/src/theme-boot.js`.

## Trust stores

The eMudhra (emSign) weak intermediate CAs are handled twice: Go 1.27.3 ships
the `crypto/x509` blocklist, and `scripts/check-emudhra.sh` scans a system or
image CA bundle for the known root/intermediate SHA-256 fingerprints. Run
`make check-trust-store` before a release and after refreshing a base image. The
scan is offline by design (forward and intercepting proxies produce false
positives); to check a container, export the bundle with
`docker run --rm --entrypoint cat <image> /etc/ssl/certs/ca-certificates.crt`
and scan the file on the host. Never install `openssl` into the runtime image
just for this check.

## Commands

```bash
make test        # go test ./... -count=1
make vet         # go vet ./...
make build       # single binary
make e2e         # black-box acceptance: real binary, CLI + HTTP, fully offline
make eval        # ranking metrics from testdata/eval.jsonl
make validate    # strict validation of .env.example
make check-boundaries  # SDK boundaries stay declarative; no inline annotations
make check-trust-store # offline eMudhra/emSign CA fingerprint scan
cd web && bun test  # offline node:test suites, zero extra dependencies
cd web && bun run build  # rebuild internal/webui/dist (the embedded console)
make web-verify  # rebuild and fail if internal/webui/dist is stale
```

## Where to look

| Concern | Entry point |
|---|---|
| Configuration (`.env`) | `internal/config/config.go`, `dotenv.go`, `.env.example`, `docker-compose.yml` |
| OKF document model | `internal/okf/document.go`, `frontmatter.go` |
| Bundle read/write | `internal/okf/bundle.go` |
| Git backend | `internal/vcs/git.go` |
| GORM models & migrations | `internal/meta/models.go`, `internal/meta/migrations/` |
| Adapter contract | `internal/adapter/port.go`, `registry.go`, `lifecycle.go` |
| HTTP/CLI bridges | `internal/bridges/` |
| Engine plugins & SDK boundary | `internal/plugins/manifest/`, `internal/plugins/<engine>/plugin.yaml` |
| Attested Computation | `internal/compute/`, `internal/meta/postgres_exec.go` |
| Compute sidecars (bigquery/dbt/looker/python) | `internal/compute/sidecar.go`, `internal/compute/runtime.go`, `internal/bridges/outbound.go`, `docs/compute-sidecar.md` |
| Routing rules | `internal/router/rules.go` |
| System One | `internal/router/systemone.go` |
| Write / read / migrate | `internal/router/write.go`, `read.go`, `migrate.go` |
| Assembly | `internal/service/knowledge.go` |
| MCP server (protocol, tools, sessions, both transports) | `internal/mcp/`, `internal/handlers/mcp.go`, `internal/handlers/mcp_http.go`, `internal/cli/mcp.go`, `internal/service/mcp_backend.go`, `docs/mcp.md` |
| API / CLI | `internal/handlers/http.go`, `internal/cli/root.go` |
| Authentication & permissions | `internal/auth/auth.go`, `internal/handlers/security.go` |
| Path safety | `internal/safepath/safepath.go` |
| Outbound allowlist | `internal/netsafe/netsafe.go` |
| Security posture docs | `docs/security.md`, `SECURITY.md` |
| Trust-store scan | `scripts/check-emudhra.sh`, `make check-trust-store` |
| Web shell / views | `web/index.html`, `web/src/main.js`, `web/src/views/` |
| Embedded console, CSP, `AGENTMEM_CONSOLE_ENABLED` | `internal/webui/`, `internal/cli/root.go`, `.env.example` |
| Compute sidecar reference implementation | `examples/compute-sidecars/`, `internal/compute/sidecar.go` |
| Web languages & copy | `web/src/lib/languages.js`, `i18n.js`, `copy/` |
| Web themes | `web/src/lib/theme.js`, `web/src/style.css` |
| API docs skeleton | `web/src/lib/api-content.js`, `internal/handlers/http.go` |
| Web tests | `web/tests/` |

Real third-party engine SDK wiring belongs in a sidecar, not in agentmem. Each
compiled-in engine plugin declares that boundary as data in `plugin.yaml`
(`sdk_boundary`: sidecar, wire protocol version, endpoints/commands, capability
mapping, credential reference) and is validated by `make check-boundaries`.
Do not "silently" replace a bridge with an untestable native client: the offline
mock must stay reachable and observable, and every new capability needs a fake.

The endpoint table in `web/src/lib/api-content.js` is the user-facing half of
`internal/handlers/http.go`: a route, status code or auth requirement change must
be mirrored there, including the `headerLines` bearer/scope/rate-limit rows and
the `statusGroups` table. Request samples never carry a `subject` field.
`tryMeta` is the interactive half of the same table: one entry per route with
its query samples and a `write` flag, so a new endpoint cannot ship without a
request form. `web/tests/api-content.test.mjs` pins the 9 method+path
combinations and the documented statuses, `api-try.test.mjs` pins URL/header
building and the write-confirmation rule, `copy-fallback.test.mjs` demands a
non-English translation for every key, `style.test.mjs` keeps the console
monochrome, and `security.test.mjs` keeps the page CSP honest. The outbound
sidecar wire protocol in `internal/bridges/` is not part of the public API; do
not document `/put`, `/query` or `/delete` as agentmem endpoints.
