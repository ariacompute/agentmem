# agentmem 实施任务与验收清单

## 阶段 1 · 骨架与 OKF+Git 内核

- [x] `go.mod` / `Makefile` / `.env.example`；`.env` 单一配置源（`--env` / `AGENTMEM_ENV_FILE`、进程环境覆盖、封闭 `AGENTMEM_*` 键白名单、生产 DSN 必填），Docker Compose 用 `env_file` 与 `${...}` 插值读取同一文件。
- [x] `internal/okf`：frontmatter 解析与 round-trip、文档模型、信任派生、Attested 契约、Bundle 读写、渐进索引、变更日志、路径安全。
- [x] `internal/vcs`：Repository 端口、go-git 实现、内存 fake、Log/Show/Diff。
- [x] `internal/meta`：MetaStore 端口、GORM 模型、PostgresStore、版本化迁移、内存 fake、VectorStore（含 `Scanner/Valuer`）。
- [x] `internal/store`：OKFStore 正文+元数据联动、幂等写入、ACL 默认值、投影状态与指纹。

## 阶段 2 · 适配器注册表

- [x] `Adapter` / `Embedder` 端口与 `Capabilities`。
- [x] 显式注册表、配置驱动工厂、未知 kind 显式报错。
- [x] 内置 `okf` / `memory` / `pgvector`，记录调用序列的向量 fake。
- [x] 统一契约测试套件覆盖所有实现。

## 阶段 3 · 第三方 RAG 桥接

- [x] 通用 HTTP 桥接（超时、重试上限、健康检查、JSON 线协议；SDK 边界由插件清单 `sdk_boundary` 正式声明）。
- [x] 通用 CLI 桥接（无 shell、参数白名单、stdin/stdout JSON）。
- [x] 五引擎 YAML 声明与离线确定性 mock；`RegisterKinds` 幂等注册。

## 阶段 4 · 智能写入路由

- [x] 特征提取（仅特征与摘要，不发正文）。
- [x] 有序规则表 `R1`–`R7` 与确定性短路。
- [x] AFM-D System One 客户端（缓存、低分保护、fail closed）与离线 stub。
- [x] 写入分流：主干优先、元数据事务、幂等投影、失败不回滚、projection-only。
- [x] 决策审计（slog + PG）。

## 阶段 5 · 读取合并与迁移

- [x] 双路并发查询、OKF 优先合并、冲突标注、ACL 过滤、超时可见。
- [x] 基于 `content_hash` 的双向增量迁移、dry-run、断点续跑、指纹漂移重建、孤儿失效。

## 阶段 6 · 交付面

- [x] `internal/service` 装配、`internal/handlers` HTTP API、端到端 httptest。
- [x] `cmd/agentmem` 入口与 CLI 命令。
- [x] `internal/eval` 纯函数指标与路由准确率。
- [x] 文档、CI、Docker、compose、最小 Vite 页面、示例 bundle 与评测数据集（Web 控制台后续升级见阶段 9）。

## 阶段 7 · 四引擎 Go 插件化

- [x] `internal/plugins/manifest` 严格清单解析、`sdk_boundary` 正式化 SDK 边界、协议指纹。
- [x] `internal/plugins/<engine>/plugin.yaml` 与 `Adapter` + `Lifecycle` 实现（四份）。
- [x] `pluginbase` 组合泛型 HTTP/CLI 桥接、引擎级引用、离线 mock 可见性、fail-closed 启动。
- [x] `adapter.Registry.StartAll/Close`、config kind 白名单、`service.Assemble` 显式注册装配。
- [x] 仓库内旧版 SDK 边界注释清零；`make check-boundaries` 与 CI 防回潮；`docs/plugins.md`。

## 阶段 8 · Attested Computation

- [x] `internal/compute`：契约加载、参数绑定、SQL 规范化、receipt、确定性 attester、五 runtime 骨架。
- [x] `meta.SQLExecutor` 端口与 `internal/meta/postgres_exec.go` 只读 GORM 执行；离线 fake。
- [x] `computation_runs` 审计：`003_computations.sql`、`MetaStore` 扩展、快照往返、幂等。
- [x] HTTP `/v1/computations`、CLI `compute` / `computations`、`docs/computations.md`。

## 阶段 9 · Web 控制台本地化与 API 文档

