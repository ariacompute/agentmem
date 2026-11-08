# agentmem 发布说明

🧠 基于 OKF+Git、PostgreSQL/GORM、可插拔 RAG 与规则优先智能路由的 Go 知识中枢。

## ✨ 新特性

- 📦 **OKF v0.2 + Git**：宽松兼容、渐进披露、变更日志、信任等级、Attested Computation 契约与执行、版本化正文
- 🗄️ **PostgreSQL + GORM**：仅存元数据；版本化迁移、事务 upsert、ACL、决策与计算 verdict 审计；库内不存正文、SQL 或结果值
- 🧩 **引擎插件**：四个 Python 引擎为独立 Go 插件包，以 `plugin.yaml` 的 `sdk_boundary` 声明边界，含生命周期、健康检查与协议指纹，离线可测
- ✅ **Attested Computation**：参数绑定 → 只读 `postgres` 执行 → receipt → 确定性 Go attester → 失败拒绝展示 → PG 审计
- 🧭 **智能路由**：确定性规则优先、System One 兜底、读写双程、存量双向迁移
- 🌐 **接口**：HTTP API（含 `/v1/computations`）、十个 CLI 命令（含 `compute` / `computations`）、纯函数评测
- 🖥️ **Web 控制台**：零依赖 ES module 单页，状态视图 + 内置 HTTP API 文档（9 个端点组合、JSON 样例、通用约定与错误码）；13 语言完整译文、英文兜底、阿拉伯语 RTL；纯黑白灰 light / dark 双主题跟随系统；离线 `node:test` 守住译文、端点漂移与单色约束
- 🎨 **Web 渲染检查（impeccable）**：接入 pbakaus/impeccable 做设计与反模式检测。`make web-audit` 提供源码级扫描（无需浏览器），`npm run audit:live` 针对运行中站点做元素级检查（对比度、过小字号、间距）。保持单色不变式，移除被标记为「AI 痕迹」的侧边强调边框，并提升三级文字对比度以满足 WCAG AA
- ⚙️ **`.env` 配置**：单一配置源（模板 `.env.example`）、封闭 `AGENTMEM_*` 键白名单、进程环境覆盖、零依赖解析；Docker Compose 经 `env_file` 与 `${...}` 复用同一文件
- 🔐 **安全加固**：Bearer 令牌仅存 SHA-256 摘要，配合权限矩阵与可信主体注入（请求体不再接受 `subject`）；私有正文强隔离、错误体脱敏、限流与 Origin 校验；出向主机白名单 + 逐跳重定向复检；bundle 路径防符号链接逃逸与 `0600` 原子写；危险 SQL 拦截与执行超时；非 root 容器 + 回环端口 + 最小权限 CI；`make check-trust-store` 离线扫描 eMudhra（emSign）弱证书

## ⚠️ 已知限制

- 🔌 Go 二进制不链接 Python 原生 SDK，由 sidecar 实现 `sdk_boundary` 声明的线协议
- 🧪 Attested Computation 仅 `postgres` runtime 真实执行；`bigquery` / `dbt` / `python` / `looker` 返回 `Unsupported`
- 📄 Web 控制台为静态页面，由反向代理或任意文件服务器提供，Go 二进制不托管；API 文档只读，无交互式试调用
- 🧩 `002_pgvector.sql` 为尽力而为；扩展不可用时元数据层仍可启动并输出告警
