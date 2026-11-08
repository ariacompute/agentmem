# agentmem 发布说明

基于 OKF+Git、PostgreSQL/GORM、可插拔 RAG 与规则优先智能路由的 Go 知识中枢。

## 主要特性

- **OKF v0.2 + Git**：宽松 frontmatter 兼容、渐进式 `index.md`、`log.md`、链接图、信任等级、Attested Computation 契约与执行、go-git 版本化正文。
- **PostgreSQL + GORM**：仅存元数据，版本化 SQL 迁移（`003_computations.sql`）、事务 upsert、ACL、决策审计、计算 verdict、迁移游标与 embedding 指纹；库内不存正文/SQL/结果值。
- **引擎插件**：统一 `Adapter` 端口与注册表；内置 `okf`、`memory`、`pgvector`；LlamaIndex / PageIndex / GraphRAG / LightRAG 为独立 Go 插件包，含严格 `plugin.yaml` 清单、`sdk_boundary` 声明、生命周期/健康检查与协议指纹，离线可测。
- **Attested Computation**：参数绑定 → 只读 `postgres` 执行 → receipt → 确定性 Go attester（SQL provenance + 展示值 fidelity）→ 失败拒绝展示 → PG 审计。
- **智能路由**：确定性 `R1`–`R7` 规则优先，AFM-D System One 兜底（5000ms 超时、256 条缓存、低分保护、失败关闭）；读写双程 + 可续跑迁移。
- **接口**：JSON HTTP API（含 `/v1/computations`）、十个 CLI 命令（含 `compute` / `computations`）、纯函数检索/路由评测。
- **Web 控制台**：零依赖原生 ES module 单页，含状态视图与内置 HTTP API 文档（9 个 method+path 组合、请求/响应 JSON 样例、通用约定与错误状态码）。界面与文档正文提供 13 种语言完整译文（英文兜底、阿拉伯语 RTL）；纯黑白灰 light / dark 双主题默认跟随系统，仅显式切换才记忆。离线 `node:test` 套件守住译文覆盖、端点漂移与单色约束。
- **Web 渲染检查（impeccable）**：接入 [pbakaus/impeccable](https://github.com/pbakaus/impeccable) 做设计与反模式检测。`make web-audit` 对 `web/` 做源码级 `impeccable detect`（无需浏览器），`npm run audit:live` 针对运行中站点加本地 Chrome/Edge 做完整元素级检查（对比度、过小字号、间距）。保持单色不变式：移除被标记为「AI 痕迹」的 `.notice` 侧边强调边框，并提升三级文字对比度以满足 WCAG AA。
- **`.env` 配置**：唯一配置文件为被 git 忽略的 `.env`（模板 `.env.example`），采用封闭 `AGENTMEM_*` 键白名单、进程环境覆盖与零依赖解析；Docker Compose 通过 `env_file` 与 `${...}` 插值复用同一文件，版本化文件不含凭据。
- **安全加固（不兼容变更）**：请求不再能自报身份。Bearer 令牌以 SHA-256 摘要配置，映射到主体与权限，六个权限覆盖全部 9 条路由（`401` / `403` / `429` 进入对外契约）。私有正文仅 owner 可读，被拒读取与文档不存在不可区分。错误体统一脱敏，出向调用走主机白名单并逐跳复检重定向，bundle 写入防符号链接逃逸且以 `0600` 原子落盘，OKF/YAML 解析与计算执行均有资源上限，危险 SQL 函数被拒，CLI 桥接只允许裸可执行程序名。生产环境缺少鉴权、缺少令牌、DSN 含 `sslmode=disable` 或未配出向白名单时拒绝启动。容器以非 root 运行且发布端口仅绑回环，CI/release 采用最小 `permissions`，`make check-trust-store` 离线扫描 CA bundle 中的 eMudhra（emSign）弱证书指纹。

## 已知限制

- Go 二进制内不链接 Python 原生 SDK：由 sidecar 实现各插件 `sdk_boundary` 声明的线协议。
- Attested Computation 仅 `postgres` runtime 真实执行；`bigquery` / `dbt` / `python` / `looker` 为正式契约骨架，返回 `Unsupported`。
- Web 控制台是静态页面，由反向代理或任意文件服务器提供，Go 二进制不托管；内置 API 文档为只读端点参考，不提供交互式试调用。
- `002_pgvector.sql` 为尽力而为；扩展不可用时元数据层仍可启动并输出告警。
- 令牌是静态的：轮换需修改 `.env` 并重启；分布式限流、OIDC/SSO、mTLS 与 actions/镜像的完整 SHA 钉住仍属后续项，见 `SECURITY.md`。