- [x] `web/src/lib/languages.js` + `i18n.js`：13 语言目录、`?lang=` > localStorage > 浏览器语言 > 英文、英文浅合并兜底、`<html lang|dir>` 与 RTL。
- [x] `web/src/lib/copy/shell-copy.js` / `api-copy.js`：界面文案与文档正文 13 语言完整译文。
- [x] `web/src/lib/theme.js` + `web/src/style.css` + `index.html` 首屏脚本：纯黑白灰双主题、跟随系统、显式选择持久化、`prefers-reduced-motion`。
- [x] 外壳重构：`web/src/main.js` 顶栏 / 导航 / 语言与主题切换器 / 页脚，`views/status.js` 状态视图、`views/docs.js` 文档视图。
- [x] `web/src/lib/api-content.js`：9 个 method+path 组合、JSON 样例、通用约定与状态码，严格镜像 `internal/handlers/http.go`。
- [x] `web/tests/` 零依赖 `node:test` 套件（语言、译文兜底、端点漂移、单色守卫、主题、DOM 冒烟；CSP 守卫见阶段 11）；`web/package.json` 的 `bun test` 与 CI web job 接入。
- [x] `README.md` / `README_cn.md` Web 章节同步。

## 阶段 10 · eMudhra（emSign）信任库响应

- [x] `scripts/check-emudhra.sh`：离线扫描 CA bundle 的 2 个根 + 8 个中间证书 SHA-256 指纹，退出码 `0` 干净 / `1` 命中 / `2` 无法读取，零网络调用（与 `mcpx` 同一指纹表）。
- [x] `make check-trust-store` 目标并入 `.PHONY` 与 CI；`SECURITY.md` 记录镜像 CA 导出后在宿主扫描的口径，不把 `openssl` 装进运行时镜像。
- [x] `Dockerfile` / `docker-compose.yml`：构建与运行阶段刷新 `ca-certificates`，运行时升到 `alpine:3.21` 非 root 用户。

## 阶段 11 · 全面安全加固

- [x] H1 `internal/auth` + `handlers/security.go`：Bearer → 摘要常量时间比较 → `Principal` 注入 `context`；9 条路由的权限矩阵集中在 `routePermissions`。
- [x] H2 主体只由中间件产生；`/v1/knowledge`、`/v1/query`、`/v1/computations` 移除请求体 `subject`，严格 JSON 解码拒绝该字段。
- [x] H3 `store.TrustedACL` / `AuthorizeUpdate` / `aclFromVisibility`：私有文档按 owner 强隔离，匿名写入拒绝，读无权限与不存在统一 `404`。
- [x] H4 错误脱敏：客户端只见分类文案，日志只记 `class` + `%T`；移除全部 `err.Error()` 回显（含 `/healthz` 与计算审计）。
- [x] H5 配置闭白名单覆盖进程环境 `AGENTMEM_*`；生产强制鉴权 + 令牌 + 拒绝 `sslmode=disable` + 出向端点必配白名单；删除死开关 `AGENTMEM_SYSTEMONE_FAIL_CLOSED`。
- [x] H6 `internal/netsafe`：装配期端点校验 + `RedirectPolicy` 逐跳复检 + 禁止 TLS 降级；System One 与 HTTP 桥接同时接入。
- [x] H7 `internal/safepath`：`okf` 全部读写删与 `vcs` 提交路径统一校验，符号链接逃逸（文件或父目录）拒绝；原子写改 `CreateTemp` + `0o600` + `fsync` + `rename`。
- [x] H8 计算面：危险函数黑名单扩充（文件/网络/会话/大对象），块注释内占位符不绑定，整轮共用超时 context，审计失败不回显。
- [x] H9 CLI 桥接：裸可执行程序名白名单、UTF-8 参数放行、stderr 不进错误；`engines.yaml` 改严格解析。
- [x] H10 服务超时与优雅退出（读头 5s / 读 15s / 写 30s / 空闲 60s / 头 1 MiB）；compose 端口改 `127.0.0.1` 回环，容器内仍 `0.0.0.0`。
- [x] H11 CI/release 最小 `permissions`、`persist-credentials: false`、`image` job 使用 `ARIACOMPUTE_TOKEN`（PAT，仅 `write:packages`）、`go mod verify`、dependabot。
- [x] M1 每客户端令牌桶限流（默认 20 rps / burst 40，桶表上限 10000）+ 认证失败日志按秒节流。
- [x] M2 `AGENTMEM_ALLOWED_ORIGINS` 精确匹配（同源直接放行，不返回 CORS 头）。
- [x] M3 安全响应头：`nosniff`、`DENY`、`no-referrer`、`no-store`、API 侧 CSP。
- [x] M4 严格 JSON（`Content-Type` 校验、4 MiB、`DisallowUnknownFields`、拒绝尾随数据）与参数边界（`top_k` ≤ 100、`limit` 1–1000 默认 20）。
- [x] M5 OKF 解析资源上限（文档 8 MiB、frontmatter 1 MiB、YAML 节点 10000/深度 32/别名 32、条目 100000、资源 1 MiB），不加 `KnownFields(true)`。
- [x] M6 规范化缓存键长度上限；`audit:read` 覆盖 `/v1/decisions` 与 `/v1/computations`。
- [x] M7 Web：`api-content.js` 同步 Bearer/scopes/限流行与 401/403/429 状态，13 语言补真实译文，移除请求样例的 `subject`，新增 CSP meta（inline 主题脚本 SHA-256 白名单）与 `web/tests/security.test.mjs`。
- [x] L1 `docs/security.md`、`SECURITY.md`、`AGENTS.md`、`README*`、`requirements.md`、`releasenotes*` 同步留痕。

