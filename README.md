# agentmem

[English](README.md) | [中文](README_cn.md)

`agentmem` is a knowledge hub for AI agents. It keeps one authoritative,
versioned knowledge body in **OKF + Git**, exposes pluggable **RAG channels**
(LlamaIndex / PageIndex / GraphRAG / LightRAG / pgvector), and uses a
**smart router** to decide where each piece of knowledge is written and where
each query is answered from. Server-side structured data lives in
**PostgreSQL via GORM**. Attested Computations turn sanctioned SQL kept in Git
into verified, audited numbers.

The design draws on [HKUDS/DeepTutor](https://github.com/HKUDS/DeepTutor)
(pluggable retrievers, versioned indexes, offline retrieval evaluation) and
[GoogleCloudPlatform/open-knowledge-format](https://github.com/GoogleCloudPlatform/open-knowledge-format)
(OKF v0.2 frontmatter, progressive disclosure, Git as governance).

## Storage split

| Store | Holds | Authority |
|---|---|---|
| OKF bundle in Git | Knowledge bodies (Markdown + YAML frontmatter), `index.md`, `log.md`, sanctioned `.sql` computation bodies | Only canonical body |
| PostgreSQL (GORM) | Concept metadata, projection state, `content_hash` index, embedding fingerprints, routing decisions, computation verdicts, migration cursors, ACL | Only structured state |
| RAG adapters | Rebuildable projections | Never authoritative |

The two stores are linked by `content_hash` + `commit_sha`. PostgreSQL never
copies a knowledge body, so there is no dual-body consistency problem.
Conflicts are resolved **OKF-first** and explicitly surfaced.

## Quick start

```bash
go test ./...
go run ./cmd/agentmem validate
go run ./cmd/agentmem put --id metrics/churn --type Metric \
  --title "Monthly churn" --description "Churn definition" \
  --body "# Definition" --visibility public --owner alice
go run ./cmd/agentmem query --q "define churn" --subject alice
go run ./cmd/agentmem migrate --direction canonical_to_rag --target memory
go run ./cmd/agentmem decisions --limit 10
```

Without `AGENTMEM_PG_DSN`, the service uses a file-backed `MetaStore` fake
(snapshot at `data/meta.fake.json`, so separate CLI processes share state), an
in-memory vector store, and a deterministic System One stub. No network,
Postgres, or API keys are needed. The snapshot is only an offline fallback;
the production path is entirely PostgreSQL. No `.env` is required here:
`config.Load` falls back to `Default()` when the file is absent.

The example bundle under `testdata/bundle/` contains a runnable Attested
Computation; copy it into `git.root` before using the `compute` command.

## CLI commands

| Command | Purpose |
|---|---|
| `serve` | Start the HTTP API |
| `put` | Write a knowledge concept |
| `query` | Route and execute a query |
| `migrate` | Reconcile canonical bodies with RAG projections |
| `decisions` | Show the routing audit trail |
| `concepts` | List canonical concept ids |
| `compute` | Execute an Attested Computation (repeat `--param name=value`) |
| `computations` | Show the Attested Computation verdict audit |
| `validate` | Validate configuration |
| `eval` | Run ranking/routing evaluation on a JSONL dataset |

`serve`, `put`, `query`, `migrate`, `decisions`, `concepts`, `compute`,
`computations` and `validate` all accept `--env`; `AGENTMEM_ENV_FILE` selects
the default env file.

## Configuration

Copy `.env.example` to `.env` and adjust it. `config.Load` reads that file
(default `.env`, overridable by `--env` or `AGENTMEM_ENV_FILE`) and layers
process environment variables on top; a missing file falls back to
`Default()`. A closed whitelist of `AGENTMEM_*` keys is enforced: unknown
keys, malformed values and unimplemented capabilities are hard errors (the
`.env` equivalent of `KnownFields(true)`). Non-`AGENTMEM_*` keys such as
`POSTGRES_*` are left for Docker Compose. Secrets live only in the
gitignored `.env`, never in versioned files or logs.

| Variable | Effect |
|---|---|
| `AGENTMEM_ENV_FILE` | Path to the `.env` file (default `.env`) |
| `AGENTMEM_ADDR` | HTTP listen address |
| `AGENTMEM_MODE` | `dev` or `production` (production requires a DSN) |
| `AGENTMEM_GIT_ROOT` | Root of the OKF bundle |
| `AGENTMEM_GIT_AUTHOR_NAME` | Git commit author name |
| `AGENTMEM_GIT_AUTHOR_EMAIL` | Git commit author email |
| `AGENTMEM_PG_DSN` | PostgreSQL DSN |
| `AGENTMEM_VECTOR_DSN` | pgvector DSN; empty reuses `AGENTMEM_PG_DSN` |
| `AGENTMEM_PG_MAX_OPEN_CONNS` | Max open connections |
| `AGENTMEM_PG_MAX_IDLE_CONNS` | Max idle connections |
| `AGENTMEM_PG_CONN_MAX_LIFETIME` | Connection lifetime (e.g. `30m`) |
| `AGENTMEM_PG_LOG_LEVEL` | GORM log level (`warn` by default) |
| `AGENTMEM_CONFLICT_POLICY` | Fixed to `okf_wins` |
| `AGENTMEM_SYSTEMONE_ENDPOINT` | AFM-D System One service endpoint |
| `AGENTMEM_SYSTEMONE_TIMEOUT_MS` | System One timeout |
| `AGENTMEM_SYSTEMONE_CACHE_CAPACITY` | Decision cache size |
| `AGENTMEM_SYSTEMONE_MAX_CHOICES` | Max candidate count |
| `AGENTMEM_SYSTEMONE_MIN_CONFIDENCE` | Low-score protection threshold |
| `AGENTMEM_SYSTEMONE_FALLBACK` | Eligible fallback target |
| `AGENTMEM_COMPUTE_TIMEOUT_MS` | Computation timeout (`0` = default 5000 ms) |
| `AGENTMEM_COMPUTE_MAX_ROWS` | Max result rows (`0` = default 100) |
| `AGENTMEM_ADAPTERS` | JSON array of adapter entries |
| `AGENTMEM_ACL_DEFAULT_VISIBILITY` | `private` / `team` / `public` |
| `AGENTMEM_ACL_DEFAULT_OWNER` | Default owner (also the local subject when auth is off) |
| `AGENTMEM_AUTH_ENABLED` | Require a bearer token on every route except `/healthz` |
| `AGENTMEM_AUTH_TOKENS` | JSON array of `{token_sha256, subject, scopes}`; digests only |
| `AGENTMEM_ALLOWED_ORIGINS` | Comma-separated cross-origin consoles allowed to write |
| `AGENTMEM_EGRESS_ALLOWED_HOSTS` | Outbound host allowlist (`api.example.com`, `*.example.com`) |
| `AGENTMEM_RATE_RPS` / `AGENTMEM_RATE_BURST` | Per-client token bucket (default 20 / 40) |

Fail-closed routing is unconditional: there is no switch to disable the fallback
to the canonical body on timeout or error.

`AGENTMEM_COMPUTE_TIMEOUT_MS` / `AGENTMEM_COMPUTE_MAX_ROWS` bound execution;
`0` selects the built-in defaults (5000 ms / 100 rows). `AGENTMEM_ADAPTERS`
takes a JSON array such as `[{"name":"okf","kind":"okf","enabled":true}]`;
only entries with `"enabled":true` are assembled.

## With PostgreSQL

Edit `.env` first: set `AGENTMEM_MODE=production`, `AGENTMEM_PG_DSN` and
`POSTGRES_PASSWORD`. Compose reads `.env` both for `${POSTGRES_DB}`
interpolation and as `env_file`, so no credentials live in a versioned file.
For a local (non-Docker) run use `go run ./cmd/agentmem serve --env .env`.

```bash
cp .env.example .env
docker compose up -d --build
go run ./cmd/agentmem validate --env .env
```

`internal/meta/migrations/001_init.sql` creates the metadata tables;
`002_pgvector.sql` is best-effort and only applies when the `vector` extension
is available; `003_computations.sql` adds the Attested Computation audit table.
Migrations are versioned in `schema_migrations` and applied transactionally
with an advisory lock. `AutoMigrate` is never the schema source.

## Deployment with Docker Compose

The repository ships `docker-compose.yml` (PostgreSQL + agentmem) and a
multi-stage `Dockerfile`: the Go backend is built under `golang:1.27.3-alpine`,
the static web console under `node:20-alpine`, and both are assembled into a
minimal `alpine:3.21` image that runs as an unprivileged `agentmem` user.

1. Create `.env` from the template:

   ```bash
   cp .env.example .env
   ```

   Then edit it (see below). Compose consumes `.env` twice — for
   `${POSTGRES_DB}`-style interpolation and as the `env_file` for the app — so
   no credentials are committed.

2. Set at least the following for a working stack:

   ```dotenv
   POSTGRES_PASSWORD=<strong password>
   AGENTMEM_MODE=production
   AGENTMEM_PG_DSN=postgres://agentmem:<POSTGRES_PASSWORD>@postgres:5432/agentmem?sslmode=require
   AGENTMEM_AUTH_ENABLED=true
   AGENTMEM_AUTH_TOKENS='[{"token_sha256":"<64 hex>","subject":"console","scopes":["knowledge:read","knowledge:write"]}]'
   ```

   In `production`, `AGENTMEM_MODE=production` also requires a DSN without
   `sslmode=disable` and, whenever an outbound endpoint is configured,
   `AGENTMEM_EGRESS_ALLOWED_HOSTS`.

3. Build and start:

   ```bash
   docker compose up -d --build
   ```

   The `postgres` service uses `pgvector/pgvector:pg16`, is published only on
   loopback (`127.0.0.1:5432`), and has a `pg_isready` healthcheck; the
   `agentmem` service waits for it to be healthy, then runs `serve`. The app
   listens on `0.0.0.0:8080` inside the container and is published only on
   `127.0.0.1:8080`. Put a TLS-terminating reverse proxy (e.g. Nginx, Caddy) in
   front before exposing it beyond the host.

4. Verify:

   ```bash
   docker compose ps
   curl -i http://127.0.0.1:8080/healthz
   go run ./cmd/agentmem validate --env .env
   ```

Data and the OKF bundle live in named volumes (`pgdata`, `agentmem_data`);
`AGENTMEM_GIT_ROOT` is `/app/data/bundle` inside the container. To seed the
bundle, copy `testdata/bundle/` into the volume (e.g. via a temporary container
or `docker compose cp` against the running service).

**Trust-store refresh.** After the 2026-10 eMudhra/emSign CA key compromise,
re-pull `pgvector/pgvector:pg16` and rebuild the agentmem image to refresh the
shipped trust stores; verify offline with `make check-trust-store`.

Logs: `docker compose logs -f agentmem`. Stop: `docker compose down` (add `-v`
to also remove volumes).

## HTTP API

| Method | Path | Purpose |
|---|---|---|
| GET | `/healthz` | Health of all adapters, plugins and stores (public) |
| POST | `/v1/knowledge` | Routed write (`concept_id`, `document`) |
| GET | `/v1/knowledge?id=` | Canonical read with ACL check |
| POST | `/v1/query` | Routed read with dual-path merge |
| POST | `/v1/migrate` | Incremental reconciliation (dry-run/resume) |
| GET | `/v1/decisions` | Routing audit trail |
| GET | `/v1/concepts` | Canonical concept ids |
| POST | `/v1/computations` | Execute and attest a sanctioned computation |
| GET | `/v1/computations?concept_id=&limit=` | Attested Computation verdict audit |

Every route except `/healthz` needs the permission listed above; `401` / `403` /
`429` match the error table in the console. `POST /v1/computations` returns `200`
for a verified value, `422` for an attestation failure or an unsupported
runtime, `400` for an invalid contract or parameters, and `502` for an executor
failure. Failed responses never carry `display_value`, and error bodies never
echo SQL, DSNs or driver text.

## Smart routing

Deterministic rules run first and short-circuit (`R1`–`R7`): authoritative or
sensitive knowledge stays canonical, definition queries go to OKF, exploratory
queries go to RAG, large bodies get long-tail projections. Only ambiguous tasks
reach the **AFM-D System One** choice service (`POST /v1/systemone`), with a
5000 ms timeout, a 256-entry decision cache, `min_confidence` low-score
protection, and fail-closed fallback to the canonical body.

Routing is **dual-path**: writes decide projections, reads decide query
channels, and migration reconciles existing knowledge in both directions.

## Engine plugins

`internal/adapter` defines the `Adapter` port. Built-ins cover the canonical
body and local projections; the four Python-ecosystem engines are first-class
Go plugin packages under `internal/plugins/<engine>/`.

| Engine | kind | Wire protocol | Capabilities |
|---|---|---|---|
| OKF (built-in) | `okf` | in-process | canonical lookup, query only |
| Memory (built-in) | `memory` | in-process | vector / semantic |
| pgvector (built-in) | `pgvector` | parameterized SQL | vector / semantic |
| LlamaIndex | `llamaindex` | HTTP | vector / semantic |
| PageIndex | `pageindex` | HTTP | semantic / page_level |
| GraphRAG | `graphrag` | CLI stdio | graph |
| LightRAG | `lightrag` | HTTP | vector / semantic / graph |

Each plugin owns a validated `plugin.yaml` with a declarative `sdk_boundary`
(sidecar, `agentmem-bridge/v1` wire protocol, endpoints, capability mapping,
credential reference), an `Adapter` + `Lifecycle` implementation, health checks
and a versioned protocol fingerprint. The real SDK connection lives behind that
boundary — a sidecar implements the line protocol — rather than in a code
comment. Go `.so` plugins are deliberately avoided; the registry is explicit
and configuration-driven. An empty `endpoint` selects the deterministic
offline mock, and `make check-boundaries` keeps the boundary declarative. The
generic `http` and `cli` kinds remain available for custom engines. See
[docs/plugins.md](docs/plugins.md) and
[docs/adapters.md](docs/adapters.md).

## Attested Computations

`POST /v1/computations` and `agentmem compute` load a sanctioned computation
from OKF+Git, bind only declared parameter values, execute it through the
`postgres` runtime, and return a value **only after** a deterministic Go
attester verifies SQL provenance and display-value fidelity. `bigquery`, `dbt`,
`python` and `looker` are formal contract skeletons returning `Unsupported`.
Every outcome (pass, rejection, error) is audited in `computation_runs`
(offline: `data/meta.fake.json`).

```bash
go run ./cmd/agentmem compute --id computations/revenue-postgres --param year=2026
go run ./cmd/agentmem computations --limit 10
```

`testdata/bundle/computations/revenue-postgres.md` is a runnable example; see
[docs/computations.md](docs/computations.md).

## Evaluation

`go run ./cmd/agentmem eval --dataset testdata/eval.jsonl --k 5` reports
Recall@k, Precision@k, nDCG@k, MRR, MAP and HitRate@k as pure functions with
no LLM judge. See [docs/architecture.md](docs/architecture.md) for the metric
conventions (coverage recall vs binary hit rate).

## Web console

`web/` is a dependency-free ES module console with two views: **Status**
(health + recent routing decisions) and **API docs** (all 9 HTTP method + path
combinations, request/response samples, conventions and error codes).

- 13 languages, matching agenthub: `en`, `zh`, `zh-TW`, `es`, `fr`, `de`, `it`,
  `ru`, `ja`, `ko`, `pt`, `ar`, `hi`. Resolution order is `?lang=` > local
  storage > browser language > English; English is the fallback for a missing
  key. Arabic flips the layout to RTL.
- Light and dark themes share one monochrome grey scale. The theme is resolved
  before the first paint, follows the OS until the visitor picks one, and only
  an explicit choice is stored in `agentmem.theme`.
- `?view=docs` opens the API documentation directly.
- Methods, paths, field names and JSON samples render verbatim in every locale;
  only prose is translated. Endpoint data mirrors `internal/handlers/http.go`.
- Web rendering is checked with
  [impeccable](https://github.com/pbakaus/impeccable): `make web-audit` runs a
  source-level `impeccable detect` over `web/` with no browser; the full
  element-level pass (contrast, tiny-text, spacing) needs a running app plus a
  local Chrome/Edge — `cd web && npm run dev` then `npm run audit:live`. The
  monochrome invariant is kept; the `.notice` side-accent border is
  intentionally avoided as a known slop tell.

Serve `web/` directly with any static file server, or bundle it with Vite
(`cd web && npm ci && npm run build`). No runtime framework is required,
and the Go binary does not depend on Node. Frontend checks run offline with
`cd web && npm test` (Node's built-in test runner, no extra dependencies).
The page ships its own CSP meta (`default-src 'none'`, same-origin assets, the
inline theme bootstrap allowed by SHA-256) and `web/tests/security.test.mjs`
fails if the digest and the script ever drift.

## Development commands

| Target | Effect |
|---|---|
| `make test` | `go test ./... -count=1` |
| `make vet` | `go vet ./...` |
| `make build` | single binary at `bin/agentmem` |
| `make validate` | strict validation of `.env.example` |
| `make eval` | ranking metrics from `testdata/eval.jsonl` |
| `make check-boundaries` | enforce declarative SDK boundary + plugin tests |
| `make check-trust-store` | offline eMudhra/emSign CA fingerprint scan |
| `make web-audit` | source-level web rendering/slop check via impeccable |

`go test ./...` passes without Postgres, network or credentials. Set
`AGENTMEM_TEST_PG_DSN` to enable the optional real-PostgreSQL integration
test.

## Authentication and access control

With `AGENTMEM_AUTH_ENABLED=true`, every route except `GET /healthz` requires
`Authorization: Bearer <token>`.

Configuration stores only the SHA-256 digest of each token:

```bash
printf %s 'your-token' | sha256sum
```

```dotenv
AGENTMEM_AUTH_ENABLED=true
AGENTMEM_AUTH_TOKENS='[{"token_sha256":"<64 hex>","subject":"console","scopes":["knowledge:read","knowledge:write"]}]'
```

| Route | Scope |
|---|---|
| `GET /healthz` | public |
| `GET /v1/knowledge`, `POST /v1/query`, `GET /v1/concepts` | `knowledge:read` |
| `POST /v1/knowledge` | `knowledge:write` |
| `POST /v1/migrate` | `migrate:run` |
| `GET /v1/decisions`, `GET /v1/computations` | `audit:read` |
| `POST /v1/computations` | `compute:run` |

Missing or unknown credentials answer `401` with `WWW-Authenticate: Bearer`; a
valid token without the required scope answers `403`; exceeding
`AGENTMEM_RATE_RPS` answers `429`. Private documents are readable only by their
owner (an explicit `acl` grant wins), anonymous writes are refused, and a read
denied by ACL is indistinguishable from a missing document (`404`) so existence
cannot be probed. Tokens are static: rotating one means editing `.env` and
restarting. See [docs/security.md](docs/security.md) for the threat model.

`AGENTMEM_MODE=production` refuses to start without authentication, without at
least one token, with a DSN containing `sslmode=disable`, or with an outbound
endpoint but no `AGENTMEM_EGRESS_ALLOWED_HOSTS`.

## Trust store security

The 2026-10 eMudhra (emSign) intermediate CA key compromise is covered in two
layers: Go 1.27.3 ships the `crypto/x509` blocklist, and the runtime image
refreshes its Alpine `ca-certificates` bundle at build time. `make
check-trust-store` scans a system or image CA bundle offline for the known
root/intermediate SHA-256 fingerprints (exit `0` clean, `1` hit, `2` unreadable):

```bash
make check-trust-store
docker run --rm --entrypoint cat <image> /etc/ssl/certs/ca-certificates.crt > /tmp/image-ca.crt
scripts/check-emudhra.sh /tmp/image-ca.crt
```

## Documentation

- [Architecture](docs/architecture.md)
- [Security: identity, ACL, egress and trust stores](docs/security.md)
- [Storage: OKF+Git and PostgreSQL/GORM](docs/storage.md)
- [OKF conformance](docs/okf-conformance.md)
- [Adapter plugins](docs/adapters.md)
- [Engine plugins and SDK boundary](docs/plugins.md)
- [Attested Computation](docs/computations.md)
- [Routing and System One](docs/routing.md)

## License

MIT.

## Engineering Conventions

This repository follows the Harness Engineering philosophy:

- [`AGENTS.md`](AGENTS.md): Agent engineering context entry and directory index
- [`requirements.md`](requirements.md): Requirements spec (feature boundaries/exceptions/acceptance criteria, human-review-gated)
- [`task.md`](task.md): Implementation task checklist
