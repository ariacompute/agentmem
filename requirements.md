# agentmem 需求规格 v0.1.0

## 1. OKF+Git 存储知识数据

- 实现 OKF v0.2 兼容读写：YAML frontmatter、必填 `type`、`sources` / `generated` / `verified` / `status` / `stale_after` 信任族、Attested Computation 契约建模。
- 宽松 conformance：仅 `type` 非空为硬性要求；未知 `type`、未知 frontmatter 键、断链、缺 `index.md` 均不拒绝；`verified` bare mapping 视为单元素列表；未知键 round-trip 保留。
- `index.md` 渐进式披露（按受影响目录增量重生成）、`log.md` 日期分组变更史、markdown 跨链接解析（断链合法）。
- Git 为正文唯一权威：`go-git/v5` 纯 Go 后端，每次写入生成 `content_hash` 与 `commit_sha`，支持 Log / Show / Diff / 回放；批量文件合并为单次提交；原子写（临时文件 + rename）。
- 路径安全：拒绝 `..`、绝对路径、反斜杠、保留名 `index` / `log`。
- ACL 扩展字段 `visibility` / `owner` 写入 frontmatter 作为可移植副本。

## 2. PostgreSQL + GORM 存储服务端结构化数据

- 依赖 `gorm.io/gorm` + `gorm.io/driver/postgres` + `pgx/v5`，版本与 `agenthub/go.mod` 一致。
- 库内只存：概念元数据、RAG 投影状态、`content_hash` 索引、embedding `Fingerprint`、路由决策审计、Attested Computation verdict、迁移游标/断点、ACL、引擎指纹与配置版本。**不存知识正文、SQL 文本、参数值或结果值**。
- 版本化迁移 `migrations/*.sql` 由 `db.Exec` 在事务内按序应用（advisory lock + `schema_migrations`）；`003_computations.sql` 建 `computation_runs`；`AutoMigrate` 不作为 schema 来源；pgvector 迁移为尽力而为。
- GORM 仅出现在 `internal/meta`：`MetaStore` 端口签名不含 `*gorm.DB`，上层包不 import gorm；CRUD 使用模型、`clause.OnConflict` upsert 与事务；日志默认 `Warn` 且不打印 SQL 参数。
- `pgvector` 的 `vector` 类型用自定义 `Scanner/Valuer` + 原生参数化 SQL，经 `VectorStore` 窄端口暴露。
- 生产 `AGENTMEM_PG_DSN` 必填；dev/测试注入文件持久化 fake（`data/meta.fake.json`），保证 CLI 多进程共享 ACL、投影与审计状态。

## 3. 插件化第三方 RAG

- 统一 `Adapter` 端口：`Name` / `Capabilities` / `Put` / `Query` / `Delete` / `Fingerprint` / `Health`；显式注册表 + 配置驱动工厂，未知 kind 显式报错，未实现能力返回 `Unsupported`（禁止静默 no-op）。
- 内置可用实现：`okf`、`memory`（倒排 + 哈希向量）、`pgvector`（含记录调用序列的 fake）。
- 五个目标引擎：`LlamaIndex`、`PageIndex`、`GraphRAG`、`LightRAG`、`pgvector`；前四者为 `internal/plugins/<engine>/` 独立 Go 插件包，含 `plugin.yaml`（`sdk_boundary` 正式声明真实 SDK 边界）、`Adapter` + `Lifecycle`、健康检查、版本化协议指纹与注册表装配，经泛型 HTTP/CLI 线协议与 sidecar 通信。`make check-boundaries` 保证边界结构化、仓库内无内联 SDK 边界注释。
- 所有外部依赖置于接口之后，`go test ./...` 离线全绿，不依赖外部服务与密钥。

## 4. 智能路由（读写双程）

- 有序确定性规则表优先短路，输出 `rule_id` / 理由 / 置信度；仅模糊样本进入 System One。
- System One 对齐本机 `router/afm_d` 契约：`POST {endpoint}/v1/systemone` → choice + confidence；超时 5000ms、缓存 cap 256（命中 confidence 0.5、`cache:` 前缀）、候选上限 255、`min_confidence` 低分保护、fallback 必须在 eligible 池内、错误/超时 fail closed 回落到主干。
- 写入分流：主干先提交 → GORM 事务落元数据 → 幂等投影；投影失败不回滚正文，记入 failed 投影由迁移补偿。支持 projection-only 模式（仅写 RAG，元数据标记、不写 Git 正文）。
- 读取路由：OKF-only / 单 RAG / 双路并发（总延迟取 max）、OKF 优先合并、去重、引用保留、冲突显式标注、ACL 过滤。
- 存量迁移：基于 `content_hash` 增量对账、指纹漂移重建、dry-run、幂等、断点续跑（游标存 PG）、双向失效标记。
- 决策审计：`slog` 结构化输出 + PG 持久化，可查询可回放，不含正文/PII/凭据。

## 5. Attested Computation