## 阶段 12 · Web 渲染检查（impeccable）

- [x] `web/package.json`：新增 `audit`（源码级 `impeccable detect src index.html`）与 `audit:live`（运行态 URL `http://localhost:5173`）脚本。
- [x] `web/.impeccable/config.json`：共享配置（关闭遥测）；`.gitignore` 排除临时缓存、保留该配置。
- [x] `Makefile`：新增 `web-audit` 目标（`cd web && bun x impeccable@latest detect src index.html`）并并入 `.PHONY`。
- [x] `web/src/style.css`：移除 `.notice` 的 3px 侧边强调边框（impeccable 标记的「AI 痕迹」），改为细实线边框 + 极淡底色；复跑 `impeccable detect` 收敛至 0 反模式。
- [x] `web/src/style.css`：提升三级文字 `--text-3` 透明度（暗色 `0.44 → 0.55`、亮色 `0.52 → 0.6`），使三级文字在 `#050505` / `#f5f5f5` 上满足 WCAG AA 对比度。
- [x] `AGENTS.md` / `README.md` / `README_cn.md` / `requirements.md`：补充 impeccable 渲染检查工作流与 `make web-audit` 说明。

## 阶段 13 · 门禁闭环与计算面扩展

- [x] `ci.yml`：`go` job 末尾新增离线黑盒验收 `make e2e E2E_ARGS='--no-gates'`；`web` job 新增 `bun run audit`，`bun-version` 对齐 `1.4.4`。
- [x] `web/package.json` 精确固定 `impeccable: 4.9.1` 并刷新 `web/bun.lock`；`Makefile` 的 `web-audit` 改为 `bun run audit`，不再使用 `@latest`（阶段 12 的 `@latest` 由此取代）。
- [x] `SECURITY.md` 新增「CLI trust boundary」小节（自声明主体、本地可信运维面、存储层仍拒绝匿名写与 owner 不匹配、缓解建议）；`docs/security.md` 信任边界与威胁模型同步。
- [x] 计算面：`AGENTMEM_COMPUTE_SIDECARS` 声明 `bigquery` / `dbt` / `python` / `looker` 的 `http` 或 `cli` sidecar；`internal/compute/sidecar.go` 实现线协议 v1，`body_digest` 由 Go 侧重算，未配置 runtime 保持 `ErrUnsupported`。
- [x] 正文泛化：`body_kind`（`sql` / `script` / `model` / `query`）驱动围栏语言、资源后缀、`SafeBody` 与 `CanonicalBody`；SQL 仍保留只读 SELECT/WITH 与危险函数黑名单。
- [x] 出向复用：`internal/bridges/outbound.go` 的 `PostJSON`（白名单、逐跳复检、1 MiB 请求 / 4 MiB 响应、5xx 重试 1 次、错误仅留类型），装配期与调用前双重校验端点。
- [x] 测试：`internal/compute/sidecar*_test.go`（HTTP/CLI、digest 不符、非标量单元格、行截断、命令校验）、`internal/bridges/outbound_test.go`、`internal/config/sidecar_test.go`；`scripts/e2e.sh` 新增 C29–C32（CLI sidecar、HTTP sidecar、失败不泄漏正文、未配置仍 fail-closed）。
- [x] 文档：新增 `docs/compute-sidecar.md`；`requirements.md`、`releasenotes*`、`README*`、`docs/computations.md`、`docs/okf-conformance.md`、`docs/architecture.md`、`docs/e2e.md`、`.env.example`、`AGENTS.md` 同步。

