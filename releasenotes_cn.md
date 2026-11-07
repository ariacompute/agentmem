# agentmem 发布说明

基于 OKF+Git、PostgreSQL/GORM、可插拔 RAG 与规则优先智能路由的 Go 知识中枢。

## 主要特性

- **OKF v0.2 + Git**：宽松 frontmatter 兼容、渐进式 `index.md`、`log.md`、链接图、信任等级、Attested Computation 契约与执行、go-git 版本化正文。
- **PostgreSQL + GORM**：仅存元数据，版本化 SQL 迁移（`003_computations.sql`）、事务 upsert、ACL、决策审计、计算 verdict、迁移游标与 embedding 指纹；库内不存正文/SQL/结果值。
- **引擎插件**：统一 `Adapter` 端口与注册表；内置 `okf`、`memory`、`pgvector`；LlamaIndex / PageIndex / GraphRAG / LightRAG 为独立 Go 插件包，含严格 `plugin.yaml` 清单、`sdk_boundary` 声明、生命周期/健康检查与协议指纹，离线可测。
- **Attested Computation**：参数绑定 → 只读 `postgres` 执行 → receipt → 确定性 Go attester（SQL provenance + 展示值 fidelity）→ 失败拒绝展示 → PG 审计。
- **智能路由**：确定性 `R1`–`R7` 规则优先，AFM-D System One 兜底（5000ms 超时、256 条缓存、低分保护、失败关闭）；读写双程 + 可续跑迁移。
- **接口**：JSON HTTP API（含 `/v1/computations`）、十个 CLI 命令（含 `compute` / `computations`）、纯函数检索/路由评测、最小 Vite 状态页。
- **`.env` 配置**：唯一配置文件为被 git 忽略的 `.env`（模板 `.env.example`），采用封闭 `AGENTMEM_*` 键白名单、进程环境覆盖与零依赖解析；Docker Compose 通过 `env_file` 与 `${...}` 插值复用同一文件，版本化文件不含凭据。

## 已知限制

- Go 二进制内不链接 Python 原生 SDK：由 sidecar 实现各插件 `sdk_boundary` 声明的线协议。
- Attested Computation 仅 `postgres` runtime 真实执行；`bigquery` / `dbt` / `python` / `looker` 为正式契约骨架，返回 `Unsupported`。
- Vite 页面为模板级（健康状态 + 决策列表）。
- `002_pgvector.sql` 为尽力而为；扩展不可用时元数据层仍可启动并输出告警。
