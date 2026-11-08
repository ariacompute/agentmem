# 存储：OKF+Git 与 PostgreSQL/GORM

## 职责边界

| | OKF bundle in Git | PostgreSQL（GORM） |
|---|---|---|
| 存什么 | 知识正文、frontmatter、`index.md`、`log.md`、跨链接、受批准计算体（`.sql` 资源） | 概念元数据、投影状态、决策审计、计算 verdict、迁移游标、ACL、引擎指纹 |
| 不存什么 | 服务运行状态 | 知识正文 |
| 权威 | 正文唯一权威 | 结构化状态权威 |
| 关联键 | `content_hash`、`commit_sha` | 同左（外加 `concept_id`） |

库内不复制正文，因此不存在「两份正文谁对」的问题。外部 RAG 是**可重建投影**，永不作为权威。

## OKF bundle

```
data/bundle/
├── index.md                 # 根：子目录清单（渐进式披露）
├── log.md                   # 日期分组变更史
├── metrics/
│   ├── index.md             # 该目录概念清单
│   ├── gross-margin.md      # 概念文档
│   └── log.md
├── computations/
│   ├── revenue-ytd.md       # Attested Computation 契约（bigquery 骨架）
│   └── revenue-postgres.md  # 可执行的 postgres 契约
└── references/
    └── revenue.sql          # 受批准计算体（Bundle.ReadResource 读取）
```

概念 ID = 相对路径去掉 `.md`。所有路径（`Path` / `Get` / `Put` / `Delete` / `ReadResource`）都先经 `internal/safepath.Resolve`：拒绝 NUL、反斜杠、冒号、控制字符、`..` 段、尾部空格/点、Windows 保留名与超过 512 字节的相对路径，并用 `Clean` + `EvalSymlinks` 确认目标（或最深的已存在祖先）仍在 bundle root 内，符号链接逃逸无法成立。

`internal/okf/bundle.go` 的 `Put` 采用 `os.CreateTemp`（权限 `0o600`）+ `fsync` + `rename` 原子写，只重生成所在目录的 `index.md`（不做全树遍历）。`log.md` 新条目按日期插入到最前。目录遍历对条目数与总字节数设上限，frontmatter 解析对文档大小、YAML 节点数/深度与别名展开设上限；**未知键与未知类型仍然接受**，OKF 宽松兼容是硬约束。

## PostgreSQL 表

| 表 | 主要列 | 用途 |
|---|---|---|
| `schema_migrations` | `name`, `applied_at` | 迁移版本记录 |
| `concepts_meta` | `concept_id` PK, `content_hash` (idx), `commit_sha`, `visibility`, `owner`, `status` | 正文与状态的关联 |
| `projections` | `(concept_id, adapter)` UNIQUE, `content_hash`, `fingerprint`, `status` (idx) | 投影对账 |
| `decisions` | `rule_id`, `reason`, `source`, `confidence`, `targets` JSONB | 路由审计 |
| `migration_cursors` | `job` PK, `last_concept_id`, `done` | 断点续跑 |
| `acl` | `(subject, concept_id)` PK, `allow` | 显式授权 |
| `engine_fingerprints` | `adapter` PK, `fingerprint` | embedding 身份 |
| `computation_runs` | `job_id` + `verdict` UNIQUE, `contract_hash` (idx), `params_digest`, `reasons` JSONB | Attested Computation verdict 审计（不存 SQL、参数值、结果值） |
| `concept_vectors`（可选） | `embedding vector(1536)` | pgvector 投影 |

## 迁移

- `internal/meta/migrations/001_init.sql`：基础设施表，幂等 `IF NOT EXISTS`。
- `internal/meta/migrations/002_pgvector.sql`：`CREATE EXTENSION IF NOT EXISTS vector` + HNSW 索引，**尽力而为**：扩展不可用只告警，不阻断元数据层启动。
- `internal/meta/migrations/003_computations.sql`：`computation_runs` 表与索引，`(job_id, verdict)` 唯一键实现幂等审计。
- `PostgresStore.Migrate` 先建 `schema_migrations`，再按文件名顺序在事务内 `pg_advisory_xact_lock` + 检查 + 执行 + 登记。SQL 文件按 `;` 拆分逐条执行（兼容 pgx 扩展协议）；新增 `NNN_*.sql` 会被 embed glob 自动拾取。
- `AutoMigrate` 不作为 schema 来源。

## GORM 边界

- `gorm` 只在 `internal/meta` 中出现；`MetaStore` 端口签名不含 `*gorm.DB`。
- 模型定义在 `internal/meta/models.go`；`StringList`（JSONB）与 `Vector`（pgvector 字面量）实现 `driver.Valuer` / `sql.Scanner`。
- Upsert 使用 `clause.OnConflict{DoUpdates: clause.AssignmentColumns(...)}`；批量写入走事务 / `CreateInBatches`。
- `vector` 列的写入与余弦查询使用参数化原生 SQL（`?::vector`、`<=>`），经 `VectorStore` 窄端口暴露给适配器。
- Logger 默认 `Warn`，`ParameterizedQueries: true`，不输出 SQL 参数值。
- 连接与查询错误在返回或记录前先脱敏：pgx 错误可能携带完整 DSN 与 SQL 参数，只保留错误类型与分类，客户端只看到固定的 `internal error`。
- 连接池通过 `db.DB()` 设置 `MaxOpenConns` / `MaxIdleConns` / `ConnMaxLifetime`；计算执行器在只读事务内按 `AGENTMEM_COMPUTE_MAX_ROWS`（默认 100）截断行数，并由计算服务的整轮 context 超时（`AGENTMEM_COMPUTE_TIMEOUT_MS`）兜住耗时。

## ACL 双写

`visibility` / `owner` 写入 OKF frontmatter 作为可移植副本；PostgreSQL 的 `acl` 表 + `concepts_meta.visibility` 是运行时校验权威。两者不一致时以 PG 为准并输出结构化告警。

主体（subject）只来自 Handler 中间件由令牌推导的 `Principal`，请求体与查询串里的 `subject` 被忽略；写入时 `owner` 缺省取该主体，且不允许把 `owner` 写成别人（否则越权写入者可以接管他人私有概念）。

判定顺序：先查 `acl` 表里 `(subject, concept_id)` 的显式授权（`allow=false` 即拒绝），缺失时才回落到可见性规则：

| 可见性 | 回落规则 |
|---|---|
| `private` | subject 非空且等于 `owner`（强隔离） |
| `team` | subject 非空 |
| `public` | 放行 |

匿名写入一律拒绝（`store: anonymous write rejected`），覆盖 CLI 路径；覆盖他人已有正文同样被拒（`store: write denied for this concept`）。`acl` 查询走 `(subject, concept_id)` 主键集合式查询，不做逐文档全表扫描。读取失败与权限不足对客户端返回同一个 `404`，不泄露私有文档是否存在。

## 离线路径

`AGENTMEM_PG_DSN` 为空时注入 `meta.FakeStore`（同时实现 `MetaStore`、`VectorStore` 与 `SQLExecutor`），`go test ./...` 无需数据库。CLI 每条命令是独立进程，因此 `service.Assemble` 使用 `NewFileFakeStore` 把 fake 的状态序列化到 `data/meta.fake.json`（原子写，进程退出时保存），保证 ACL、投影状态、决策审计与计算 verdict 跨进程可见。该快照位于 OKF bundle 的上级目录，不会被 `Bundle.List()` 扫描，也不会被 `vcs.Commit` 提交。设置 `AGENTMEM_TEST_PG_DSN` 可运行 `TestPostgresIntegration` 验证真实 GORM 后端。