## 阶段 14 · 控制台内嵌与 try-it

- [x] `internal/webui`：`//go:embed all:dist` 把 Vite 产物编入二进制；`serve` 在**同一 listener** 上应答 `GET /`（含 `/index.html`）与 `/assets/*`，首页 `no-cache`、带哈希资产 `immutable`，只服务已 clean 的路径（`..`、`//`、尾斜杠一律拒绝而非规范化），非 GET/HEAD 返回 405，未注册路径返回 JSON 404。
- [x] 启动期 CSP：从内嵌 `index.html` 现算唯一内联主题脚本的 SHA-256 并组装 `Content-Security-Policy` 响应头，Go 头与页面永不失配；`web/index.html` 的 meta 降级为纯静态托管兜底。
- [x] 控制台公开白名单：`handlers.Console.Public` 只放行 `/`、`/index.html`、`/assets/*`；`/v1/*` 仍需 Bearer 令牌，未注册 `/v1` 路径继续返回 JSON 404，控制台不会接管 API 前缀。
- [x] try-it 工作台：`web/src/lib/api-try.js` + `web/src/views/try-it.js` 提供同源真实请求、可编辑查询参数与 JSON 正文、`Ctrl/Cmd+Enter` 发送、状态码 / 耗时 / 响应头 / 美化正文、写入二次确认；令牌只存 `sessionStorage` 且只走 `Authorization` 头，响应一律以 `textContent` + `createElement` 渲染（`web/src/` 零 `innerHTML` / `outerHTML` / `insertAdjacentHTML` / `document.write`）。
- [x] 产物新鲜度门禁：产物目录随仓库入库，`go build` / `go test ./...` 无需 Bun；CI 用 `git diff --exit-code -- internal/webui/dist` 阻止陈旧产物，本地以 `make web-verify` 复现。
- [x] 黑盒验收：`scripts/e2e.sh` 新增 `H31`（二进制托管首页 200）、`H36`（哈希资产长缓存）、`H37` / `H38`（未注册 API 与静态路径仍 JSON 404）、`H39`（写方法 405）、`S21`（鉴权开启时控制台仍公开）与 `W06`（文档页提供 try-it）。
- [x] 文档：`AGENTS.md`、`README.md`、`README_cn.md`、`docs/security.md`、`docs/architecture.md`、`docs/e2e.md` 同步内嵌控制台、公开白名单与 try-it 边界。

## 阶段 15 · 规格一致性与持续覆盖补强

