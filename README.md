<div align="center">
<a name="readme-top"></a>

# agentmem

**A knowledge hub for AI agents**

It keeps one authoritative, versioned knowledge body in **OKF + Git** and exposes pluggable<br/>
**RAG channels** (LlamaIndex / PageIndex / GraphRAG / LightRAG / pgvector) for the long tail, with a<br/>
**rule-first smart router** in front of both directions: it decides where every write lands and<br/>
where every read is answered from. Server-side structured state lives in **PostgreSQL via GORM**,<br/>
and **Attested Computations** turn sanctioned SQL kept in Git into verified, auditable numbers.

**English** · [简体中文](./README_cn.md) · [Docs](./docs/architecture.md) · [Release notes](./releasenotes.md) · [Security policy](./SECURITY.md)

[![][release-shield]][release-link] [![][go-shield]][go-link] [![][license-shield]][license-link] [![][lastcommit-shield]][lastcommit-link]
<br/>
[![][httpapi-shield]][httpapi-link] [![][tests-shield]][tests-link] [![][docs-shield]][docs-link] [![][i18n-shield]][web-console-link]
</div>

<details>
<summary><kbd>Table of contents</kbd></summary>

#### TOC

**Get started**

- [✨ Features](#-features)
- [💾 Storage split](#-storage-split)
- [🚀 Quick start](#-quick-start)
- [🧰 Command line](#-command-line)
- [🔧 Configuration](#-configuration)

**Deploy & serve**

- [🛳 Deployment](#-deployment)
  - [`A` Run from source](#a-run-from-source)
  - [`B` Docker Compose](#b-docker-compose)
- [🔌 HTTP API](#-http-api)
- [🔐 Authentication and access control](#-authentication-and-access-control)

**Internals**

- [🧭 Smart routing](#-smart-routing)
- [🧩 Engine plugins](#-engine-plugins)
- [🧮 Attested Computations](#-attested-computations)
- [📊 Evaluation](#-evaluation)
- [🌐 Web console](#-web-console)

**Trust & governance**

- [🔏 Trust store security](#-trust-store-security)
- [🔨 Development](#-development)
- [📚 Documentation](#-documentation)
- [📝 License](#-license)
- [🤝 Engineering conventions](#-engineering-conventions)

</details>

## ✨ Features

agentmem answers one question for an agent: *which copy of this knowledge is the real one, and where should this question be answered?* The answer is never duplicated state. The body lives in Git as an **OKF bundle**, PostgreSQL holds only metadata keyed by `content_hash` + `commit_sha`, and every RAG index is a rebuildable projection that can be thrown away and recomputed.

The design draws on [HKUDS/DeepTutor][deeptutor-link] (pluggable retrievers, versioned indexes, offline retrieval evaluation) and [GoogleCloudPlatform/open-knowledge-format][okf-link] (OKF v0.2 frontmatter, progressive disclosure, Git as governance).

- 📜 **Git is the only authority for bodies** — Knowledge is reviewable, diffable and revertible. Postgres never stores a second copy, so there is no dual-body consistency problem.
- 🧭 **Rules run before System One** — A probabilistic choice can never override a deterministic authoritative decision. Ambiguity reaches the model; everything else short-circuits.
- 🔒 **Fail closed, not fail open** — Routing timeouts and errors land on the canonical body. There is no switch to disable that fallback.
- 🧪 **Offline tests are a hard constraint** — `go test ./...` passes without Postgres, network or credentials. Every external dependency has a fake.
- ✅ **User-perspective acceptance** — `make e2e` drives the real binary and HTTP API offline: 10 CLI commands, 9 routes, auth, config fail-closed rules and the web console.
- 🧩 **Explicit engine registry** — Plugins are declared in `plugin.yaml` with a machine-checked `sdk_boundary`. Go `.so` plugins are deliberately avoided.
- 🪶 **Zero-dependency console** — `web/` is plain ES modules, monochrome, 13 locales, no runtime framework and no CDN asset.

<div align="right">

[![][back-to-top]](#readme-top)

</div>

## 💾 Storage split

| Store | Holds | Authority |
|---|---|---|
| OKF bundle in Git | Knowledge bodies (Markdown + YAML frontmatter), `index.md`, `log.md`, sanctioned `.sql` computation bodies | Only canonical body |
| PostgreSQL (GORM) | Concept metadata, projection state, `content_hash` index, embedding fingerprints, routing decisions, computation verdicts, migration cursors, ACL | Only structured state |
| RAG adapters | Rebuildable projections | Never authoritative |

The two stores are linked by `content_hash` + `commit_sha`. PostgreSQL never copies a knowledge body, so there is no dual-body consistency problem. Conflicts are resolved **OKF-first** and explicitly surfaced, never hidden. See [docs/storage.md](./docs/storage.md).

<div align="right">

[![][back-to-top]](#readme-top)

</div>

## 🚀 Quick start

```bash
go test ./...
go run ./cmd/agentmem validate
go run ./cmd/agentmem put --id metrics/churn --type Metric \
  --title "Monthly churn" --description "Churn definition" \
  --body "# Definition" --visibility public --owner alice
go run ./cmd/agentmem query --q "define churn" --subject alice
go run ./cmd/agentmem migrate --direction canonical_to_rag --target memory
go run ./cmd/agentmem decisions --limit 10
make e2e  # user-perspective offline acceptance
```

Without `AGENTMEM_PG_DSN` the service uses a file-backed `MetaStore` fake (snapshot at `data/meta.fake.json`, so separate CLI processes share state), an in-memory vector store and a deterministic System One stub. No network, Postgres or API keys are needed. The snapshot is only an offline fallback; the production path is entirely PostgreSQL. No `.env` is required either: `config.Load` falls back to `Default()` when the file is absent.

The example bundle under `testdata/bundle/` contains a runnable Attested Computation. Copy it into `git.root` before using the `compute` command.

> `make validate` validates `.env.example`, not `.env`. Use `go run ./cmd/agentmem validate --env .env` to check a real configuration.

<div align="right">

[![][back-to-top]](#readme-top)

</div>

## 🧰 Command line

| Command | Key flags | Purpose |
|---|---|---|
| `serve` | `--addr` | Start the HTTP API |
| `put` | `--id` `--type` `--title` `--description` `--body` `--visibility` `--owner` `--subject` | Write a knowledge concept |
| `query` | `--q` `--subject` `--top-k` (5) | Route and execute a query |
| `migrate` | `--direction` `--target` `--job` `--dry-run` `--limit` | Reconcile canonical bodies with RAG projections |
| `decisions` | `--id` `--limit` (20) | Show the routing audit trail |
| `concepts` | — | List canonical concept ids |
| `compute` | `--id` `--subject` `--param name=value` (repeatable) | Execute an Attested Computation |
| `computations` | `--id` `--limit` (20) | Show the Attested Computation verdict audit |
| `validate` | — | Validate configuration |
| `eval` | `--dataset` `--k` (5) | Ranking evaluation on a JSONL dataset |

`serve`, `put`, `query`, `migrate`, `decisions`, `concepts`, `compute`, `computations` and `validate` all accept `--env`; `AGENTMEM_ENV_FILE` selects the default env file. `eval` is the one command that reads no configuration.

<div align="right">

[![][back-to-top]](#readme-top)

</div>

## 🔧 Configuration

Copy `.env.example` to `.env` and adjust it. `config.Load` reads that file (default `.env`, overridable by `--env` or `AGENTMEM_ENV_FILE`) and layers process environment variables on top; a missing file falls back to `Default()`. A closed whitelist of `AGENTMEM_*` keys is enforced: unknown keys, malformed values and unimplemented capabilities are hard errors (the `.env` equivalent of `KnownFields(true)`). Non-`AGENTMEM_*` keys such as `POSTGRES_*` are left for Docker Compose. Secrets live only in the gitignored `.env`, never in versioned files or logs.

| Variable | Default | Effect |
|---|---|---|
| `AGENTMEM_ENV_FILE` | `.env` | Path to the env file |
| `AGENTMEM_ADDR` | `127.0.0.1:8080` | HTTP listen address |
| `AGENTMEM_MODE` | `dev` | `dev` or `production` (production requires a DSN) |
| `AGENTMEM_GIT_ROOT` | `./data/bundle` | Root of the OKF bundle |
| `AGENTMEM_GIT_AUTHOR_NAME` | `agentmem` | Git commit author name |
| `AGENTMEM_GIT_AUTHOR_EMAIL` | `agentmem@localhost` | Git commit author email |
| `AGENTMEM_PG_DSN` | empty | PostgreSQL DSN; empty selects the offline fake |
| `AGENTMEM_VECTOR_DSN` | empty | pgvector DSN; empty reuses `AGENTMEM_PG_DSN` |
| `AGENTMEM_PG_MAX_OPEN_CONNS` | `25` | Max open connections |
| `AGENTMEM_PG_MAX_IDLE_CONNS` | `10` | Max idle connections |
| `AGENTMEM_PG_CONN_MAX_LIFETIME` | `30m` | Connection lifetime |
| `AGENTMEM_PG_LOG_LEVEL` | `warn` | GORM log level |
| `AGENTMEM_CONFLICT_POLICY` | `okf_wins` | Conflict resolution; fixed to `okf_wins` |
| `AGENTMEM_SYSTEMONE_ENDPOINT` | empty | AFM-D System One service endpoint |
| `AGENTMEM_SYSTEMONE_TIMEOUT_MS` | `5000` | System One timeout |
| `AGENTMEM_SYSTEMONE_CACHE_CAPACITY` | `256` | Decision cache size |
| `AGENTMEM_SYSTEMONE_MAX_CHOICES` | `255` | Max candidate count |
| `AGENTMEM_SYSTEMONE_MIN_CONFIDENCE` | `0.5` | Low-score protection threshold |
| `AGENTMEM_SYSTEMONE_FALLBACK` | `okf` | Eligible fallback target for low scores |
| `AGENTMEM_COMPUTE_TIMEOUT_MS` | `5000` | Computation timeout (`0` selects the default) |
| `AGENTMEM_COMPUTE_MAX_ROWS` | `100` | Max result rows (`0` selects the default) |
| `AGENTMEM_ADAPTERS` | `okf` + `memory` | JSON array of adapter entries |
| `AGENTMEM_ACL_DEFAULT_VISIBILITY` | `private` | `private` / `team` / `public` |
| `AGENTMEM_ACL_DEFAULT_OWNER` | `system` | Default owner (also the local subject when auth is off) |
| `AGENTMEM_AUTH_ENABLED` | `false` | Require a bearer token on every route except `GET /healthz` |
| `AGENTMEM_AUTH_TOKENS` | empty | JSON array of `{token_sha256, subject, scopes}`; digests only |
| `AGENTMEM_ALLOWED_ORIGINS` | empty | Comma-separated cross-origin consoles allowed to write |
| `AGENTMEM_EGRESS_ALLOWED_HOSTS` | empty | Outbound host allowlist (`api.example.com`, `*.example.com`) |
| `AGENTMEM_RATE_RPS` / `AGENTMEM_RATE_BURST` | `20` / `40` | Per-client token bucket |
| `AGENTMEM_TEST_PG_DSN` | unset | Optional real-PostgreSQL integration test target; ignored by the service |

Fail-closed routing is unconditional: there is no switch to disable the fallback to the canonical body on timeout or error. `AGENTMEM_EGRESS_ALLOWED_HOSTS` is required **in every mode**, not just production, as soon as an outbound HTTP endpoint is configured (System One, or any non-`cli` adapter); the check runs during `config.Load`, so `validate` catches it before the process starts.

`AGENTMEM_COMPUTE_TIMEOUT_MS` / `AGENTMEM_COMPUTE_MAX_ROWS` bound execution; `0` selects the built-in defaults (5000 ms / 100 rows). `AGENTMEM_ADAPTERS` takes a JSON array such as `[{"name":"okf","kind":"okf","enabled":true}]`; only entries with `"enabled":true` are assembled. `health:read` is an accepted scope but no route currently requires it.

<div align="right">

[![][back-to-top]](#readme-top)

</div>

## 🛳 Deployment

Two supported paths: run the binary against a DSN, or bring up the full stack with Docker Compose.

### `A` Run from source

Edit `.env` first: set `AGENTMEM_MODE=production`, `AGENTMEM_PG_DSN` and `POSTGRES_PASSWORD`.

```bash
cp .env.example .env
go run ./cmd/agentmem serve --env .env
go run ./cmd/agentmem validate --env .env
```

`internal/meta/migrations/001_init.sql` creates the metadata tables; `002_pgvector.sql` is best-effort and only applies when the `vector` extension is available; `003_computations.sql` adds the Attested Computation audit table. Migrations are versioned in `schema_migrations` and applied transactionally with an advisory lock. `AutoMigrate` is never the schema source.

### `B` Docker Compose

The repository ships `docker-compose.yml` (PostgreSQL + agentmem) and a multi-stage `Dockerfile`: the Go backend is built under `golang:1.27.3-alpine`, the static web console is bundled with Bun under `oven/bun:alpine`, and both are assembled into a minimal `alpine:3.21` image that runs as an unprivileged `agentmem` user.

1. Create `.env` from the template:

   ```bash
   cp .env.example .env
   ```

   Compose consumes `.env` twice — for `${POSTGRES_DB}`-style interpolation and as the `env_file` for the app — so no credentials are committed.

2. Set at least the following for a working stack:

   ```dotenv
   POSTGRES_PASSWORD=<strong password>
   AGENTMEM_MODE=production
   AGENTMEM_PG_DSN=postgres://agentmem:<POSTGRES_PASSWORD>@postgres:5432/agentmem?sslmode=require
   AGENTMEM_AUTH_ENABLED=true
   AGENTMEM_AUTH_TOKENS='[{"token_sha256":"<64 hex>","subject":"console","scopes":["knowledge:read","knowledge:write"]}]'
   ```

3. Build and start:

   ```bash
   docker compose up -d --build
   ```

   The `postgres` service uses `pgvector/pgvector:pg16`, is published only on loopback (`127.0.0.1:5432`), and has a `pg_isready` healthcheck; the `agentmem` service waits for it to be healthy, then runs `serve`. The app listens on `0.0.0.0:8080` inside the container and is published only on `127.0.0.1:8080`. Put a TLS-terminating reverse proxy (e.g. Nginx, Caddy) in front before exposing it beyond the host.

4. Verify:

   ```bash
   docker compose ps
   curl -i http://127.0.0.1:8080/healthz
   go run ./cmd/agentmem validate --env .env
   ```

Data and the OKF bundle live in named volumes (`pgdata`, `agentmem_data`); `AGENTMEM_GIT_ROOT` is `/app/data/bundle` inside the container. To seed the bundle, copy `testdata/bundle/` into the volume (e.g. via a temporary container or `docker compose cp` against the running service).

The image ships the built console at `/app/web/dist`, but the Go binary neither embeds nor serves `web/` — the API listens on the 9 documented paths only. Serve that directory with any static file server (or reverse-proxy it alongside the API) if you want the console in the same deployment.

**Trust-store refresh.** After the 2026-10 eMudhra/emSign CA key compromise, re-pull `pgvector/pgvector:pg16` and rebuild the agentmem image to refresh the shipped trust stores; verify offline with `make check-trust-store`.

Logs: `docker compose logs -f agentmem`. Stop: `docker compose down` (add `-v` to also remove volumes).

<div align="right">

[![][back-to-top]](#readme-top)

</div>

## 🔌 HTTP API

| Method | Path | Scope | Purpose |
|---|---|---|---|
| GET | `/healthz` | public | Health of all adapters, plugins and stores |
| POST | `/v1/knowledge` | `knowledge:write` | Routed write (`concept_id`, `document`) |
| GET | `/v1/knowledge?id=` | `knowledge:read` | Canonical read with ACL check |
| POST | `/v1/query` | `knowledge:read` | Routed read with dual-path merge |
| POST | `/v1/migrate` | `migrate:run` | Incremental reconciliation (dry-run/resume) |
| GET | `/v1/decisions` | `audit:read` | Routing audit trail |
| GET | `/v1/concepts` | `knowledge:read` | Canonical concept ids |
| POST | `/v1/computations` | `compute:run` | Execute and attest a sanctioned computation |
| GET | `/v1/computations?concept_id=&limit=` | `audit:read` | Attested Computation verdict audit |

Every route except `GET /healthz` requires the scope above; the same matrix is reproduced in [Authentication and access control](#-authentication-and-access-control). `401` / `403` / `429` match the error table in the console; a registered path with an unsupported method answers `405`. `POST /v1/computations` returns `200` for a verified value, `422` for an attestation failure or an unsupported runtime, `400` for an invalid contract or parameters, and `502` for an executor failure. Failed responses never carry `display_value`, and error bodies never echo SQL, DSNs or driver text.

<div align="right">

[![][back-to-top]](#readme-top)

</div>

## 🔐 Authentication and access control

With `AGENTMEM_AUTH_ENABLED=true`, every route except `GET /healthz` requires `Authorization: Bearer <token>`.

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

Missing or unknown credentials answer `401` with `WWW-Authenticate: Bearer`; a valid token without the required scope answers `403`; exceeding `AGENTMEM_RATE_RPS` answers `429`. Private documents are readable only by their owner (an explicit `acl` grant wins), anonymous writes are refused, and a read denied by ACL is indistinguishable from a missing document (`404`) so existence cannot be probed.

Tokens are static: rotating one means editing `.env` and restarting. `AGENTMEM_MODE=production` refuses to start without authentication, without at least one token, with a DSN containing `sslmode=disable`, or with an outbound endpoint but no `AGENTMEM_EGRESS_ALLOWED_HOSTS`. Identity is established once, at the handler boundary: a subject in a request body or query string is ignored, never trusted. See [docs/security.md](./docs/security.md) for the threat model.

<div align="right">

[![][back-to-top]](#readme-top)

</div>

## 🧭 Smart routing

Deterministic rules run first, in order, and the first match short-circuits:

| Rule | When | Result |
|---|---|---|
| `R1` authoritative write | write + authoritative | canonical only (confidence 1.0) |
| `R2` sensitive write | write + sensitive | canonical only (0.95) |
| `R3` definition query | read + definition | canonical only (1.0) |
| `R4` canonical update | write + existing canonical | canonical + first non-canonical channel (0.9) |
| `R5` large document | write + body ≥ 4096 bytes | canonical + every non-canonical channel (0.85) |
| `R6` exploratory query | read + exploratory | non-canonical channels, canonical as fallback (0.8) |
| `R7` aggregate dual read | read + aggregate | canonical + non-canonical channels (0.75) |

Only ambiguous tasks reach the **AFM-D System One** choice service. The client appends `/v1/systemone` to `AGENTMEM_SYSTEMONE_ENDPOINT` — that path is outbound, it is not a route of this service. It runs with a 5000 ms timeout, a 256-entry decision cache, `min_confidence` low-score protection, a `max_choices` candidate cap, and a fail-closed fallback to the canonical body. The fail-closed fallback is structural: it is hard-set at construction and cannot be configured away.

Routing is **dual-path**: writes decide projections, reads decide query channels, and migration reconciles existing knowledge in both directions. See [docs/routing.md](./docs/routing.md).

<div align="right">

[![][back-to-top]](#readme-top)

</div>

## 🧩 Engine plugins

`internal/adapter` defines the `Adapter` port. Built-ins cover the canonical body and local projections; the four Python-ecosystem engines are first-class Go plugin packages under `internal/plugins/<engine>/`.

| Engine | kind | Wire protocol | Capabilities |
|---|---|---|---|
| OKF (built-in) | `okf` | in-process | canonical lookup, query only |
| Memory (built-in) | `memory` | in-process | vector / semantic |
| pgvector (built-in) | `pgvector` | parameterized SQL | vector / semantic |
| LlamaIndex | `llamaindex` | HTTP | vector / semantic |
| PageIndex | `pageindex` | HTTP | semantic / page_level |
| GraphRAG | `graphrag` | CLI stdio | graph |
| LightRAG | `lightrag` | HTTP | vector / semantic / graph |

Each plugin owns a validated `plugin.yaml` with a declarative `sdk_boundary` (sidecar, `agentmem-bridge/v1` wire protocol, endpoints, capability mapping, credential reference), an `Adapter` + `Lifecycle` implementation, health checks and a versioned protocol fingerprint. The real SDK connection lives behind that boundary — a sidecar implements the line protocol — rather than in a code comment. Go `.so` plugins are deliberately avoided; the registry is explicit and configuration-driven. An empty `endpoint` selects the deterministic offline mock, and `make check-boundaries` keeps the boundary declarative. The generic `http` and `cli` kinds remain available for custom engines. See [docs/plugins.md](./docs/plugins.md) and [docs/adapters.md](./docs/adapters.md).

<div align="right">

[![][back-to-top]](#readme-top)

</div>

## 🧮 Attested Computations

`POST /v1/computations` and `agentmem compute` load a sanctioned computation from OKF+Git, bind only declared parameter values, execute it through the `postgres` runtime, and return a value **only after** a deterministic Go attester verifies SQL provenance and display-value fidelity. `bigquery`, `dbt`, `python` and `looker` are formal contract skeletons returning `Unsupported` — only `postgres` has an executor today. Every outcome (pass, rejection, error) is audited in `computation_runs` (offline: `data/meta.fake.json`).

```bash
go run ./cmd/agentmem compute --id computations/revenue-postgres --param year=2026
go run ./cmd/agentmem computations --limit 10
```

`testdata/bundle/computations/revenue-postgres.md` is a runnable example; see [docs/computations.md](./docs/computations.md).

<div align="right">

[![][back-to-top]](#readme-top)

</div>

## 📊 Evaluation

```bash
go run ./cmd/agentmem eval --dataset testdata/eval.jsonl --k 5
```

Reports Recall@k, Precision@k, nDCG@k, MRR, MAP and HitRate@k as pure functions with no LLM judge. See [docs/architecture.md](./docs/architecture.md) for the metric conventions (coverage recall vs binary hit rate).

<div align="right">

[![][back-to-top]](#readme-top)

</div>

## 🌐 Web console

`web/` is a dependency-free ES module console with two views: **Status** (health + recent routing decisions) and **API docs** (all 9 HTTP method + path combinations, request/response samples, conventions and error codes).

- 13 languages, matching agenthub: `en`, `zh`, `zh-TW`, `es`, `fr`, `de`, `it`, `ru`, `ja`, `ko`, `pt`, `ar`, `hi`. Resolution order is `?lang=` > local storage > browser language > English; English is the fallback for a missing key. Arabic flips the layout to RTL.
- Light and dark themes share one monochrome grey scale. The theme is resolved before the first paint, follows the OS until the visitor picks one, and only an explicit choice is stored in `agentmem.theme`.
- `?view=docs` opens the API documentation directly.
- Methods, paths, field names and JSON samples render verbatim in every locale; only prose is translated. Endpoint data mirrors `internal/handlers/http.go`.
- The page ships its own CSP meta (`default-src 'none'`, same-origin assets, the inline theme bootstrap allowed by SHA-256) and `web/tests/security.test.mjs` fails if the digest and the script ever drift.
- Web rendering is checked with [impeccable][impeccable-link]; the monochrome invariant is kept and the `.notice` side-accent border (a known slop tell) is intentionally avoided.

```bash
# Source-level check (no browser needed)
make web-audit

# Live element-level check: backend → dev server → scan
go run ./cmd/agentmem serve  # repo root, listens on :8080
cd web && bun run dev        # Vite dev server on :5173
bun run audit:live           # = bun x impeccable detect http://localhost:5173

# Save the report (redirect output; findings print to the terminal by default)
bun x impeccable detect http://localhost:5173 --json > impeccable-report.json
bun x impeccable detect http://localhost:5173 > impeccable-report.txt
bun x impeccable detect http://localhost:5173 --quiet
```

Serve `web/` directly with any static file server, or bundle it with Vite (`cd web && bun install --frozen-lockfile && bun run build`). No runtime framework is required, and the Go binary does not depend on a JS runtime. Frontend checks run offline with `cd web && bun test` (Bun's built-in test runner, no extra dependencies).

<div align="right">

[![][back-to-top]](#readme-top)

</div>

## 🔏 Trust store security

The 2026-10 eMudhra (emSign) intermediate CA key compromise is covered in two layers: Go 1.27.3 ships the `crypto/x509` blocklist, and the runtime image refreshes its Alpine `ca-certificates` bundle at build time. `make check-trust-store` scans a system or image CA bundle offline for the known root/intermediate SHA-256 fingerprints (exit `0` clean, `1` hit, `2` unreadable):

```bash
make check-trust-store
docker run --rm --entrypoint cat <image> /etc/ssl/certs/ca-certificates.crt > /tmp/image-ca.crt
scripts/check-emudhra.sh /tmp/image-ca.crt
```

<div align="right">

[![][back-to-top]](#readme-top)

</div>

## 🔨 Development

| Target | Effect |
|---|---|
| `make test` | `go test ./... -count=1` |
| `make vet` | `go vet ./...` |
| `make build` | single binary at `bin/agentmem` |
| `make run` | build, then `serve --env .env` |
| `make validate` | strict validation of `.env.example` |
| `make eval` | ranking metrics from `testdata/eval.jsonl` |
| `make check-boundaries` | enforce declarative SDK boundary + plugin tests |
| `make check-trust-store` | offline eMudhra/emSign CA fingerprint scan |
| `make web` | install the console and build it with Vite |
| `make web-audit` | source-level web rendering/slop check via impeccable |
| `make clean` | remove `bin/` and `web/dist` |
| `make e2e` | user-perspective offline black-box acceptance (`scripts/e2e.sh`) |

`go test ./...` passes without Postgres, network or credentials. Set `AGENTMEM_TEST_PG_DSN` to enable the optional real-PostgreSQL integration test.

`make e2e` runs [`scripts/e2e.sh`](./scripts/e2e.sh), a user-perspective acceptance suite: it compiles the real binary, drives all 10 CLI commands and all 9 HTTP routes, then checks the scope matrix, ACL, rate limiting, config fail-closed rules, Web console static checks and cross-process state. It ends with the existing `go vet` / `go test` / `make validate` / `make check-trust-store` gates. Everything stays offline (`GOPROXY=off`, file-fake store, no Postgres, network or credentials); a missing optional `bun` or `openssl` is reported as `SKIP`, never silently passed.

```bash
make e2e
make e2e E2E_ARGS='--no-gates'         # black-box cases only
./scripts/e2e.sh --filter '^H' --keep  # HTTP cases only; keep the temp workspace
```

See [docs/e2e.md](./docs/e2e.md) for the full case matrix, exit codes and troubleshooting.

<div align="right">

[![][back-to-top]](#readme-top)

</div>

## 📚 Documentation

All detailed docs live under [`docs/`](./docs) and are written in Chinese:

| Document | What it covers |
|---|---|
| [Architecture](./docs/architecture.md) | Layering rules, ports and adapters, the no-upward-import contract |
| [Security](./docs/security.md) | Identity, ACL, egress allowlist, error redaction, production fail-closed |
| [Storage](./docs/storage.md) | OKF+Git bodies vs PostgreSQL metadata, hashes, conflict surfacing |
| [OKF conformance](./docs/okf-conformance.md) | The permissive OKF v0.2 subset: only `type` is required |
| [Adapters](./docs/adapters.md) | The unified `Adapter` contract and capability tags |
| [Engine plugins](./docs/plugins.md) | Four engines as Go packages, declarative `sdk_boundary` |
| [Attested Computation](./docs/computations.md) | Contract, parameter binding, attester chain, verdict audit |
| [Routing](./docs/routing.md) | Rule-first decision, System One, dual-path reconciliation |
| [E2E acceptance](./docs/e2e.md) | User-perspective offline black-box suite, case matrix and troubleshooting |

<div align="right">

[![][back-to-top]](#readme-top)

</div>

## 📝 License

MIT. See [LICENSE](./LICENSE).

<div align="right">

[![][back-to-top]](#readme-top)

</div>

## 🤝 Engineering conventions

This repository follows the Harness Engineering philosophy: the repository is the agent's context, and the documents that govern it are versioned alongside the code.

- [`AGENTS.md`](./AGENTS.md) — agent engineering context entry and directory index
- [`requirements.md`](./requirements.md) — requirements spec (feature boundaries / exceptions / acceptance criteria), human-review-gated before implementation
- [`task.md`](./task.md) — implementation task checklist
- [`SECURITY.md`](./SECURITY.md) — how to report a vulnerability

<div align="right">

[![][back-to-top]](#readme-top)

</div>

---

<div align="center">

Copyright © 2026 [ariacompute][org]. <br/>
This project is [MIT](./LICENSE) licensed.

</div>

<!-- LINK GROUP -->

[org]: https://github.com/ariacompute
[deeptutor-link]: https://github.com/HKUDS/DeepTutor
[okf-link]: https://github.com/GoogleCloudPlatform/open-knowledge-format
[impeccable-link]: https://github.com/pbakaus/impeccable

[release-shield]: https://img.shields.io/github/v/release/ariacompute/agentmem?style=flat-square&labelColor=black&color=555
[release-link]: https://github.com/ariacompute/agentmem/releases
[go-shield]: https://img.shields.io/github/go-mod/go-version/ariacompute/agentmem?style=flat-square&labelColor=black&color=555
[go-link]: ./go.mod
[license-shield]: https://img.shields.io/github/license/ariacompute/agentmem?style=flat-square&labelColor=black&color=555
[license-link]: ./LICENSE
[lastcommit-shield]: https://img.shields.io/github/last-commit/ariacompute/agentmem?style=flat-square&labelColor=black&color=555
[lastcommit-link]: https://github.com/ariacompute/agentmem/commits

[httpapi-shield]: https://img.shields.io/badge/HTTP_API-9_routes-555?style=flat-square&labelColor=black
[httpapi-link]: #-http-api
[tests-shield]: https://img.shields.io/badge/tests-offline_first-555?style=flat-square&labelColor=black
[tests-link]: ./Makefile
[docs-shield]: https://img.shields.io/badge/docs-8_guides-555?style=flat-square&labelColor=black
[docs-link]: ./docs
[i18n-shield]: https://img.shields.io/badge/console-13_locales-555?style=flat-square&labelColor=black
[web-console-link]: #-web-console

[back-to-top]: https://img.shields.io/badge/-BACK_TO_TOP-151515?style=flat-square
