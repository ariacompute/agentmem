<a id="readme-top"></a>

[![][image-banner]][repo-link]

# agentmem

**面向 Agent 的知识中枢**

以 **OKF + Git** 保存唯一权威、可版本化的知识正文，用可插拔的 **RAG 通道**（LlamaIndex / PageIndex / GraphRAG / LightRAG / pgvector）覆盖长尾检索，并由 **规则优先的智能路由** 同时管住两个方向：每条知识写向哪里、每次提问从哪里取回。服务端自身的结构化数据存放在 **PostgreSQL（GORM）**；**Attested Computation** 把 Git 中受批准的 SQL 变成经过验证、可审计的数值。

[English](./README.md) · **简体中文** · [文档](./docs/architecture.md) · [发布说明](./releasenotes_cn.md) · [安全策略](./SECURITY.md)

[![][release-shield]][release-link] [![][go-shield]][go-link] [![][license-shield]][license-link] [![][lastcommit-shield]][lastcommit-link]

[![][httpapi-shield]][httpapi-link] [![][tests-shield]][tests-link] [![][docs-shield]][docs-link] [![][i18n-shield]][web-console-link]

#### 目录

- [👋 agentmem 是什么](#-agentmem-是什么)
- [💾 存储职责](#-存储职责)
- [🚀 快速开始](#-快速开始)
- [🧰 命令行](#-命令行)
- [🔧 配置](#-配置)
- [🛳 部署](#-部署)
  - [`A` 源码运行](#a-源码运行)
  - [`B` Docker Compose](#b-docker-compose)
- [🔌 HTTP API](#-http-api)
- [🧭 智能路由](#-智能路由)
- [🧩 引擎插件](#-引擎插件)
- [🧮 受批准计算](#-受批准计算)
- [📊 评测](#-评测)
- [🌐 Web 控制台](#-web-控制台)
- [🔐 身份认证与访问控制](#-身份认证与访问控制)
- [🔏 信任库安全](#-信任库安全)
- [🔨 开发](#-开发)
- [📚 文档](#-文档)
- [🤝 工程约定](#-工程约定)
- [📝 许可证](#-许可证)

## 👋 agentmem 是什么

agentmem 只回答 Agent 的一个问题：*这份知识的哪一份副本才算数，这个问题又该由谁回答？* 答案绝不是「两份状态」。正文以 **OKF bundle** 形式活在 Git 里，PostgreSQL 只保存以 `content_hash` + `commit_sha` 为键的元数据，每个 RAG 索引都是可以随时丢弃重建的投影。

设计参考 [HKUDS/DeepTutor][deeptutor-link]（可插拔检索、索引版本化、离线检索评测）与 [GoogleCloudPlatform/open-knowledge-format][okf-link]（OKF v0.2 frontmatter、渐进式披露、Git 治理）。

**差异化的六个支点**

| 支点 | 带来的收益 |
|---|---|
| 📜 **Git 是正文唯一权威** | 知识可评审、可 diff、可回滚；Postgres 不存第二份正文，因此不存在双正文一致性问题。 |
| 🧭 **规则先于 System One** | 概率选择永远不能覆盖确定性的权威判断；只有真正模糊的任务才交给模型。 |
| 🔒 **失败关闭而非失败放行** | 路由超时或报错一律回落到主干正文，且不存在关闭该行为的开关。 |
| 🧪 **离线测试是硬约束** | `go test ./...` 无需 Postgres、网络或凭据即可通过，每个外部依赖都有 fake。 |
| 🧩 **显式引擎注册表** | 插件由 `plugin.yaml` 声明，`sdk_boundary` 由机器校验；刻意不使用 Go `.so` 动态插件。 |
| 🪶 **零依赖控制台** | `web/` 是原生 ES module，单色、13 语言、无运行时框架、无 CDN 资源。 |

[![][back-to-top]](#readme-top)

## 💾 存储职责

| 存储 | 内容 | 地位 |
|---|---|---|
| Git 中的 OKF bundle | 知识正文（Markdown + YAML frontmatter）、`index.md`、`log.md`、受批准的 `.sql` 计算体 | 正文唯一权威 |
| PostgreSQL（GORM） | 概念元数据、投影状态、`content_hash` 索引、embedding 指纹、路由决策、计算 verdict、迁移游标、ACL | 结构化状态权威 |
| RAG 适配器 | 可重建的投影 | 从不作为权威 |

两者通过 `content_hash` + `commit_sha` 关联；库内不复制正文，因此不存在双正文一致性问题。结论冲突时 **OKF 优先**，且显式标注、绝不隐藏。详见 [docs/storage.md](./docs/storage.md)。

[![][back-to-top]](#readme-top)

## 🚀 快速开始

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

未设置 `AGENTMEM_PG_DSN` 时，服务使用**文件持久化**的 `MetaStore` fake（快照位于 `data/meta.fake.json`，使多个 CLI 进程共享状态）、内存向量库与确定性 System One stub，不需要数据库、网络或任何密钥。该快照仅为离线 fallback，生产路径完全走 PostgreSQL。此路径也不需要 `.env`：文件缺失时 `config.Load` 回退 `Default()`。

`testdata/bundle/` 下的示例 bundle 含一个可直接运行的 Attested Computation；使用 `compute` 前请先将其复制到 `git.root`。

> `make validate` 校验的是 `.env.example`，不是 `.env`。要校验真实配置，请用 `go run ./cmd/agentmem validate --env .env`。

[![][back-to-top]](#readme-top)

## 🧰 命令行

| 命令 | 主要参数 | 用途 |
|---|---|---|
| `serve` | `--addr` | 启动 HTTP API |
| `put` | `--id` `--type` `--title` `--description` `--body` `--visibility` `--owner` `--subject` | 写入知识概念 |
| `query` | `--q` `--subject` `--top-k`（5） | 路由并执行查询 |
| `migrate` | `--direction` `--target` `--job` `--dry-run` `--limit` | 对账权威正文与 RAG 投影 |
| `decisions` | `--id` `--limit`（20） | 查看路由决策审计 |
| `concepts` | — | 列出权威概念 ID |
| `compute` | `--id` `--subject` `--param name=value`（可重复） | 执行 Attested Computation |
| `computations` | `--id` `--limit`（20） | 查看 Attested Computation verdict 审计 |
| `validate` | — | 校验配置 |
| `eval` | `--dataset` `--k`（5） | 在 JSONL 数据集上跑排序评测 |

`serve`、`put`、`query`、`migrate`、`decisions`、`concepts`、`compute`、`computations`、`validate` 均支持 `--env`；`AGENTMEM_ENV_FILE` 指定默认 env 文件。`eval` 是唯一不读配置的命令。

[![][back-to-top]](#readme-top)

## 🔧 配置

复制 `.env.example` 为 `.env` 并按需调整。`config.Load` 读取该文件（默认 `.env`，可由 `--env` 或 `AGENTMEM_ENV_FILE` 覆盖），再把进程环境变量叠加其上；文件缺失时回退 `Default()`。`AGENTMEM_*` 采用封闭白名单：未知键、非法值与未实现能力一律报错（等价于 `KnownFields(true)`）。`POSTGRES_*` 等非 `AGENTMEM_*` 键留给 Docker Compose。密钥只存在于被 git 忽略的 `.env`，不写入版本化文件或日志。

| 变量 | 默认值 | 作用 |
|---|---|---|
| `AGENTMEM_ENV_FILE` | `.env` | env 文件路径 |
| `AGENTMEM_ADDR` | `127.0.0.1:8080` | HTTP 监听地址 |
| `AGENTMEM_MODE` | `dev` | `dev` 或 `production`（production 必须有 DSN） |
| `AGENTMEM_GIT_ROOT` | `./data/bundle` | OKF bundle 根目录 |
| `AGENTMEM_GIT_AUTHOR_NAME` | `agentmem` | Git 提交作者名 |
| `AGENTMEM_GIT_AUTHOR_EMAIL` | `agentmem@localhost` | Git 提交作者邮箱 |
| `AGENTMEM_PG_DSN` | 空 | PostgreSQL DSN；留空走离线 fake |
| `AGENTMEM_VECTOR_DSN` | 空 | pgvector DSN；留空复用 `AGENTMEM_PG_DSN` |
| `AGENTMEM_PG_MAX_OPEN_CONNS` | `25` | 最大打开连接数 |
| `AGENTMEM_PG_MAX_IDLE_CONNS` | `10` | 最大空闲连接数 |
| `AGENTMEM_PG_CONN_MAX_LIFETIME` | `30m` | 连接生命周期 |
| `AGENTMEM_PG_LOG_LEVEL` | `warn` | GORM 日志级别 |
| `AGENTMEM_CONFLICT_POLICY` | `okf_wins` | 冲突策略，固定为 `okf_wins` |
| `AGENTMEM_SYSTEMONE_ENDPOINT` | 空 | AFM-D System One 服务地址 |
| `AGENTMEM_SYSTEMONE_TIMEOUT_MS` | `5000` | System One 超时 |
| `AGENTMEM_SYSTEMONE_CACHE_CAPACITY` | `256` | 决策缓存容量 |
| `AGENTMEM_SYSTEMONE_MAX_CHOICES` | `255` | 候选数量上限 |
| `AGENTMEM_SYSTEMONE_MIN_CONFIDENCE` | `0.5` | 低分保护阈值 |
| `AGENTMEM_SYSTEMONE_FALLBACK` | `okf` | 低分时可用的回落目标 |
| `AGENTMEM_COMPUTE_TIMEOUT_MS` | `5000` | 计算超时（`0` 选默认值） |
| `AGENTMEM_COMPUTE_MAX_ROWS` | `100` | 最大结果行数（`0` 选默认值） |
| `AGENTMEM_ADAPTERS` | `okf` + `memory` | 适配器条目的 JSON 数组 |
| `AGENTMEM_ACL_DEFAULT_VISIBILITY` | `private` | `private` / `team` / `public` |
| `AGENTMEM_ACL_DEFAULT_OWNER` | `system` | 默认 owner（关闭鉴权时也是本地主体） |
| `AGENTMEM_AUTH_ENABLED` | `false` | 除 `GET /healthz` 外所有路由要求 Bearer 令牌 |
| `AGENTMEM_AUTH_TOKENS` | 空 | `{token_sha256, subject, scopes}` 的 JSON 数组，只存摘要 |
| `AGENTMEM_ALLOWED_ORIGINS` | 空 | 允许写入的跨源控制台来源（逗号分隔） |
| `AGENTMEM_EGRESS_ALLOWED_HOSTS` | 空 | 出向主机白名单（`api.example.com`、`*.example.com`） |
| `AGENTMEM_RATE_RPS` / `AGENTMEM_RATE_BURST` | `20` / `40` | 每客户端令牌桶 |
| `AGENTMEM_TEST_PG_DSN` | 未设置 | 可选的真实 PostgreSQL 集成测试目标；服务本身不读取 |

失败关闭是无条件的：不存在关闭「超时/错误回落到主干」的开关。只要配置了出向 HTTP 端点（System One，或任何非 `cli` 适配器），`AGENTMEM_EGRESS_ALLOWED_HOSTS` 在**任何模式下**都必须设置，而不是只在 production 下；该检查在 `config.Load` 阶段执行，因此进程启动前 `validate` 就会拦住。

`AGENTMEM_COMPUTE_TIMEOUT_MS` / `AGENTMEM_COMPUTE_MAX_ROWS` 限定执行边界；`0` 使用内置默认值（5000 ms / 100 行）。`AGENTMEM_ADAPTERS` 接受 JSON 数组，例如 `[{"name":"okf","kind":"okf","enabled":true}]`；仅 `"enabled":true` 的条目会被装配。`health:read` 是被接受的权限名，但当前没有路由需要它。

[![][back-to-top]](#readme-top)

## 🛳 部署

支持两条路径：直接运行二进制并连 DSN，或用 Docker Compose 拉起整套栈。

### `A` 源码运行

先编辑 `.env`：设置 `AGENTMEM_MODE=production`、`AGENTMEM_PG_DSN` 与 `POSTGRES_PASSWORD`。

```bash
cp .env.example .env
go run ./cmd/agentmem serve --env .env
go run ./cmd/agentmem validate --env .env
```

`internal/meta/migrations/001_init.sql` 创建元数据表；`002_pgvector.sql` 为尽力而为，仅在 `vector` 扩展可用时生效；`003_computations.sql` 增加 Attested Computation 审计表。迁移版本记录在 `schema_migrations`，通过事务与 advisory lock 应用；`AutoMigrate` 不作为 schema 来源。

### `B` Docker Compose

仓库附带 `docker-compose.yml`（PostgreSQL + agentmem）与多阶段 `Dockerfile`：Go 后端在 `golang:1.27.3-alpine` 下构建，静态 web 控制台在 `oven/bun:alpine` 下用 Bun 打包，二者合并进以非特权用户 `agentmem` 运行的极简 `alpine:3.21` 镜像。

1. 基于模板生成 `.env`：

   ```bash
   cp .env.example .env
   ```

   Compose 会两次消费 `.env`：既用于 `${POSTGRES_DB}` 式插值，也作为 app 的 `env_file`，因此凭据不会提交进版本库。

2. 至少设置以下项以启动可用栈：

   ```dotenv
   POSTGRES_PASSWORD=<强密码>
   AGENTMEM_MODE=production
   AGENTMEM_PG_DSN=postgres://agentmem:<POSTGRES_PASSWORD>@postgres:5432/agentmem?sslmode=require
   AGENTMEM_AUTH_ENABLED=true
   AGENTMEM_AUTH_TOKENS='[{"token_sha256":"<64 位十六进制>","subject":"console","scopes":["knowledge:read","knowledge:write"]}]'
   ```

3. 构建并启动：

   ```bash
   docker compose up -d --build
   ```

   `postgres` 服务使用 `pgvector/pgvector:pg16`，仅绑定回环地址（`127.0.0.1:5432`）并带 `pg_isready` 健康检查；`agentmem` 服务等待其健康后再以 `serve` 启动。容器内监听 `0.0.0.0:8080`，对外仅发布到 `127.0.0.1:8080`。在跨主机暴露前，请在其前方放置一个终结 TLS 的反向代理（如 Nginx、Caddy）。

4. 验证：

   ```bash
   docker compose ps
   curl -i http://127.0.0.1:8080/healthz
   go run ./cmd/agentmem validate --env .env
   ```

数据与 OKF bundle 存于命名卷（`pgdata`、`agentmem_data`）；容器内 `AGENTMEM_GIT_ROOT` 为 `/app/data/bundle`。如需初始化 bundle，可将 `testdata/bundle/` 复制进该卷（例如通过临时容器或对运行中的服务执行 `docker compose cp`）。

镜像把构建好的控制台放在 `/app/web/dist`，但 Go 二进制既不内嵌也不托管 `web/` —— API 只监听文档中列出的 9 条路径。若希望控制台与 API 同机部署，请用任意静态服务器（或反向代理）单独托管该目录。

**信任库刷新。** 2026-10 eMudhra/emSign 中间 CA 私钥泄露后，请重新拉取 `pgvector/pgvector:pg16` 并重建 agentmem 镜像以刷新随附信任库；可用 `make check-trust-store` 离线核验。

日志：`docker compose logs -f agentmem`。停止：`docker compose down`（加 `-v` 同时删除卷）。

[![][back-to-top]](#readme-top)

## 🔌 HTTP API

| 方法 | 路径 | 权限 | 用途 |
|---|---|---|---|
| GET | `/healthz` | 公开 | 各适配器、插件与存储的健康状态 |
| POST | `/v1/knowledge` | `knowledge:write` | 路由写入（`concept_id`、`document`） |
| GET | `/v1/knowledge?id=` | `knowledge:read` | 带 ACL 的权威读取 |
| POST | `/v1/query` | `knowledge:read` | 路由查询与双路合并 |
| POST | `/v1/migrate` | `migrate:run` | 增量对账（dry-run / 断点续跑） |
| GET | `/v1/decisions` | `audit:read` | 路由决策审计 |
| GET | `/v1/concepts` | `knowledge:read` | 权威概念列表 |
| POST | `/v1/computations` | `compute:run` | 执行并验证受批准计算 |
| GET | `/v1/computations?concept_id=&limit=` | `audit:read` | 计算 verdict 审计 |

除 `GET /healthz` 外每条路由都需要上表中的权限，同一矩阵在 [身份认证与访问控制](#-身份认证与访问控制) 处复述。`401` / `403` / `429` 与控制台里的错误码表一致；路径存在但方法不支持返回 `405`。`POST /v1/computations` 验证通过返回 `200`；attestation 失败或 runtime 未实现返回 `422`；契约/参数非法返回 `400`；执行器故障返回 `502`。失败响应一律不携带 `display_value`，错误体也不会回显 SQL、DSN 或驱动信息。

[![][back-to-top]](#readme-top)

## 🧭 智能路由

确定性规则按顺序求值，首个命中即短路：

| 规则 | 触发条件 | 结果 |
|---|---|---|
| `R1` 权威写入 | 写入 + authoritative | 仅主干（置信度 1.0） |
| `R2` 敏感写入 | 写入 + sensitive | 仅主干（0.95） |
| `R3` 定义查询 | 读取 + definition | 仅主干（1.0） |
| `R4` 主干更新 | 写入 + 已存在权威副本 | 主干 + 首个非主干通道（0.9） |
| `R5` 大文档 | 写入 + 正文 ≥ 4096 字节 | 主干 + 所有非主干通道（0.85） |
| `R6` 探索查询 | 读取 + exploratory | 非主干通道，主干兜底（0.8） |
| `R7` 聚合双读 | 读取 + aggregate | 主干 + 非主干通道（0.75） |

仅模糊样本进入 **AFM-D System One** 决策服务。客户端在 `AGENTMEM_SYSTEMONE_ENDPOINT` 后追加 `/v1/systemone` —— 该路径是出向调用，不是本服务的路由。其配置为 5000 ms 超时、256 条决策缓存、`min_confidence` 低分保护、`max_choices` 候选上限，以及失败关闭回落到主干。失败关闭是结构性的：在构造时硬编码，无法通过配置关闭。

路由为**读写双程**：写入决定投影，读取决定查询通道，迁移负责存量知识双向对账。详见 [docs/routing.md](./docs/routing.md)。

[![][back-to-top]](#readme-top)

## 🧩 引擎插件

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

每个插件持有严格校验的 `plugin.yaml`，以声明式 `sdk_boundary` 描述 sidecar、`agentmem-bridge/v1` 线协议、端点、能力映射与凭据引用，并实现 `Adapter` + `Lifecycle`、健康检查与版本化协议指纹。真实 SDK 连接位于该边界之后（由 sidecar 实现线协议），而不是写在代码注释里。刻意不使用 Go `.so` 动态插件，注册表为显式配置驱动；`endpoint` 留空即选择确定性离线 mock，`make check-boundaries` 保证边界始终结构化。泛型 `http` 与 `cli` kind 仍可用于自定义引擎。详见 [docs/plugins.md](./docs/plugins.md) 与 [docs/adapters.md](./docs/adapters.md)。

[![][back-to-top]](#readme-top)

## 🧮 受批准计算

`POST /v1/computations` 与 `agentmem compute` 从 OKF+Git 加载受批准计算，只绑定声明参数值，经 `postgres` runtime 执行；只有确定性 Go attester 校验 SQL provenance 与展示值 fidelity 通过后才返回数值。`bigquery` / `dbt` / `python` / `looker` 为正式契约骨架并返回 `Unsupported` —— 目前只有 `postgres` 有执行器。成功、拒绝与错误都写入 `computation_runs` 审计（离线为 `data/meta.fake.json`）。

```bash
go run ./cmd/agentmem compute --id computations/revenue-postgres --param year=2026
go run ./cmd/agentmem computations --limit 10
```

`testdata/bundle/computations/revenue-postgres.md` 是可运行示例；详见 [docs/computations.md](./docs/computations.md)。

[![][back-to-top]](#readme-top)

## 📊 评测

```bash
go run ./cmd/agentmem eval --dataset testdata/eval.jsonl --k 5
```

输出 Recall@k、Precision@k、nDCG@k、MRR、MAP、HitRate@k，全部为纯函数、无 LLM 裁判。两套口径（覆盖率 Recall 与二值 HitRate）的区别见 [docs/architecture.md](./docs/architecture.md)。

[![][back-to-top]](#readme-top)

## 🌐 Web 控制台

`web/` 是零运行时依赖的原生 ES module 控制台，包含两个视图：**状态**（健康状态 + 最近路由决策）与 **API 文档**（全部 9 个 HTTP method + path 组合、请求/响应样例、通用约定与错误码）。

- 13 种语言，与 agenthub 一致：`en`、`zh`、`zh-TW`、`es`、`fr`、`de`、`it`、`ru`、`ja`、`ko`、`pt`、`ar`、`hi`。优先级为 `?lang=` > 本地存储 > 浏览器语言 > 英文；缺失键回退英文，阿拉伯语自动切换 RTL。
- light / dark 双主题共用一套纯黑白灰灰阶。主题在首屏绘制前解析，未选择时跟随系统，仅显式切换才写入 `agentmem.theme`。
- `?view=docs` 可直接打开 API 文档视图。
- 方法、路径、字段名与 JSON 样例在所有语言下逐字呈现，仅说明文字翻译；端点数据与 `internal/handlers/http.go` 保持一致。
- 页面自带 `Content-Security-Policy` meta（`default-src 'none'`、仅同源资源、内联首屏主题脚本以 SHA-256 白名单放行），`web/tests/security.test.mjs` 会在摘要与脚本文本不一致时失败。
- Web 渲染质量使用 [impeccable][impeccable-link] 检查，单色不变式保持不变；`.notice` 的侧边强调边框作为已知「AI 痕迹」被刻意避免。

```bash
# 源码级检查（无需浏览器）
make web-audit

# 运行态元素级检查：先起后端，再起前端，最后扫描
go run ./cmd/agentmem serve  # 仓库根目录，监听 :8080
cd web && bun run dev        # Vite 开发服务器 :5173
bun run audit:live           # 等价：bun x impeccable detect http://localhost:5173

# 保存报告（重定向输出；检测结论默认打印到终端）
bun x impeccable detect http://localhost:5173 --json > impeccable-report.json
bun x impeccable detect http://localhost:5173 > impeccable-report.txt
bun x impeccable detect http://localhost:5173 --quiet
```

可直接用任意静态服务器托管 `web/`；有 Bun 时也可用 Vite 打包（`cd web && bun install --frozen-lockfile && bun run build`）。Go 二进制不依赖 Bun 或任何 JS 运行时。前端校验使用 Bun 内置测试运行器，离线执行且无新增依赖：`cd web && bun test`。

[![][back-to-top]](#readme-top)

## 🔐 身份认证与访问控制

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

令牌缺失或未知返回 `401`（带 `WWW-Authenticate: Bearer`）；令牌缺少权限返回 `403`；超过 `AGENTMEM_RATE_RPS` 返回 `429`。私有文档仅 owner 可读（`acl` 表显式授权优先），匿名写入一律拒绝，因 ACL 被拒的读取与文档不存在同样返回 `404`，无法探测私有文档是否存在。

令牌是静态的：轮换需修改 `.env` 并重启。`AGENTMEM_MODE=production` 时，缺少鉴权、没有令牌、DSN 含 `sslmode=disable`，或存在出向端点却没有 `AGENTMEM_EGRESS_ALLOWED_HOSTS`，都会拒绝启动。身份只在 Handler 边界确立一次：请求体或查询串里的 subject 一律忽略、从不采信。威胁模型见 [docs/security.md](./docs/security.md)。

[![][back-to-top]](#readme-top)

## 🔏 信任库安全

2026-10 的 eMudhra（emSign）中间 CA 私钥泄露事件由两层兜底：Go 1.27.3 内置 `crypto/x509` 屏蔽列表，运行时镜像在构建时刷新 Alpine 的 `ca-certificates`。`make check-trust-store` 离线扫描系统或镜像 CA bundle 中已知根/中间证书的 SHA-256 指纹（退出码 `0` 干净、`1` 命中、`2` 无法读取）：

```bash
make check-trust-store
docker run --rm --entrypoint cat <image> /etc/ssl/certs/ca-certificates.crt > /tmp/image-ca.crt
scripts/check-emudhra.sh /tmp/image-ca.crt
```

[![][back-to-top]](#readme-top)

## 🔨 开发

| 目标 | 作用 |
|---|---|
| `make test` | `go test ./... -count=1` |
| `make vet` | `go vet ./...` |
| `make build` | 生成单一二进制 `bin/agentmem` |
| `make run` | 构建后执行 `serve --env .env` |
| `make validate` | 严格校验 `.env.example` |
| `make eval` | 基于 `testdata/eval.jsonl` 的排序指标 |
| `make check-boundaries` | 检查声明式 SDK 边界并跑插件测试 |
| `make check-trust-store` | 离线扫描 eMudhra/emSign CA 指纹 |
| `make web` | 安装并用 Vite 构建控制台 |
| `make web-audit` | 通过 impeccable 做源码级 web 渲染/反模式检查 |
| `make clean` | 清理 `bin/` 与 `web/dist` |

`go test ./...` 无需 Postgres、网络或凭据即可通过。设置 `AGENTMEM_TEST_PG_DSN` 可启用可选的真实 PostgreSQL 集成测试。

[![][back-to-top]](#readme-top)

## 📚 文档

完整细节文档位于 [`docs/`](./docs)，均以中文撰写：

| 文档 | 覆盖内容 |
|---|---|
| [架构](./docs/architecture.md) | 分层规则、端口与适配器、禁止上向依赖的契约 |
| [安全](./docs/security.md) | 身份、ACL、出向白名单、错误脱敏、production 失败关闭 |
| [存储](./docs/storage.md) | OKF+Git 正文与 PostgreSQL 元数据的分工、哈希、冲突显式化 |
| [OKF 兼容性](./docs/okf-conformance.md) | 宽松的 OKF v0.2 子集：只有 `type` 必填 |
| [适配器](./docs/adapters.md) | 统一的 `Adapter` 契约与能力标签 |
| [引擎插件](./docs/plugins.md) | 四个引擎作为 Go 包，声明式 `sdk_boundary` |
| [受批准计算](./docs/computations.md) | 契约、参数绑定、attester 链路、verdict 审计 |
| [路由](./docs/routing.md) | 规则优先决策、System One、双程对账 |

[![][back-to-top]](#readme-top)

## 🤝 工程约定

本仓库遵循 Harness Engineering：仓库本身即 Agent 的上下文，治理它的文档与代码一同版本化。

- [`AGENTS.md`](./AGENTS.md) —— Agent 工程上下文入口与目录索引
- [`requirements.md`](./requirements.md) —— 需求规格（功能边界 / 异常 / 验收标准），人审通过后再编码
- [`task.md`](./task.md) —— 实施清单
- [`SECURITY.md`](./SECURITY.md) —— 漏洞报告方式

[![][back-to-top]](#readme-top)

---

## 📝 许可证

MIT，详见 [LICENSE](./LICENSE)。

[![][back-to-top]](#readme-top)

<!-- 链接定义 -->

[repo-link]: https://github.com/ariacompute/agentmem
[deeptutor-link]: https://github.com/HKUDS/DeepTutor
[okf-link]: https://github.com/GoogleCloudPlatform/open-knowledge-format
[impeccable-link]: https://github.com/pbakaus/impeccable

[image-banner]: ./docs/assets/banner.svg

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
[web-console-link]: #-web-控制台

[stars-shield]: https://img.shields.io/github/stars/ariacompute/agentmem?style=flat-square&labelColor=black&color=555
[stars-link]: https://github.com/ariacompute/agentmem/stargazers
[forks-shield]: https://img.shields.io/github/forks/ariacompute/agentmem?style=flat-square&labelColor=black&color=555
[forks-link]: https://github.com/ariacompute/agentmem/network/members
[issues-shield]: https://img.shields.io/github/issues/ariacompute/agentmem?style=flat-square&labelColor=black&color=555
[issues-link]: https://github.com/ariacompute/agentmem/issues
[contributors-shield]: https://img.shields.io/github/contributors/ariacompute/agentmem?style=flat-square&labelColor=black&color=555
[contributors-link]: https://github.com/ariacompute/agentmem/graphs/contributors

[back-to-top]: https://img.shields.io/badge/-BACK_TO_TOP-151515?style=flat-square