- [x] 规格同步：`requirements.md` 升为 v0.2.0；§7 补控制台静态路由，§8 首条改为二进制内嵌托管并新增控制台开关与 try-it 条目，§10 删除「非 `postgres` 计算执行器」与「交互式 Try it」两条已被实现打破的非目标，并把「完整管理台」收窄为写入 / RBAC 管理与多租户运维面板。`task.md` 补记阶段 14。
- [x] 控制台开关：新增 `AGENTMEM_CONSOLE_ENABLED`（默认 `true`，进闭白名单与 `.env.example`）；`cmdServe` 仅在该值为真时装配 `webui.New`，关闭时 `handlers.Options.Console` 保持真正的 nil（避免 typed-nil），`GET /`、`/assets/*` 与未知静态路径统一返回 JSON 404，`/healthz` 与 9 条 API 路由不变。
- [x] 真实 Postgres 持续覆盖：`postgres-integration` 由纯 `workflow_dispatch` 改为每日 `schedule` 定时触发并保留手动派发，让 GORM + 迁移 + `computation_runs` + 向量 `Scanner/Valuer` 这条唯一无持续验证的生产存储路径每天被真实 pgvector 验证一次。
- [x] root 环境测试稳定：`internal/okf`、`internal/vcs`、`internal/store`、`internal/meta`、`internal/adapter` 五个包内依赖 `os.Chmod` 制造拒绝场景的断言在 `os.Geteuid() == 0` 时跳过（root 持有 `CAP_DAC_OVERRIDE`，权限检查被绕过），非 root 覆盖不变。
- [x] sidecar 示例：`examples/compute-sidecars/sidecar.py` 单文件参考实现，`stdio` 对应 `bridge: cli`、`http` 对应 `bridge: http`，按 `runtime` 分派 `bigquery` / `dbt` / `looker` / `python`，`AGENTMEM_SIDECAR_OFFLINE=1` 返回确定性表，任何分支都只回 `columns` / `rows` / `body_digest`；`examples/compute-sidecars/README.md` 说明配置、凭据最小权限与上线检查清单。
- [x] sidecar 契约测试：`internal/compute/sidecar_contract_test.go` 表驱动覆盖四个出向 runtime 的 `runtime` / `body_kind` / 参数透传、digest 正确与不符、未知字段拒绝、`postgres` 不得声明为 sidecar；`scripts/e2e.sh` 新增 `C33`–`C35`（bigquery / dbt / looker 经示例 sidecar 真实执行）与 `H40`（控制台关闭后的对外表现）。
- [x] 文档同步：`.env.example`、`AGENTS.md`、`docs/security.md`、`docs/compute-sidecar.md`、`docs/e2e.md`、`README.md`、`README_cn.md` 记录开关、定时门禁与 sidecar 示例；`releasenotes*` 属历史发布记录，不改写。

## 阶段 16 · 知识图谱与本体（graph）

目标：概念既是文档也是节点，关系在 frontmatter 声明，服务端只保留可重建的邻接索引，并把图读写、类型化校验、融合检索与可回答性暴露到 HTTP / CLI / MCP / 控制台。

