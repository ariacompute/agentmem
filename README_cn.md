# agentmem

[English](README.md) | [中文](README_cn.md)

`agentmem` 是面向 Agent 的知识中枢：以 **OKF + Git** 保存唯一权威、可版本化的知识正文，通过可插拔的 **RAG 通道**（LlamaIndex / PageIndex / GraphRAG / LightRAG / pgvector）覆盖长尾检索，并由 **智能路由** 决定每条知识写向哪里、每次查询从哪里取回。服务端自身的结构化数据存放在 **PostgreSQL（GORM）**。Attested Computation 把 Git 中的受批准 SQL 变成经过验证、可审计的数值。

设计参考 [HKUDS/DeepTutor](https://github.com/HKUDS/DeepTutor)（可插拔检索、索引版本化、离线检索评测）与 [GoogleCloudPlatform/open-knowledge-format](https://github.com/GoogleCloudPlatform/open-knowledge-format)（OKF v0.2 frontmatter、渐进式披露、Git 治理）。

## 存储职责

| 存储 | 内容 | 地位 |
|---|---|---|
| Git 中的 OKF bundle | 知识正文（Markdown + YAML frontmatter）、`index.md`、`log.md`、受批准的 `.sql` 计算体 | 正文唯一权威 |
| PostgreSQL（GORM） | 概念元数据、投影状态、`content_hash` 索引、embedding 指纹、路由决策、计算 verdict、迁移游标、ACL | 结构化状态权威 |
| RAG 适配器 | 可重建的投影 | 从不作为权威 |

两者通过 `content_hash` + `commit_sha` 关联；库内不复制正文，因此不存在双正文一致性问题。结论冲突时 **OKF 优先** 且显式标注。

## 快速开始

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

未设置 `AGENTMEM_PG_DSN` 时，服务使用**文件持久化**的 `MetaStore` fake（快照位于 `data/meta.fake.json`，使多个 CLI 进程共享状态）、内存向量库与确定性 System One stub，不需要数据库、网络或任何密钥。该快照仅为离线 fallback，生产路径完全走 PostgreSQL。此路径不需要 `.env`：文件缺失时 `config.Load` 回退 `Default()`。

`testdata/bundle/` 下的示例 bundle 含一个可直接运行的 Attested Computation；使用 `compute` 前请先将其复制到 `git.root`。

## CLI 命令

| 命令 | 用途 |
|---|---|
| `serve` | 启动 HTTP API |
| `put` | 写入知识概念 |
| `query` | 路由并执行查询 |
| `migrate` | 对账权威正文与 RAG 投影 |
| `decisions` | 查看路由决策审计 |
| `concepts` | 列出权威概念 ID |
| `compute` | 执行 Attested Computation（可重复 `--param name=value`） |
| `computations` | 查看 Attested Computation verdict 审计 |
| `validate` | 校验配置 |
| `eval` | 在 JSONL 数据集上跑排序/路由评测 |

`serve`、`put`、`query`、`migrate`、`decisions`、`concepts`、`compute`、`computations`、`validate` 均支持 `--env`；`AGENTMEM_ENV_FILE` 指定默认 env 文件。

## 配置

复制 `.env.example` 为 `.env` 并按需要调整。`config.Load` 读取该文件（默认 `.env`，可由 `--env` 或 `AGENTMEM_ENV_FILE` 覆盖），再把进程环境变量叠加其上；文件缺失时回退 `Default()`。`AGENTMEM_*` 采用封闭白名单：未知键、非法值与未实现能力一律报错（等价于 `KnownFields(true)`）。`POSTGRES_*` 等非 `AGENTMEM_*` 键留给 Docker Compose。密钥只存在于被 git 忽略的 `.env`，不写入版本化文件或日志。

| 变量 | 作用 |
|---|---|
| `AGENTMEM_ENV_FILE` | `.env` 文件路径（默认 `.env`） |
| `AGENTMEM_ADDR` | HTTP 监听地址 |
| `AGENTMEM_MODE` | `dev` 或 `production`（production 必须有 DSN） |
| `AGENTMEM_GIT_ROOT` | OKF bundle 根目录 |
| `AGENTMEM_GIT_AUTHOR_NAME` | Git 提交作者名 |
| `AGENTMEM_GIT_AUTHOR_EMAIL` | Git 提交作者邮箱 |
| `AGENTMEM_PG_DSN` | PostgreSQL DSN |
| `AGENTMEM_VECTOR_DSN` | pgvector DSN；留空则复用 `AGENTMEM_PG_DSN` |
| `AGENTMEM_PG_MAX_OPEN_CONNS` | 最大打开连接数 |
| `AGENTMEM_PG_MAX_IDLE_CONNS` | 最大空闲连接数 |
| `AGENTMEM_PG_CONN_MAX_LIFETIME` | 连接生命周期（如 `30m`） |
| `AGENTMEM_PG_LOG_LEVEL` | GORM 日志级别（默认 `warn`） |
| `AGENTMEM_CONFLICT_POLICY` | 固定为 `okf_wins` |
| `AGENTMEM_SYSTEMONE_ENDPOINT` | AFM-D System One 服务地址 |
| `AGENTMEM_SYSTEMONE_TIMEOUT_MS` | System One 超时 |
| `AGENTMEM_SYSTEMONE_CACHE_CAPACITY` | 决策缓存容量 |
| `AGENTMEM_SYSTEMONE_MAX_CHOICES` | 候选数量上限 |
| `AGENTMEM_SYSTEMONE_MIN_CONFIDENCE` | 低分保护阈值 |
| `AGENTMEM_SYSTEMONE_FALLBACK` | 可用回落目标 |
| `AGENTMEM_COMPUTE_TIMEOUT_MS` | 计算超时（`0` = 默认 5000 ms） |
| `AGENTMEM_COMPUTE_MAX_ROWS` | 最大结果行数（`0` = 默认 100） |
| `AGENTMEM_ADAPTERS` | 适配器条目的 JSON 数组 |
| `AGENTMEM_ACL_DEFAULT_VISIBILITY` | `private` / `team` / `public` |
| `AGENTMEM_ACL_DEFAULT_OWNER` | 默认 owner（关闭鉴权时也是本地主体） |
| `AGENTMEM_AUTH_ENABLED` | 除 `/healthz` 外所有路由要求 Bearer 令牌 |
| `AGENTMEM_AUTH_TOKENS` | `{token_sha256, subject, scopes}` 的 JSON 数组，只存摘要 |
| `AGENTMEM_ALLOWED_ORIGINS` | 允许写入的跨源控制台来源（逗号分隔） |
| `AGENTMEM_EGRESS_ALLOWED_HOSTS` | 出向主机白名单（`api.example.com`、`*.example.com`） |
| `AGENTMEM_RATE_RPS` / `AGENTMEM_RATE_BURST` | 每客户端令牌桶（默认 20 / 40） |

失败关闭是无条件的：不存在关闭「超时/错误回落到主干」的开关。

`AGENTMEM_COMPUTE_TIMEOUT_MS` / `AGENTMEM_COMPUTE_MAX_ROWS` 限定执行边界；`0` 使用内置默认值（5000 ms / 100 行）。`AGENTMEM_ADAPTERS` 接受 JSON 数组，例如 `[{"name":"okf","kind":"okf","enabled":true}]`；仅 `"enabled":true` 的条目会被装配。

## 接入 PostgreSQL

先编辑 `.env`：设置 `AGENTMEM_MODE=production`、`AGENTMEM_PG_DSN` 与 `POSTGRES_PASSWORD`。Compose 同时使用 `.env` 做 `${POSTGRES_DB}` 插值与 `env_file`，因此版本化文件里没有凭据。本地（非 Docker）运行用 `go run ./cmd/agentmem serve --env .env`。

```bash
cp .env.example .env
docker compose up -d --build
go run ./cmd/agentmem validate --env .env
```

`internal/meta/migrations/001_init.sql` 创建元数据表；`002_pgvector.sql` 为尽力而为，仅在 `vector` 扩展可用时生效；`003_computations.sql` 增加 Attested Computation 审计表。迁移版本记录在 `schema_migrations`，通过事务与 advisory lock 应用；`AutoMigrate` 不作为 schema 来源。

## HTTP API

| 方法 | 路径 | 用途 |
|---|---|---|
| GET | `/healthz` | 各适配器、插件与存储的健康状态（公开） |
| POST | `/v1/knowledge` | 路由写入（`concept_id`、`document`） |
| GET | `/v1/knowledge?id=` | 带 ACL 的权威读取 |
| POST | `/v1/query` | 路由查询与双路合并 |
| POST | `/v1/migrate` | 增量对账（dry-run / 断点续跑） |
| GET | `/v1/decisions` | 路由决策审计 |
| GET | `/v1/concepts` | 权威概念列表 |
| POST | `/v1/computations` | 执行并验证受批准计算 |
| GET | `/v1/computations?concept_id=&limit=` | 计算 verdict 审计 |

除 `/healthz` 外每条路由都需要上表中的对应权限；`401` / `403` / `429` 与控制台里的错误码表一致。`POST /v1/computations` 验证通过返回 `200`；attestation 失败或 runtime 未实现返回 `422`；契约/参数非法返回 `400`；执行器故障返回 `502`。失败响应一律不携带 `display_value`，错误体也不会回显 SQL、DSN 或驱动信息。

## 智能路由

有序确定性规则（`R1`–`R7`）优先短路：权威/敏感知识只进主干，定义类查询走 OKF，探索类查询走 RAG，大文档额外投影到长尾通道。仅模糊样本进入 **AFM-D System One** 决策服务（`POST /v1/systemone`），具备 5000ms 超时、256 条决策缓存、`min_confidence` 低分保护与失败关闭回落到主干。

路由为**读写双程**：写入决定投影，读取决定查询通道，迁移负责存量知识双向对账。

## 引擎插件

`internal/adapter` 定义 `Adapter` 端口。内置实现覆盖权威正文与本地投影；四个 Python 生态引擎是 `internal/plugins/<engine>/` 下的一等 Go 插件包。

| 引擎 | kind | 线协议 | 能力标签 |
|---|---|---|---|
| OKF（内置） | `okf` | 进程内 | 权威查找，仅查询 |
| Memory（内置） | `memory` | 进程内 | vector / semantic |
| pgvector（内置） | `pgvector` | 参数化 SQL | vector / semantic |
| LlamaIndex | `llamaindex` | HTTP | vector / semantic |
| PageIndex | `pageindex` | HTTP | semantic / page_level |
| GraphRAG | `graphrag` | CLI stdio | graph |
| LightRAG | `lightrag` | HTTP | vector / semantic / graph |

每个插件持有严格校验的 `plugin.yaml`，以声明式 `sdk_boundary` 描述 sidecar、`agentmem-bridge/v1` 线协议、端点、能力映射与凭据引用，并实现 `Adapter` + `Lifecycle`、健康检查与版本化协议指纹。真实 SDK 连接位于该边界之后（由 sidecar 实现线协议），而不是写在代码注释里。刻意不使用 Go `.so` 动态插件，注册表为显式配置驱动；`endpoint` 留空即选择确定性离线 mock，`make check-boundaries` 保证边界始终结构化。泛型 `http` 与 `cli` kind 仍可用于自定义引擎。详见 [docs/plugins.md](docs/plugins.md) 与 [docs/adapters.md](docs/adapters.md)。

## 受批准计算

`POST /v1/computations` 与 `agentmem compute` 从 OKF+Git 加载受批准计算，只绑定声明参数值，经 `postgres` runtime 执行；只有确定性 Go attester 校验 SQL provenance 与展示值 fidelity 通过后才返回数值。`bigquery` / `dbt` / `python` / `looker` 为正式契约骨架，返回 `Unsupported`。成功、拒绝与错误都写入 `computation_runs` 审计（离线为 `data/meta.fake.json`）。

```bash
go run ./cmd/agentmem compute --id computations/revenue-postgres --param year=2026
go run ./cmd/agentmem computations --limit 10
```

`testdata/bundle/computations/revenue-postgres.md` 是可运行示例；详见 [docs/computations.md](docs/computations.md)。

## 评测

`go run ./cmd/agentmem eval --dataset testdata/eval.jsonl --k 5` 输出 Recall@k、Precision@k、nDCG@k、MRR、MAP、HitRate@k，全部为纯函数、无 LLM 裁判。两套口径（覆盖率 Recall 与二值 HitRate）的区别见 [docs/architecture.md](docs/architecture.md)。

## Web 控制台

`web/` 是零运行时依赖的原生 ES module 控制台，包含两个视图：**状态**（健康状态 + 最近路由决策）与 **API 文档**（全部 9 个 HTTP method + path 组合、请求/响应样例、通用约定与错误码）。

- 13 种语言，与 agenthub 一致：`en`、`zh`、`zh-TW`、`es`、`fr`、`de`、`it`、`ru`、`ja`、`ko`、`pt`、`ar`、`hi`。优先级为 `?lang=` > 本地存储 > 浏览器语言 > 英文；缺失键回退英文，阿拉伯语自动切换 RTL。
- light / dark 双主题共用一套纯黑白灰灰阶。主题在首屏绘制前解析，未选择时跟随系统，仅显式切换才写入 `agentmem.theme`。
- `?view=docs` 可直接打开 API 文档视图。
- 方法、路径、字段名与 JSON 样例在所有语言下逐字呈现，仅说明文字翻译；端点数据与 `internal/handlers/http.go` 保持一致。

可直接用任意静态服务器托管 `web/`；有 Node 时也可用 Vite 打包（`cd web && npm ci && npm run build`）。Go 二进制不依赖 Node。前端校验使用 Node 内置测试运行器，离线执行且无新增依赖：`cd web && npm test`。页面自带 `Content-Security-Policy` meta（`default-src 'none'`、仅同源资源、内联首屏主题脚本以 SHA-256 白名单放行），`web/tests/security.test.mjs` 会在摘要与脚本文本不一致时失败。

## 开发命令

| 目标 | 作用 |
|---|---|
| `make test` | `go test ./... -count=1` |
| `make vet` | `go vet ./...` |
| `make build` | 生成单一二进制 `bin/agentmem` |
| `make validate` | 严格校验 `.env.example` |
| `make eval` | 基于 `testdata/eval.jsonl` 的排序指标 |
| `make check-boundaries` | 检查声明式 SDK 边界并跑插件测试 |
| `make check-trust-store` | 离线扫描 eMudhra/emSign CA 指纹 |

`go test ./...` 无需 Postgres、网络或凭据即可通过。设置 `AGENTMEM_TEST_PG_DSN` 可启用可选的真实 PostgreSQL 集成测试。

## 身份认证与访问控制

开启 `AGENTMEM_AUTH_ENABLED=true` 后，除 `GET /healthz` 之外的每条路由都需要 `Authorization: Bearer <token>`。

配置里只存令牌的 SHA-256 摘要：

```bash
printf %s 'your-token' | sha256sum
```

```dotenv
AGENTMEM_AUTH_ENABLED=true
AGENTMEM_AUTH_TOKENS='[{"token_sha256":"<64 位十六进制>","subject":"console","scopes":["knowledge:read","knowledge:write"]}]'
```

| 路由 | 权限 |
|---|---|
| `GET /healthz` | 公开 |
| `GET /v1/knowledge`、`POST /v1/query`、`GET /v1/concepts` | `knowledge:read` |
| `POST /v1/knowledge` | `knowledge:write` |
| `POST /v1/migrate` | `migrate:run` |
| `GET /v1/decisions`、`GET /v1/computations` | `audit:read` |
| `POST /v1/computations` | `compute:run` |

令牌缺失或未知返回 `401`（带 `WWW-Authenticate: Bearer`）；令牌缺少权限返回 `403`；超过 `AGENTMEM_RATE_RPS` 返回 `429`。私有文档仅 owner 可读（`acl` 表显式授权优先），匿名写入一律拒绝，因 ACL 被拒的读取与文档不存在同样返回 `404`，无法探测私有文档是否存在。令牌是静态的：轮换需修改 `.env` 并重启，威胁模型见 [docs/security.md](docs/security.md)。

`AGENTMEM_MODE=production` 时，缺少鉴权、没有令牌、DSN 含 `sslmode=disable`，或存在出向端点却没有 `AGENTMEM_EGRESS_ALLOWED_HOSTS`，都会拒绝启动。

## 信任库安全

2026-10 的 eMudhra（emSign）中间 CA 私钥泄露事件由两层兜底：Go 1.27.3 内置 `crypto/x509` 屏蔽列表，运行时镜像在构建时刷新 Alpine 的 `ca-certificates`。`make check-trust-store` 离线扫描系统或镜像 CA bundle 中已知根/中间证书的 SHA-256 指纹（退出码 `0` 干净、`1` 命中、`2` 无法读取）：

```bash
make check-trust-store
docker run --rm --entrypoint cat <image> /etc/ssl/certs/ca-certificates.crt > /tmp/image-ca.crt
scripts/check-emudhra.sh /tmp/image-ca.crt
```

## 文档

- [架构](docs/architecture.md)
- [安全：身份、ACL、出向管控与信任库](docs/security.md)
- [存储：OKF+Git 与 PostgreSQL/GORM](docs/storage.md)
- [OKF 兼容性](docs/okf-conformance.md)
- [适配器插件](docs/adapters.md)
- [引擎插件与 SDK 边界](docs/plugins.md)
- [Attested Computation](docs/computations.md)
- [路由与 System One](docs/routing.md)

## 许可证

MIT.

## 工程约定

本仓库遵循 Harness Engineering：

- [`AGENTS.md`](AGENTS.md)：Agent 工程上下文入口与目录索引
- [`requirements.md`](requirements.md)：需求规格（功能边界 / 异常 / 验收，人审后编码）
- [`task.md`](task.md)：实施清单
