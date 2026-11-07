# agentmem engineering context

Read this before modifying the repository. Detailed docs live under `docs/`.

## What this is

A Go 1.27.3 service: OKF+Git canonical knowledge bodies, PostgreSQL/GORM for
server-side structured state, pluggable RAG adapters, and a rule-first smart
router with an AFM-D System One fallback.

## Layering (single direction, no upward imports)

```
okf / meta / config (L0, zero internal deps)
  -> vcs / adapter / bridges
  -> plugins (engine packages + manifest)
  -> router / eval / compute
  -> service
  -> handlers / cli
  -> cmd/agentmem
```

Ports are defined where they are consumed. `gorm` may only be imported inside
`internal/meta`; no `*gorm.DB` in any port signature. `compute.SQLExecutor` is a
`meta` port so its GORM implementation stays in L1 without an upward import.
Attested Computation bodies live in OKF+Git; PostgreSQL stores only verdicts.

## Hard rules

- **Offline tests are a hard constraint.** `go test ./...` must pass without
  Postgres, network, or credentials. Every external dependency has a fake.
- **OKF conformance is permissive.** Only `type` is required. Never reject a
  bundle for unknown keys, unknown types, broken links, or a missing index.
- **Git is the only authority for bodies.** PostgreSQL stores metadata only,
  linked by `content_hash` + `commit_sha`. Conflicts resolve OKF-first and are
  surfaced, never hidden.
- **Config validation fails closed.** `KnownFields(true)`; unknown keys error;
  unimplemented capabilities return `Unsupported` (no silent no-op).
- **Rules run before System One.** A probabilistic choice must never override
  a deterministic authoritative decision. Timeouts/error → fail closed to OKF.
- **No secrets in files or logs.** `AGENTMEM_PG_DSN` via environment only;
  never log bodies, PII, credentials, or GORM SQL parameters.
- **Schema source is SQL migrations**, not `AutoMigrate`. Movement is versioned
  in `schema_migrations` with an advisory lock.
- **Explicit plugin registry**, not Go `.so` plugins.

## Commands

```bash
make test        # go test ./... -count=1
make vet         # go vet ./...
make build       # single binary
make eval        # ranking metrics from testdata/eval.jsonl
make validate    # strict config validation
make check-boundaries  # SDK boundaries stay declarative; no inline annotations
```

## Where to look

| Concern | Entry point |
|---|---|
| OKF document model | `internal/okf/document.go`, `frontmatter.go` |
| Bundle read/write | `internal/okf/bundle.go` |
| Git backend | `internal/vcs/git.go` |
| GORM models & migrations | `internal/meta/models.go`, `internal/meta/migrations/` |
| Adapter contract | `internal/adapter/port.go`, `registry.go`, `lifecycle.go` |
| HTTP/CLI bridges | `internal/bridges/` |
| Engine plugins & SDK boundary | `internal/plugins/manifest/`, `internal/plugins/<engine>/plugin.yaml` |
| Attested Computation | `internal/compute/`, `internal/meta/postgres_exec.go` |
| Routing rules | `internal/router/rules.go` |
| System One | `internal/router/systemone.go` |
| Write / read / migrate | `internal/router/write.go`, `read.go`, `migrate.go` |
| Assembly | `internal/service/knowledge.go` |
| API / CLI | `internal/handlers/http.go`, `internal/cli/root.go` |

Real third-party engine SDK wiring belongs in a sidecar, not in agentmem. Each
compiled-in engine plugin declares that boundary as data in `plugin.yaml`
(`sdk_boundary`: sidecar, wire protocol version, endpoints/commands, capability
mapping, credential reference) and is validated by `make check-boundaries`.
Do not "silently" replace a bridge with an untestable native client: the offline
mock must stay reachable and observable, and every new capability needs a fake.