- [x] OKF 契约：`internal/okf/graph.go` 解析与写出 `graph`（节点 + 边）与 `graph_schema`，`Marshal` 保持键序与未知键 round-trip；预算（64 边 / 32 属性键 / 256 字节值 / 1024 字节证据 / 128 字节类型名）在 `SetGraph` 处拒绝；`okf` 侧不 import `meta`，声明类型以 `okf.GraphEdge` / `okf.GraphSchema` 自带。
- [x] 端口与投影：`internal/meta/graph.go` 定义 `GraphIndex`（Upsert/Delete/Get/Neighbors/ShortestPath/Search/Stats/Stale/Prune），签名不含 `*gorm.DB`；`004_graph.sql` 幂等建 `graph_nodes` / `graph_edges` 与索引；`postgres_graph.go` 用递归 CTE 做邻居与最短路径（深度、行数在 SQL 侧封顶），`fake_graph.go` 与之同语义同排序，`file_fake.go` 快照纳入图行，CLI 多进程可见。
- [x] 图核心：`internal/graph` 拆成 `graph.go`（视图与端口）、`decl.go`（派生边 + 本体校验：已声明关系类型连错节点类型 `422`，未知类型/未知节点/断链只告警）、`index.go`（`Sync` / `EnsureFresh` / `Rebuild` + `PruneMissing`，按 `content_hash` 跳过未变更文档）、`query.go`（节点/邻居/路径/检索，ACL 先过滤再计数）、`answer.go`（分词、覆盖率 0.6/0.15/0.15/0.1 加权、三档结论、七类缺口、上限 50）、`fusion.go`（RRF + id 兜底排序）、`writer.go`（写入后同步投影，失败只告警不回滚正文）、`remote.go`（HTTP 只读引擎：netsafe 主机白名单、令牌取自环境变量名、超时与 5xx 红化、禁止静默降级）。
- [x] 配置与权限：`AGENTMEM_GRAPH_ENABLED/BACKEND/REMOTE_URL/REMOTE_TOKEN_ENV/REMOTE_TIMEOUT_MS/MAX_DEPTH/MAX_NODES/MAX_EDGES/COVERAGE_MIN/RRF_K` 进入 `config.go`、`dotenv.go` 白名单与 `Default()`，非法值/枚举/联动（remote 缺 URL、缺令牌、主机未放行）启动即失败；`auth.PermGraphWrite` 与 `knownPermissions` 同步；`service.KnowledgeService` 装配 `Graph*` 用例方法，写后刷新 schema 缓存并同步索引。
- [x] HTTP 与 MCP：`/v1/graph`（`node` / `neighbors` / `path` / `search` / `schema` / `answerability` / `retrieve` / `index` / `edges`）与 `POST /mcp` 共 12 条新 method+path，全部进 `routePermissions`；`depth`/`limit` 双端夹紧，私有与不存在统一 `404`，`/mcp` 仅 5 个只读工具、通知 `202`、超限 `413`；`AGENTMEM_GRAPH_ENABLED=false` 时统一 JSON `404`。
- [x] CLI：`graph stats|node|neighbors|path|search|answerable|retrieve|put|edge|reindex`，`root.go` 分发与 usage 同步；写命令需要显式 `--subject`（与 `put` 一致），读命令沿用同一 ACL。
- [x] 控制台：`?view=graph` 第三视图（索引新鲜度徽标、检索与类型过滤、粘性详情、出/入边表格、深度与方向有界的邻域、原生 `<progress>` 覆盖率与缺口清单、需二次确认的节点写入）；`api-graph.js` 收敛请求构造（令牌只走 `Authorization`）；API 文档为 12 条图/MCP 端点各配独立描述（新增 8 个文案键 + 改写的节点描述，覆盖 13 语言），端点表扩到 21 条并补 `202`/`413` 状态说明，新增「图与 MCP 能力开关」约定区块（scope、`graph:write`、关闭即 404、深度上限、`/mcp` 工具清单），`/v1/graph/schema` 响应统一为 snake_case 并加测试钉住；表格改为查询容器（<560px 逐格注入列名并堆叠，表头粘住），行链接支持上下键移动焦点，动效在 `prefers-reduced-motion` 下禁用；`make web-audit` 0 命中。
- [x] 文档：新增 `docs/graph.md`（声明契约、校验语义、投影与陈旧判定、表结构、API/CLI/MCP、RRF 与覆盖率、外部引擎、控制台、与 omniGraph / auto-ontology 的差异与非目标），同步 `architecture.md`（分层与图数据流、21 条路由）、`storage.md`（两张新表与 fake 语义）、`security.md`（12 条路由权限、出向白名单、生产 fail-closed、两张威胁表）、`requirements.md`（第 10 节）、中英 README、发布说明与 `.env.example`。
- [x] 验收：`go test ./...`（离线）、`go vet`、`go build`、`make check-boundaries`、`make web-audit`、`cd web && bun test`（75 用例 / 9 文件）、`cd web && bun run build`、`make e2e`（159 用例，含 C36–C42 CLI 图用例与 H40–H56 HTTP/MCP 图用例）全绿。

## 阶段 17 · 黑盒验收补齐远程引擎、类型化本体与图开关

目标：把 `docs/graph.md` 的契约放进用户视角黑盒验收，而不是只靠包内测试；同时修掉验收中发现的一处端点示例错误。

