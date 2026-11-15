# agentmem 发布说明

基于 OKF+Git、PostgreSQL/GORM、可插拔 RAG 与规则优先智能路由的 Go 知识中枢。

## 主要特性

- **OKF v0.2 + Git**：宽松 frontmatter 兼容、渐进式 `index.md`、`log.md`、链接图、信任等级、Attested Computation 契约与执行、go-git 版本化正文。
- **PostgreSQL + GORM**：仅存元数据，版本化 SQL 迁移（`003_computations.sql`）、事务 upsert、ACL、决策审计、计算 verdict、迁移游标与 embedding 指纹；库内不存正文/SQL/结果值。
- **引擎插件**：统一 `Adapter` 端口与注册表；内置 `okf`、`memory`、`pgvector`；LlamaIndex / PageIndex / GraphRAG / LightRAG 为独立 Go 插件包，含严格 `plugin.yaml` 清单、`sdk_boundary` 声明、生命周期/健康检查与协议指纹，离线可测。
- **Attested Computation**：参数绑定 → 只读 `postgres` 执行 → receipt → 确定性 Go attester（SQL provenance + 展示值 fidelity）→ 失败拒绝展示 → PG 审计。
- **智能路由**：确定性 `R1`–`R7` 规则优先，AFM-D System One 兜底（5000ms 超时、256 条缓存、低分保护、失败关闭）；读写双程 + 可续跑迁移。
- **接口**：JSON HTTP API（含 `/v1/computations`）、十个 CLI 命令（含 `compute` / `computations`）、纯函数检索/路由评测。
- **Web 控制台（编入二进制）**：零依赖原生 ES module 单页，含状态视图与内置 HTTP API 文档（9 个 method+path 组合、请求/响应 JSON 样例、通用约定与错误状态码）。Vite 直接输出到 `internal/webui/dist`，`//go:embed` 将其编入二进制，`serve` 在同一监听端口用 `GET /` 与 `/assets/*` 提供页面——不再需要反向代理、document root 或第二个进程。页面 CSP 在启动时由内联主题脚本的 SHA-256 现算，JSON 响应保持 `default-src 'none'`；带哈希的资源 `immutable`、首页 `no-cache`。界面与文档正文提供 13 种语言完整译文（英文兜底、阿拉伯语 RTL）；纯黑白灰 light / dark 双主题默认跟随系统，仅显式切换才记忆。离线 `node:test` 套件守住译文覆盖、端点漂移与单色约束。
- **交互式 API 文档**：每个端点行可展开为试调用工作区，直接发出真实的同源请求——可编辑查询参数与 JSON 请求体，`Ctrl/Cmd+Enter` 发送，返回状态码、耗时、响应头与美化后的响应体。Bearer 令牌仅存于 `sessionStorage`，只作为 `Authorization` 头发送（不进 URL、草稿或日志）；三个写操作端点必须先显式确认才发送。草稿在重绘后保留，结果只留在内存。
- **Web 渲染检查（impeccable）**：接入 [pbakaus/impeccable](https://github.com/pbakaus/impeccable) 做设计与反模式检测。`make web-audit` 对 `web/` 做源码级 `impeccable detect`（无需浏览器），`bun run audit:live` 针对运行中站点加本地 Chrome/Edge 做完整元素级检查（对比度、过小字号、间距）。保持单色不变式：移除被标记为「AI 痕迹」的 `.notice` 侧边强调边框，并提升三级文字对比度以满足 WCAG AA。
- **`.env` 配置**：唯一配置文件为被 git 忽略的 `.env`（模板 `.env.example`），采用封闭 `AGENTMEM_*` 键白名单、进程环境覆盖与零依赖解析；Docker Compose 通过 `env_file` 与 `${...}` 插值复用同一文件，版本化文件不含凭据。
- **安全加固（不兼容变更）**：请求不再能自报身份。Bearer 令牌以 SHA-256 摘要配置，映射到主体与权限，六个权限覆盖全部 9 条路由（`401` / `403` / `429` 进入对外契约）。私有正文仅 owner 可读，被拒读取与文档不存在不可区分。错误体统一脱敏，出向调用走主机白名单并逐跳复检重定向，bundle 写入防符号链接逃逸且以 `0600` 原子落盘，OKF/YAML 解析与计算执行均有资源上限，危险 SQL 函数被拒，CLI 桥接只允许裸可执行程序名。生产环境缺少鉴权、缺少令牌、DSN 含 `sslmode=disable` 或未配出向白名单时拒绝启动。容器以非 root 运行且发布端口仅绑回环，CI/release 采用最小 `permissions`，`make check-trust-store` 离线扫描 CA bundle 中的 eMudhra（emSign）弱证书指纹。
- **离线可测性**：所有外部依赖都收敛到可注入的接缝——内嵌清单与 SQL 迁移、GORM 连接、bundle 临时文件创建、插件表，以及 `os.Exit` / 装配 / serve 循环入口——因此过去必须有真实 PostgreSQL、10 万条目 bundle 或损坏清单才能走到的分支，现在只靠 `go test ./...` 即可覆盖，无需数据库、网络或凭据。`safepath.Clean`、`nodeFromYAML` 与 `vcs` 的提交键推导取代了重复的路径 / Rel 逻辑；不可能失败的保护性分支直接删除，而不是留在未测试状态。router 包语句覆盖率 99.6%，剩余两条语句可证明不可达。

## 已知限制

- Go 二进制内不链接 Python 原生 SDK：由 sidecar 实现各插件 `sdk_boundary` 声明的线协议。
- Attested Computation 覆盖全部五个 runtime：`postgres` 始终进程内执行；`bigquery` / `dbt` / `python` / `looker` 通过 `AGENTMEM_COMPUTE_SIDECARS` 声明的出向执行器（`http` 端点或 `cli` 裸命令，协议见 `docs/compute-sidecar.md`）真实执行。未配置 sidecar 的 runtime 保持 fail-closed 并返回 `Unsupported`；sidecar 回执必须回传所执行正文的 SHA-256，否则拒绝展示数值。
- 内嵌控制台属于构建产物：修改界面后需执行 `make web`（或 `bun run build`）刷新并提交 `internal/webui/dist`；CI 会在产物过期时失败，`make web-verify` 可在本地复现该检查。试调用结果只存在内存，刷新页面或切换主题即清空。
- `002_pgvector.sql` 为尽力而为；扩展不可用时元数据层仍可启动并输出告警。
- 令牌是静态的：轮换需修改 `.env` 并重启；分布式限流、OIDC/SSO、mTLS 与 actions/镜像的完整 SHA 钉住仍属后续项，见 `SECURITY.md`。
