# agentmem v0.1.0

**知识正文有权威，长尾检索可插拔，路由决策可回放。**

## 新特性

- OKF v0.2 + Git：宽松兼容、渐进披露、变更日志、信任等级、版本化正文
- PostgreSQL + GORM：元数据专属存储、版本化迁移、事务 upsert、ACL 与审计
- 引擎插件：四个 Python 引擎为独立 Go 插件包，SDK 边界由 `plugin.yaml` 声明
- Attested Computation：参数绑定、postgres 执行、确定性 attester、失败拒绝展示、PG 审计
- 智能路由：规则优先、System One 兜底、读写双程、存量双向迁移
- HTTP API（含 `/v1/computations`）、CLI（含 `compute`）、纯函数评测、最小状态页
- `.env` 单一配置源：封闭键白名单、进程环境覆盖、Docker Compose 复用同一文件

## 验证

`go test ./...`、`go vet ./...`、`go build ./cmd/agentmem` 离线全绿。

## 已知限制

四个 Python 引擎已是 Go 插件包，SDK 边界由 `plugin.yaml` 的 `sdk_boundary` 声明；Attested Computation 仅 `postgres` runtime 执行，其余返回 `Unsupported`；Vite 页面为模板级。