- [x] 远程引擎（`E01`–`E09`）：本地 `python3` 回环桩实现 §10 的只读契约（node / schema / stats / search / neighbors / path / answerability）。读取确实被转发（stats 返回桩的 `7`，不是本地索引计数）；引擎日志只记方法、路径、id、`auth=ok|missing`、转发的 subject 与 `qsubject=yes|no`，从不记令牌值，因此“身份只走 `X-Agentmem-Subject`”是被观测到的。写节点与重建返回 `422 graph request rejected`，引擎日志既无 `PUT` 也无 `/v1/graph/index`，正文无 `content_hash`，本地 `GET /v1/knowledge` 仍 `404`。启动侧用 `timeout` 证明无令牌、主机未进白名单两种配置在监听前退出，stderr 保留 `graph backend` 类别。
- [x] 类型化本体（`T01`–`T06`）：种子 `ontology-schema` 后 `GET /v1/graph/schema` 返回 snake_case 的 `from`/`to` 且无告警；`computed_from` 允许的 metric→table 提交成功并可读回；table→metric 被 `422` 拒绝、不写正文、拒绝后 `out` 仍为 `[]`；`POST /v1/graph/edges` 的越界追加被拒绝且既有声明不变；CLI `graph edge` 报同一句 `not allowed from node type`。
- [x] 图开关与配置边界（`T06`、`F20`–`F26`）：`AGENTMEM_GRAPH_ENABLED=false` 时 `/v1/graph`、`/v1/graph/node`、`/mcp` 统一 JSON `404`，`/healthz`、`/v1/concepts` 不变；`validate` 覆盖 remote 缺 URL、令牌变量名非法、未知后端、深度/边预算越界，以及禁用图时不要求远程端点但仍校验边界。
- [x] MCP（`H57`–`H60`）：`tools/call` 的 `search_terms` / `get_node` 返回 `content` + `structuredContent`，未知工具返回 `-32602` 且无 `result`，超 4 MiB 请求体返回 `413`。
- [x] 文档修正：`docs/graph.md §10` 的 `AGENTMEM_GRAPH_REMOTE_URL` 示例去掉多余的 `/v1`（客户端自身补 `/v1/graph/*`），并写明端点只写基址；`docs/e2e.md` 同步依赖（`timeout`）、七个档位、用例矩阵与新计数；中英 README 的验收条目补上本体/远程引擎。
- [x] 验收：`make e2e` 185 条全绿（`PASS=185 FAIL=0 SKIP=0`，新增 26 条：`F20`–`F26`、`T01`–`T06`、`E01`–`E09`、`H57`–`H60`）；`make e2e E2E_ARGS='--no-gates'` 为 179 条并记一条 `SKIP G00`。

## 阶段 18 · MCP server（Streamable HTTP + stdio）

目标：把只读 `/mcp` 片段升级为完整 MCP server，一份协议内核同时经 HTTP 与 stdio 暴露，工具面覆盖全部读路径并在显式开关下提供写工具。

- [x] `internal/mcp` 协议内核：JSON-RPC 2.0（单条 + batch ≤ 32）、版本协商（`2025-06-18` / `2025-03-26` / `2024-11-05` 回退）、固定错误码表、会话表（TTL + 容量 + 惰性清扫）、工具注册表与 `Backend` 端口；不 import `service` / `handlers` / `cli`。
- [x] 工具面：13 个只读（图读 8 个 + `query`、`list_concepts`、`read_knowledge`、审计 2 个）常驻；5 个写工具（`put_knowledge`、`put_graph_node`、`append_graph_edge`、`run_computation`、`rebuild_graph_index`）仅在 `AGENTMEM_MCP_WRITE=true` 时登记，调用前逐工具校验 scope，隐藏时按 `-32602 unknown tool` 处理。
- [x] Streamable HTTP（`internal/handlers/mcp_http.go`）：`initialize` 下发 `Mcp-Session-Id`、`MCP-Protocol-Version` 校验、`Accept: text/event-stream` 的 SSE 响应、`GET /mcp` 事件流（15s 心跳、5 分钟上限、`ResponseController` 只解除单连接写超时）、`DELETE /mcp` 关会话；会话缺失/未知 `404` + `-32004`。
- [x] stdio（`internal/cli/mcp.go`）：`agentmem mcp` 逐行 JSON-RPC，`stdout` 只写协议帧、日志走 `stderr`、EOF 退出 0、单行超限回 `-32700` 后继续；身份优先 `--token` / `--token-env` / `AGENTMEM_MCP_TOKEN`，仅在鉴权关闭时回退 `--subject` + `--scopes`，否则拒绝启动。
- [x] 配置：`AGENTMEM_MCP_WRITE`（默认 `false`）、`AGENTMEM_MCP_SESSION_TTL_MS`（60s–24h）、`AGENTMEM_MCP_MAX_SESSIONS`（1–4096）进入封闭白名单与 `Validate()`；`.env.example` 同步；`routePermissions` 增 `GET`/`DELETE /mcp`（均 `knowledge:read`）。
- [x] 测试：新增 `internal/mcp`（协议/工具/会话）、`handlers`（会话、SSE、事件流、版本、写开关）、`cli`（身份解析、stdio 循环、超长行）、`config` 与 `auth` 用例；e2e 新增 `F27`/`F28`、`C43`/`C44`、`H61`–`H67`、`S22`–`S24`，`H52`/`H57`/`W03` 按新工具面与 23 条路由改写。
- [x] 文档：新增 `docs/mcp.md`；`AGENTS.md` 把「`/mcp` 无写工具」改为「默认关闭 + 逐工具 scope」并补 `internal/mcp` 层位；`requirements.md`、`docs/security.md`、`docs/architecture.md`、`docs/graph.md`、`docs/e2e.md`、中英 README、三份发布说明与控制台 API 文档（23 条组合 + 13 语言新增文案）同步。
- [x] 验收：`go test ./...`、`go vet ./...`、`go build ./...`、`make check-boundaries`、`make web-audit`、`bun test`、`make e2e` 全绿；`make e2e` 199 条（`PASS=199 FAIL=0 SKIP=0`）。