- 受批准计算体存于 OKF+Git（bundle 内 `.sql` 资源或正文 ```sql 内联块）；Agent 只能填声明参数值，不得改写计算体。
- 参数类型 `integer/number/string/boolean/date`；`:name` 转 `$N` 参数化绑定；拒绝多语句与变更 SQL。
- 五 runtime 契约骨架：`bigquery / postgres / dbt / python / looker`；仅 `postgres` 真实执行（只读事务、参数化、超时、行数上限），其余返回 `Unsupported`。
- `executor → receipt`；确定性 Go attester 做 SQL 规范化 provenance 与展示值 fidelity 校验；失败拒绝展示 `display_value`。
- HTTP `POST /v1/computations` + `GET /v1/computations`；CLI `compute`（可重复 `--param`）与 `computations`；成功/拒绝/错误都写入 PG 审计。

## 6. `.env` 配置加载

- `.env` 为唯一配置文件；`.env.example` 是带占位值的模板，`cp .env.example .env` 后使用；`.env` 被 `.gitignore` / `.dockerignore` 排除，镜像内不含配置文件与凭据。
- `config.Load` 默认读 `.env`，`--env` 与 `AGENTMEM_ENV_FILE` 可改路径；进程环境变量覆盖文件值；文件缺失回退 `Default()` 以保证离线开箱可用。
- 自研零依赖 dotenv 解析：`KEY=VALUE`、可选 `export`、注释、单/双引号、`#` 行内注释；重复键、缺 `=`、非法键、非法数值、非法 `AGENTMEM_ADAPTERS` JSON 一律报错。
- 封闭 `AGENTMEM_*` 键白名单（等价 `KnownFields(true)`）：未知键与未实现能力显式报错；`POSTGRES_*` 等非 `AGENTMEM_*` 键留给 Compose。`AGENTMEM_ADAPTERS` 为 JSON 数组，仅 `enabled: true` 条目装配。
- `docker compose` 同时用 `.env` 做 `${...}` 插值与 `env_file`，版本化文件中不出现密码或 DSN。

## 7. 交付与验收

- CLI：`serve` / `put` / `query` / `migrate` / `decisions` / `concepts` / `compute` / `computations` / `validate` / `eval`；命令用 `--env` 指定配置文件。
- HTTP API：`/healthz`、`/v1/knowledge`、`/v1/query`、`/v1/migrate`、`/v1/decisions`、`/v1/concepts`、`/v1/computations`。
- 评测：Recall@k / Precision@k / nDCG / MRR / MAP / HitRate@k 纯函数；显式区分覆盖率 Recall 与二值 HitRate。
- 文档、CI、Docker、compose；`web/` 零运行时依赖原生 ES module 控制台（详见第 8 节）。
- 验收线：`go test ./...`、`go vet ./...`、`go build ./cmd/agentmem`、`make check-boundaries`、`cd web && npm test`、`cd web && npm run build` 全绿，离线无外部服务。

## 8. Web 控制台

- 零运行时依赖的原生 ES module 单页（HTML + JS + 独立 `web/src/style.css`），可由任意静态服务器直接托管；Vite 6 仅作可选打包与开发代理；不引入框架、路由、CDN 或外部资源，Go 二进制不提供静态资源。
- 两个视图：状态（`/healthz` 健康字段 + `/v1/decisions?limit=20` 最近决策，含刷新、加载与错误提示）与 API 文档；`?view=docs` 可直达文档视图。
- 13 种语言与 agenthub 一致：`en / zh / zh-TW / es / fr / de / it / ru / ja / ko / pt / ar / hi`。优先级 `?lang=` > localStorage(`agentmem.lang`) > 浏览器语言 > 英文；英文浅合并兜底，阿拉伯语 RTL 镜像 `<html lang|dir>`；界面文案与文档正文在 13 语言下均有各自译文。
- light / dark 双主题共用纯黑白灰灰阶，不含任何色相；`index.html` 首屏脚本写 `data-theme`，未选择时跟随系统，仅显式切换持久化到 `agentmem.theme`；状态差异由亮度、描边、字重、形状与克制动效表达。
- API 文档覆盖 `internal/handlers/http.go` 的 9 个 method+path 组合，含章节索引、分组端点表、可展开的请求/响应 JSON 样例，以及 Base URL、Content-Type、4 MiB 上限、`{"error":...}` 错误体、405/422/502 状态码、`limit` 与 ACL、无鉴权/CORS 等通用约定；出站 sidecar 线协议不属于对外 API。方法、路径、字段名、状态码与 JSON 样例在所有语言下逐字呈现。
- Node 内置 `node:test` 套件（零新增依赖）：语言目录、译文完整性与英文兜底、端点表与后端路由一致性、单色守卫、主题解析/持久化、DOM 冒烟（两视图切换、示例展开、RTL、主题切换）；CI 的 web job 执行 `npm test` 与 `npm run build`。
- 页面不输出日志、不记录 API 响应正文；示例仅用占位值，不含正文、PII 或凭据。

## 9. 非目标

完整管理台；Go 二进制内直接链接四个 Python 引擎的原生 SDK（由 sidecar 承担 `sdk_boundary`）；非 `postgres` 计算执行器；自动 commit/push 源码仓库；跨组织知识同步协议；前端框架/组件库、CDN 资源与交互式 Try it。
