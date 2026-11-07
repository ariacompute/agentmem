# agentmem

`agentmem` 是面向 Agent 的知识中枢：以 **OKF + Git** 保存唯一权威、可版本化的知识正文，通过可插拔的 **RAG 通道**（LlamaIndex / PageIndex / GraphRAG / LightRAG / pgvector）覆盖长尾检索，并由 **智能路由** 决定每条知识写向哪里、每次查询从哪里取回。服务端自身的结构化数据存放在 **PostgreSQL（GORM）**。

设计参考 [HKUDS/DeepTutor](https://github.com/HKUDS/DeepTutor)（可插拔检索、索引版本化、离线检索评测）与 [GoogleCloudPlatform/open-knowledge-format](https://github.com/GoogleCloudPlatform/open-knowledge-format)（OKF v0.2 frontmatter、渐进式披露、Git 治理）。

## 存储职责

| 存储 | 内容 | 地位 |
|---|---|---|
| Git 中的 OKF bundle | 知识正文（Markdown + YAML frontmatter）、`index.md`、`log.md` | 正文唯一权威 |
| PostgreSQL（GORM） | 概念元数据、投影状态、`content_hash` 索引、embedding 指纹、路由决策、迁移游标、ACL | 结构化状态权威 |
| RAG 适配器 | 可重建的投影 | 从不作为权威 |

两者通过 `content_hash` + `commit_sha` 关联；库内不复制正文，因此不存在双正文一致性问题。结论冲突时 **OKF 优先** 且显式标注。

## 快速开始（完全离线）

```bash
go test ./...
go run ./cmd/agentmem validate
go run ./cmd/agentmem put --id metrics/churn --type Metric \
  --title "月流失率" --description "流失率定义" \
  --body "# 定义" --visibility public --owner alice
go run ./cmd/agentmem query --q "define churn" --subject alice
go run ./cmd/agentmem migrate --direction canonical_to_rag --target memory
go run ./cmd/agentmem decisions --limit 10
```

未设置 `AGENTMEM_PG_DSN` 时，服务使用**文件持久化**的 `MetaStore` fake（快照位于 `data/meta.fake.json`，使多个 CLI 进程共享状态）、内存向量库与确定性 System One stub，不需要数据库、网络或任何密钥。该快照仅为离线 fallback，生产路径完全走 PostgreSQL。

## 接入 PostgreSQL

```bash
cp config.example.yaml config.yaml
export AGENTMEM_PG_DSN='postgres://user:pass@localhost:5432/agentmem?sslmode=disable'
export AGENTMEM_MODE=production
docker compose up -d postgres
go run ./cmd/agentmem serve --config config.yaml
```

`internal/meta/migrations/001_init.sql` 创建元数据表；`002_pgvector.sql` 为尽力而为，仅在 `vector` 扩展可用时生效；`003_computations.sql` 增加 Attested Computation 审计表。迁移版本记录在 `schema_migrations`，通过事务与 advisory lock 应用；`AutoMigrate` 不作为 schema 来源。

## HTTP API

| 方法 | 路径 | 用途 |
|---|---|---|
| GET | `/healthz` | 各适配器与存储健康状态 |
| POST | `/v1/knowledge` | 路由写入（`concept_id`、`subject`、`document`） |
| GET | `/v1/knowledge?id=&subject=` | 带 ACL 的权威读取 |
| POST | `/v1/query` | 路由查询与双路合并 |
| POST | `/v1/migrate` | 增量对账（dry-run / 断点续跑） |
| GET | `/v1/decisions` | 路由决策审计 |
| GET | `/v1/concepts` | 权威概念列表 |
| POST | `/v1/computations` | 执行并验证受批准计算 |
| GET | `/v1/computations?concept_id=&limit=` | 计算 verdict 审计 |

## 智能路由

有序确定性规则（`R1`–`R7`）优先短路：权威/敏感知识只进主干，定义类查询走 OKF，探索类查询走 RAG，大文档额外投影到长尾通道。仅模糊样本进入 **AFM-D System One** 决策服务（`POST /v1/systemone`），具备 5000ms 超时、256 条决策缓存、`min_confidence` 低分保护与失败关闭回落到主干。

路由为**读写双程**：写入决定投影，读取决定查询通道，迁移负责存量知识双向对账。

## 插件

`internal/adapter` 定义 `Adapter` 端口；内置 `okf`、`memory`、`pgvector`。四个 Python 生态引擎是 `internal/plugins/<engine>/` 下的一等 Go 插件包，各自持有严格校验的 `plugin.yaml`、`Adapter` + `Lifecycle` 实现、健康检查与版本化协议指纹。真实 SDK 对接边界由清单的 `sdk_boundary` 正式声明（sidecar、线协议、端点、能力映射、凭据引用），不依赖代码注释。详见 [docs/plugins.md](docs/plugins.md) 与 [docs/adapters.md](docs/adapters.md)。刻意不使用 Go `.so` 动态插件，注册表为显式配置驱动；`make check-boundaries` 保证边界始终结构化。

## Attested Computation

`POST /v1/computations` 或 `agentmem compute` 从 OKF+Git 加载受批准计算，只绑定声明参数值，经 `postgres` runtime 执行；只有确定性 Go attester 校验 SQL provenance 与展示值 fidelity 通过后才返回数值。`bigquery` / `dbt` / `python` / `looker` 为正式契约骨架，返回 `Unsupported`。成功、拒绝与错误都写入 `computation_runs` 审计（离线为 `data/meta.fake.json`）。详见 [docs/computations.md](docs/computations.md)。

## 评测

`go run ./cmd/agentmem eval --dataset testdata/eval.jsonl --k 5` 输出 Recall@k、Precision@k、nDCG@k、MRR、MAP、HitRate@k，全部为纯函数、无 LLM 裁判。两套口径（覆盖率 Recall 与二值 HitRate）的区别见 [docs/architecture.md](docs/architecture.md)。

## Web 状态页

`web/` 是零运行时依赖的原生 ES module 页面（健康状态 + 最近决策）。可直接用任意静态服务器托管；有 Node 时也可用 Vite 打包（`cd web && npm install && npm run build`）。Go 二进制不依赖 Node。

## 文档

- [架构](docs/architecture.md)
- [存储：OKF+Git 与 PostgreSQL/GORM](docs/storage.md)
- [OKF 兼容性](docs/okf-conformance.md)
- [适配器插件](docs/adapters.md)
- [引擎插件与 SDK 边界](docs/plugins.md)
- [Attested Computation](docs/computations.md)
- [路由与 System One](docs/routing.md)

## 非目标

不构建完整管理台；不在 Go 二进制内链接 Python 原生 SDK（由 sidecar 承担该边界）；不实现非 `postgres` 的计算执行器；不自动 commit/push 源码仓库。

## 许可

MIT，见 [LICENSE](LICENSE)。