## 阶段 19 · 覆盖率门禁与补齐

目标：把「每包语句覆盖率 100%」变成可执行门禁，并补齐 9 个包共 268 条未覆盖语句。

- [x] 门禁：新增 `scripts/coverage`（profile 解析、按包聚合、缺包检测、阈值与退出码、可选排除清单）与自测（100% 覆盖自身）；`make cover` 串起 `go test -covermode=atomic` 与门禁；CI 的 `go` job 在 `go test` 之后执行。
- [x] 数据库层：`internal/meta` 用既有 `sqlmock` 骨架补齐 `postgres_graph.go` / `postgres_exec.go` 的全部错误分支、空集合分支、截断、过滤与 `GraphShortestPath` 委托；`sqlmock` 由 indirect 升为直接依赖，无新增下载。
- [x] 包内补强：handlers（图 handler 失败注入、计算 422 展示门、审计/列举错误、MCP 事件流心跳/寿命/关闭）、mcp（全部 18 个工具的错误与参数边界、batch、版本回退、熵源失败）、cli（10 个图子命令的解析/装配/服务错误、stdio 读写失败、超长行、`consoleMode`）、service（`MCPBackend` 逐方法等值）、okf（schema 预算与畸形条目、解析期属性错误）、graph/router/config 各剩余分支。
- [x] 接缝与死代码：新增 `randRead`、`scanRow`、`encodeNode`、`newRequest`、`parseURL`、`nodeFrom` 与 SSE 间隔变量，删除固定结构的 `json.Marshal` 分支、`encoder.Close` 分支与 `shortestPathByLevel` 的重复 parent 检查；全部登记在 `docs/testing.md`。
- [x] e2e：新增 `G09`（门禁对 50% profile 返回 1）与 `G10`（`make cover` 已接入 Makefile 与 CI），用例 199 → 201。
- [x] 文档：新增 `docs/testing.md`（口径、命令、排除策略、替身理由、接缝清单），同步 `docs/e2e.md`、`AGENTS.md`、`requirements.md`、中英 README 与三份发布说明。
- [x] 验收：`make cover` 全 29 个包 100%（7672/7672 语句）；`go build ./...`、`go vet ./...`、`go test ./...`、`make check-boundaries`、`make e2e`（201 条）全绿。

## 验收

```bash
go test ./... -count=1
go vet ./...
go build -o bin/agentmem ./cmd/agentmem
go run ./cmd/agentmem validate --env .env.example
go run ./cmd/agentmem eval --dataset testdata/eval.jsonl --k 5
make check-boundaries
make check-trust-store
make web-audit
make e2e
cd web && bun test
cd web && bun run build
```

验收条件：以上命令全部成功；不启动 Postgres、不访问网络、不需要密钥；四个插件在离线 mock 下可装配、可写、可查、可启停；`postgres` 计算链路离线可执行且 attestation 失败拒绝展示，四个出向 runtime 经 sidecar 可执行且 `body_digest` 不符时拒绝展示；鉴权矩阵、越权、伪造主体、错误脱敏与限流均有离线用例；系统/镜像 CA bundle 不含 eMudhra 弱证书指纹；13 语言文案完整、端点表与 `http.go` 一致（含 401/403/429）、CSP 哈希自洽、单色守卫通过，web 仍可用任意静态服务器直接托管；`AGENTMEM_CONSOLE_ENABLED=false` 时控制台消失而 API 与 `/healthz` 不变。

真实 Postgres 不在离线验收线内：`postgres-integration` 作业每日定时在临时 `pgvector/pgvector:pg16` service container 上跑 `TestPostgresIntegration`（也可手动 `workflow_dispatch` 触发），覆盖 GORM、版本化迁移、`computation_runs` 与向量 `Scanner/Valuer` 这些 fake 无法证明的路径。
