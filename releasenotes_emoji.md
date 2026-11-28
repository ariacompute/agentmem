# agentmem 发布说明

🧠 基于 OKF+Git、PostgreSQL/GORM、可插拔 RAG 与规则优先智能路由的 Go 知识中枢。

## ✨ 新特性

- 📦 **OKF v0.2 + Git**：宽松兼容、渐进披露、变更日志、信任等级、Attested Computation 契约与执行、版本化正文
- 🗄️ **PostgreSQL + GORM**：仅存元数据；版本化迁移、事务 upsert、ACL、决策与计算 verdict 审计；库内不存正文、SQL 或结果值
- 🧩 **引擎插件**：四个 Python 引擎为独立 Go 插件包，以 `plugin.yaml` 的 `sdk_boundary` 声明边界，含生命周期、健康检查与协议指纹，离线可测
- ✅ **Attested Computation**：参数绑定 → 只读 `postgres` 执行 → receipt → 确定性 Go attester → 失败拒绝展示 → PG 审计；`examples/compute-sidecars` 提供线协议 v1 参考实现（`cli` / `http` 双通道、四 runtime 分派、`SIDECAR_OFFLINE=1` 确定性表），四个出向 runtime 均有表驱动契约测试（含 `body_digest` 校验），`C33`–`C35` 端到端跑通 `bigquery` / `dbt` / `looker`
- 🕸️ **声明式知识图谱**：概念既是文档也是节点，`graph` / `graph_schema` 写在 frontmatter（Git 唯一权威），PostgreSQL 只存可丢弃的邻接索引；已声明类型的首尾错配 `422`，未知类型与断链只告警；读侧提供节点详情、递归 CTE 邻域与最短路径、RRF 融合检索与带缺口的可回答性判定，全程零 LLM、零 embedding
- 🧭 **智能路由**：确定性规则优先、System One 兜底、读写双程、存量双向迁移
- 🤖 **MCP server**：一份协议内核 + 两条传输——Streamable HTTP（`POST` / `GET` / `DELETE /mcp`、会话头、SSE、协议版本头）与 stdio（`agentmem mcp` 逐行 JSON-RPC）；`2025-06-18` / `2025-03-26` 协商并回退 `2024-11-05`；结果同时给 `structuredContent` 与 `content[0].text`；13 个只读工具常驻，5 个写工具仅在 `AGENTMEM_MCP_WRITE=true` 时登记并逐个校验 scope（越权不落 Git）；stdio 身份优先 bearer 令牌，仅在鉴权关闭时回退 `--subject` + `--scopes`，两者皆无拒绝启动
- 🌐 **接口**：HTTP API（含 `/v1/computations`、`/v1/graph*`、`/mcp` 传输）、十二个 CLI 命令（含 `compute` / `computations` / `graph` / `mcp`）、纯函数评测
- 🖥️ **Web 控制台（编入二进制）**：零依赖 ES module 单页，状态视图 + 内置 HTTP API 文档（23 个端点组合、JSON 样例、通用约定与错误码）+ 图谱视图（索引新鲜度、节点检索、血缘与边表格、邻域探索、可回答性）；Vite 直接输出到 `internal/webui/dist`，`//go:embed` 编入二进制，`GET /` 与 `/assets/*` 与 API 同端口提供，无需反向代理或第二个进程；页面 CSP 启动时由内联脚本 SHA-256 现算，JSON 响应保持 `default-src 'none'`，哈希资源 `immutable`、首页 `no-cache`；13 语言完整译文、英文兜底、阿拉伯语 RTL；纯黑白灰 light / dark 双主题跟随系统；离线 `node:test` 守住译文、端点漂移与单色约束；`AGENTMEM_CONSOLE_ENABLED=false` 可关闭页面（`/` 与 `/assets/*` 转 JSON `404`，23 条 API 路由与 `/healthz` 不变）
- 📏 **覆盖率门禁**：`make cover` 要求每包语句覆盖率 100%，按包统计（只被别的包测试跑到仍算失败）；PostgreSQL 层用 `sqlmock` 离线覆盖，真实 SQL 语义留给每日 `postgres-integration`；门禁缺包即失败、支持带理由的排除清单（当前为空）、已接入 CI，并新增 e2e `G09`/`G10` 证明它会失败且确实接线
- 🧪 **交互式 API 文档**：端点行展开即试调用——可编辑查询参数与 JSON 体、`Ctrl/Cmd+Enter` 发送，展示状态码、耗时、响应头与美化响应体；Bearer 令牌仅存 `sessionStorage` 且只走 `Authorization` 头（不进 URL、草稿或日志）；六个写端点需显式确认后才发送；草稿跨重绘保留，结果只在内存
- 🎨 **Web 渲染检查（impeccable）**：接入 pbakaus/impeccable 做设计与反模式检测。`make web-audit` 提供源码级扫描（无需浏览器），`bun run audit:live` 针对运行中站点做元素级检查（对比度、过小字号、间距）。保持单色不变式，移除被标记为「AI 痕迹」的侧边强调边框，并提升三级文字对比度以满足 WCAG AA
- ⚙️ **`.env` 配置**：单一配置源（模板 `.env.example`）、封闭 `AGENTMEM_*` 键白名单、进程环境覆盖、零依赖解析；Docker Compose 经 `env_file` 与 `${...}` 复用同一文件
- 🔐 **安全加固**：Bearer 令牌仅存 SHA-256 摘要，配合权限矩阵与可信主体注入（请求体不再接受 `subject`）；私有正文强隔离、错误体脱敏、限流与 Origin 校验；出向主机白名单 + 逐跳重定向复检；bundle 路径防符号链接逃逸与 `0600` 原子写；危险 SQL 拦截与执行超时；非 root 容器 + 回环端口 + 最小权限 CI；`make check-trust-store` 离线扫描 eMudhra（emSign）弱证书
- 🧪 **离线可测性**：外部依赖全部收敛到可注入接缝（内嵌清单与 SQL 迁移、GORM 连接、bundle 临时文件、插件表、`os.Exit` / 装配 / serve 入口），过去需要真实 PostgreSQL、10 万条目 bundle 或损坏清单的分支现在由 `go test ./...` 直接覆盖，零数据库、零网络、零凭据；`safepath.Clean`、`nodeFromYAML` 与 `vcs` 提交键推导消除重复路径逻辑，不可能失败的分支直接删除；每个包语句覆盖率 100%，由 `make cover` 强制；权限失败经包内接缝注入而非 `os.Chmod`，root 与非 root 口径完全一致
- ⏱️ **真实 Postgres 持续验证**：`postgres-integration` 每日定时（保留手动派发）在临时 pgvector service container 上验证 GORM、版本化迁移、`computation_runs` 与向量 `Scanner/Valuer`

## ⚠️ 已知限制

- 🔌 Go 二进制不链接 Python 原生 SDK，由 sidecar 实现 `sdk_boundary` 声明的线协议
- 🧪 Attested Computation 覆盖五个 runtime：`postgres` 进程内执行，`bigquery` / `dbt` / `python` / `looker` 通过 `AGENTMEM_COMPUTE_SIDECARS` 声明的出向 sidecar 执行；未配置时返回 `Unsupported`，sidecar 回执须回传正文 SHA-256
- 📄 内嵌控制台是构建产物：改界面后需 `make web`（或 `bun run build`）刷新并提交 `internal/webui/dist`；CI 在产物过期时失败，`make web-verify` 可本地复现；试调用结果只在内存，刷新或切换主题即清空
- 🧩 `002_pgvector.sql` 为尽力而为；扩展不可用时元数据层仍可启动并输出告警
- 📅 默认 `go test ./...` 仍跑在 fake 上；真实 PostgreSQL 覆盖由每日（或手动）的 `postgres-integration` 作业承担，不门禁 PR
