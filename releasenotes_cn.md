# agentmem 发布说明

基于 OKF+Git、PostgreSQL/GORM、可插拔 RAG 与规则优先智能路由的 Go 知识中枢。

## 主要特性

- **OKF v0.2 + Git**：宽松 frontmatter 兼容、渐进式 `index.md`、`log.md`、链接图、信任等级、Attested Computation 契约与执行、go-git 版本化正文。
- **PostgreSQL + GORM**：仅存元数据，版本化 SQL 迁移（`003_computations.sql`）、事务 upsert、ACL、决策审计、计算 verdict、迁移游标与 embedding 指纹；库内不存正文/SQL/结果值。
- **引擎插件**：统一 `Adapter` 端口与注册表；内置 `okf`、`memory`、`pgvector`；LlamaIndex / PageIndex / GraphRAG / LightRAG 为独立 Go 插件包，含严格 `plugin.yaml` 清单、`sdk_boundary` 声明、生命周期/健康检查与协议指纹，离线可测。
- **Attested Computation**：参数绑定 → 只读 `postgres` 执行 → receipt → 确定性 Go attester（SQL provenance + 展示值 fidelity）→ 失败拒绝展示 → PG 审计。`examples/compute-sidecars/sidecar.py` 是线协议 v1 的参考实现：`cli` stdio 与 `http POST /run` 双通道，按 runtime 分派 `bigquery` / `dbt` / `looker` / `python`，`SIDECAR_OFFLINE=1` 时返回确定性表供演示与验收使用。表驱动契约测试覆盖全部四个出向 runtime（请求字段、`body_digest` 相符与不符、未知响应字段拒绝、`postgres` 不得声明为 sidecar），黑盒用例 `C33`–`C35` 端到端跑通 `bigquery` / `dbt` / `looker`。
- **声明式知识图谱**：概念既是文档也是节点。`graph` frontmatter 声明节点类型/标签/属性与类型化关系，`ontology-schema` 文档声明允许的节点类型与关系类型首尾约束；只有「已声明关系类型的首尾类型不匹配」是硬拒绝（`422`），未知类型、未知节点类型与断链只告警，与 OKF 宽松口径一致。PostgreSQL 只保存可丢弃的 `graph_nodes` / `graph_edges` 邻接索引：可从 Git 全量重建、清理已消失的正文，并在 `content_hash` 漂移时按单文档惰性修复。读侧新增节点详情（血缘、属性、计算摘要、出边入边）、一次递归 CTE 完成的有界邻域与最短路径、文本与图邻域的 RRF 融合检索，以及按 0.6/0.15/0.15/0.1 加权覆盖率给出 `answerable` / `partial` / `insufficient` 与七类缺口的可回答性判定——证据不足时只报缺口，不硬答。可选外部 HTTP 图引擎只承载只读查询，写入永远落 OKF+Git。
- **智能路由**：确定性 `R1`–`R7` 规则优先，AFM-D System One 兜底（5000ms 超时、256 条缓存、低分保护、失败关闭）；读写双程 + 可续跑迁移。
- **MCP server**：一份协议内核（`internal/mcp`）对接两条传输——Streamable HTTP（`POST` / `GET` / `DELETE /mcp`、`Mcp-Session-Id`、SSE 事件流、`MCP-Protocol-Version`）与 stdio（`agentmem mcp`，逐行 JSON-RPC）。协议版本 `2025-06-18` / `2025-03-26` 协商，回退无状态 `2024-11-05`；每条结果同时给 `structuredContent` 与 `content[0].text` 的 JSON 字符串。13 个只读工具覆盖全部读面（图读、`query`、`read_knowledge`、审计列表）；5 个写工具仅在 `AGENTMEM_MCP_WRITE=true` 时登记，每次调用先按自身 scope 校验再进入 `service`，越权写入不会产生任何 Git 提交。stdio 身份优先 `--token` / `AGENTMEM_MCP_TOKEN`，仅在鉴权关闭时回退 `--subject` + `--scopes`，两者皆无则拒绝启动。
- **接口**：JSON HTTP API（含 `/v1/computations`、`/v1/graph*` 与 `/mcp` 传输）、十二个 CLI 命令（含 `compute` / `computations` / `graph` / `mcp`）、纯函数检索/路由评测。
- **Web 控制台（编入二进制）**：零依赖原生 ES module 单页，含状态视图、内置 HTTP API 文档（23 个 method+path 组合、请求/响应 JSON 样例、通用约定与错误状态码）与图谱视图（索引新鲜度徽标、节点检索与类型过滤、粘性血缘面板与出边入边表格、有界邻域探索、覆盖率进度条与缺口清单）。Vite 直接输出到 `internal/webui/dist`，`//go:embed` 将其编入二进制，`serve` 在同一监听端口用 `GET /` 与 `/assets/*` 提供页面——不再需要反向代理、document root 或第二个进程。页面 CSP 在启动时由内联主题脚本的 SHA-256 现算，JSON 响应保持 `default-src 'none'`；带哈希的资源 `immutable`、首页 `no-cache`。界面与文档正文提供 13 种语言完整译文（英文兜底、阿拉伯语 RTL）；纯黑白灰 light / dark 双主题默认跟随系统，仅显式切换才记忆。离线 `node:test` 套件守住译文覆盖、端点漂移与单色约束。`AGENTMEM_CONSOLE_ENABLED`（默认 `true`）可关闭页面：设为 `false` 后 `GET /`、`/assets/*` 与未知静态路径统一返回 JSON `404`，23 条 API 路由、权限矩阵、限流与 `GET /healthz` 完全不变，且无需重新编译二进制。
- **覆盖率门禁**：`make cover` 要求 `go list ./...` 中每个包的语句覆盖率都是 100%，且按包统计——只被其他包测试顺带跑到的包仍然算失败。PostgreSQL 存储层用 `sqlmock` 替身离线覆盖（无数据库、无网络），真实 SQL 语义仍由每日 `postgres-integration` 作业验证。门禁打印包级明细、缺包即失败、支持带理由的排除清单（当前为空），并已接入 CI。七处可证不可达的分支被删除或改为默认真实实现的接缝，逐条记录在 `docs/testing.md`。
- **交互式 API 文档**：每个端点行可展开为试调用工作区，直接发出真实的同源请求——可编辑查询参数与 JSON 请求体，`Ctrl/Cmd+Enter` 发送，返回状态码、耗时、响应头与美化后的响应体。Bearer 令牌仅存于 `sessionStorage`，只作为 `Authorization` 头发送（不进 URL、草稿或日志）；三个写操作端点必须先显式确认才发送。草稿在重绘后保留，结果只留在内存。
- **Web 渲染检查（impeccable）**：接入 [pbakaus/impeccable](https://github.com/pbakaus/impeccable) 做设计与反模式检测。`make web-audit` 对 `web/` 做源码级 `impeccable detect`（无需浏览器），`bun run audit:live` 针对运行中站点加本地 Chrome/Edge 做完整元素级检查（对比度、过小字号、间距）。保持单色不变式：移除被标记为「AI 痕迹」的 `.notice` 侧边强调边框，并提升三级文字对比度以满足 WCAG AA。
- **`.env` 配置**：唯一配置文件为被 git 忽略的 `.env`（模板 `.env.example`），采用封闭 `AGENTMEM_*` 键白名单、进程环境覆盖与零依赖解析；Docker Compose 通过 `env_file` 与 `${...}` 插值复用同一文件，版本化文件不含凭据。
- **安全加固（不兼容变更）**：请求不再能自报身份。Bearer 令牌以 SHA-256 摘要配置，映射到主体与权限，七个权限覆盖全部 23 条路由（`401` / `403` / `429` 进入对外契约）。私有正文仅 owner 可读，被拒读取与文档不存在不可区分。错误体统一脱敏，出向调用走主机白名单并逐跳复检重定向，bundle 写入防符号链接逃逸且以 `0600` 原子落盘，OKF/YAML 解析与计算执行均有资源上限，危险 SQL 函数被拒，CLI 桥接只允许裸可执行程序名。生产环境缺少鉴权、缺少令牌、DSN 含 `sslmode=disable` 或未配出向白名单时拒绝启动。容器以非 root 运行且发布端口仅绑回环，CI/release 采用最小 `permissions`，`make check-trust-store` 离线扫描 CA bundle 中的 eMudhra（emSign）弱证书指纹。
- **离线可测性**：所有外部依赖都收敛到可注入的接缝——内嵌清单与 SQL 迁移、GORM 连接、bundle 临时文件创建、插件表，以及 `os.Exit` / 装配 / serve 循环入口——因此过去必须有真实 PostgreSQL、10 万条目 bundle 或损坏清单才能走到的分支，现在只靠 `go test ./...` 即可覆盖，无需数据库、网络或凭据。`safepath.Clean`、`nodeFromYAML` 与 `vcs` 的提交键推导取代了重复的路径 / Rel 逻辑；不可能失败的保护性分支直接删除，而不是留在未测试状态。router 包语句覆盖率 99.6%，剩余两条语句可证明不可达。依赖 `os.Chmod` 制造拒绝场景的权限断言在 root 下自动跳过——`CAP_DAC_OVERRIDE` 使该否定断言不可成立，root 容器不再对正确代码误报。
- **真实 Postgres 持续验证**：`postgres-integration` 作业改为每日定时（同时保留手动派发），在临时 `pgvector/pgvector:pg16` service container 上验证 GORM、版本化迁移、`computation_runs` 与向量 `Scanner/Valuer`——这条离线套件无法证明的生产路径因此每 24 小时内必被真实数据库覆盖一次。

## 已知限制

- Go 二进制内不链接 Python 原生 SDK：由 sidecar 实现各插件 `sdk_boundary` 声明的线协议。
- Attested Computation 覆盖全部五个 runtime：`postgres` 始终进程内执行；`bigquery` / `dbt` / `python` / `looker` 通过 `AGENTMEM_COMPUTE_SIDECARS` 声明的出向执行器（`http` 端点或 `cli` 裸命令，协议见 `docs/compute-sidecar.md`）真实执行。未配置 sidecar 的 runtime 保持 fail-closed 并返回 `Unsupported`；sidecar 回执必须回传所执行正文的 SHA-256，否则拒绝展示数值。
- 内嵌控制台属于构建产物：修改界面后需执行 `make web`（或 `bun run build`）刷新并提交 `internal/webui/dist`；CI 会在产物过期时失败，`make web-verify` 可在本地复现该检查。试调用结果只存在内存，刷新页面或切换主题即清空。
- 默认 `go test ./...` 仍完全跑在 fake 上；真实 PostgreSQL 覆盖来自定时（或手动派发）的 `postgres-integration` 作业，该作业不门禁 Pull Request。
- `002_pgvector.sql` 为尽力而为；扩展不可用时元数据层仍可启动并输出告警。
- 令牌是静态的：轮换需修改 `.env` 并重启；分布式限流、OIDC/SSO、mTLS 与 actions/镜像的完整 SHA 钉住仍属后续项，见 `SECURITY.md`。
