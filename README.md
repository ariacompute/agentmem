# agentmem

`agentmem` is a knowledge hub for AI agents. It keeps one authoritative,
versioned knowledge body in **OKF + Git**, exposes pluggable **RAG channels**
(LlamaIndex / PageIndex / GraphRAG / LightRAG / pgvector), and uses a
**smart router** to decide where each piece of knowledge is written and where
each query is answered from. Server-side structured data lives in
**PostgreSQL via GORM**.

The design draws on [HKUDS/DeepTutor](https://github.com/HKUDS/DeepTutor)
(pluggable retrievers, versioned indexes, offline retrieval evaluation) and
[GoogleCloudPlatform/open-knowledge-format](https://github.com/GoogleCloudPlatform/open-knowledge-format)
(OKF v0.2 frontmatter, progressive disclosure, Git as governance).

## Storage split

| Store | Holds | Authority |
|---|---|---|
| OKF bundle in Git | Knowledge bodies (Markdown + YAML frontmatter), `index.md`, `log.md` | Only canonical body |
| PostgreSQL (GORM) | Concept metadata, projection state, `content_hash` index, embeddings fingerprints, routing decisions, migration cursors, ACL | Only structured state |
| RAG adapters | Rebuildable projections | Never authoritative |

The two stores are linked by `content_hash` + `commit_sha`. PostgreSQL never
copies a knowledge body, so there is no dual-body consistency problem.
Conflicts are resolved **OKF-first** and explicitly surfaced.

## Quick start (fully offline)

```bash
go test ./...                    # all tests, no services required
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
the production path is entirely PostgreSQL.

## With PostgreSQL

```bash
cp config.example.yaml config.yaml
export AGENTMEM_PG_DSN='postgres://user:pass@localhost:5432/agentmem?sslmode=disable'
export AGENTMEM_MODE=production
docker compose up -d postgres
go run ./cmd/agentmem serve --config config.yaml
```

`internal/meta/migrations/001_init.sql` creates the metadata tables;
`002_pgvector.sql` is best-effort and only applies when the `vector` extension
is available; `003_computations.sql` adds the Attested Computation audit table.
Migrations are versioned in `schema_migrations` and applied transactionally
with an advisory lock. `AutoMigrate` is never the schema source.

## HTTP API

| Method | Path | Purpose |
|---|---|---|
| GET | `/healthz` | Health of all adapters and stores |
| POST | `/v1/knowledge` | Routed write (`concept_id`, `subject`, `document`) |
| GET | `/v1/knowledge?id=&subject=` | Canonical read with ACL check |
| POST | `/v1/query` | Routed read with dual-path merge |
| POST | `/v1/migrate` | Incremental reconciliation (dry-run/resume) |
| GET | `/v1/decisions` | Routing audit trail |
| GET | `/v1/concepts` | Canonical concept ids |
| POST | `/v1/computations` | Execute and attest a sanctioned computation |
| GET | `/v1/computations?concept_id=&limit=` | Attested Computation verdict audit |

## Smart routing

Deterministic rules run first and short-circuit (`R1`–`R7`): authoritative or
sensitive knowledge stays canonical, definition queries go to OKF, exploratory
queries go to RAG, large bodies get long-tail projections. Only ambiguous tasks
reach the **AFM-D System One** choice service (`POST /v1/systemone`), with a
5000 ms timeout, a 256-entry decision cache, `min_confidence` low-score
protection, and fail-closed fallback to the canonical body.

Routing is **dual-path**: writes decide projections, reads decide query
channels, and migration reconciles existing knowledge in both directions.

## Plugins

`internal/adapter` defines the `Adapter` port. Built-ins: `okf`, `memory`,
`pgvector`. The four Python-ecosystem engines are first-class Go plugin packages
under `internal/plugins/<engine>/`, each with a validated `plugin.yaml`, an
`Adapter` + `Lifecycle` implementation, health checks and a versioned protocol
fingerprint. The real SDK boundary is the `sdk_boundary` field in each manifest
(sidecar, wire protocol, endpoints, capability mapping, credential reference),
not a code comment. See [docs/plugins.md](docs/plugins.md) and
[docs/adapters.md](docs/adapters.md).

Go `.so` plugins are deliberately avoided; the registry is explicit and
configuration-driven. `make check-boundaries` verifies the boundary stays
declarative.

## Attested Computations

`POST /v1/computations` / `agentmem compute` load a sanctioned computation from
OKF+Git, bind only declared parameter values, execute it through the `postgres`
runtime, and return a value **only after** a deterministic Go attester verifies
SQL provenance and display-value fidelity. `bigquery`, `dbt`, `python` and
`looker` are formal contract skeletons returning `Unsupported`. Every outcome
is audited in `computation_runs` (offline: `data/meta.fake.json`). See
[docs/computations.md](docs/computations.md).

## Evaluation

`go run ./cmd/agentmem eval --dataset testdata/eval.jsonl --k 5` reports
Recall@k, Precision@k, nDCG@k, MRR, MAP and HitRate@k as pure functions with
no LLM judge. See [docs/architecture.md](docs/architecture.md) for the metric
conventions (coverage recall vs binary hit rate).

## Web status page

`web/` is a dependency-free ES module page (health + recent decisions). It can
be served directly by any static file server, or bundled with Vite when Node is
available (`cd web && npm install && npm run build`). No runtime framework is
required, and the Go binary does not depend on Node.

## Documentation

- [Architecture](docs/architecture.md)
- [Storage: OKF+Git and PostgreSQL/GORM](docs/storage.md)
- [OKF conformance](docs/okf-conformance.md)
- [Adapter plugins](docs/adapters.md)
- [Engine plugins and SDK boundary](docs/plugins.md)
- [Attested Computation](docs/computations.md)
- [Routing and System One](docs/routing.md)

## Non-goals

A full admin UI, native Python SDK clients inside the Go binary (sidecars own
that boundary), non-`postgres` computation executors, and any automatic
`git commit`/`push` of the source repository.

## License

MIT — see [LICENSE](LICENSE).
