# agentmem v0.1.0

首个版本：基于 OKF+Git、PostgreSQL/GORM、可插拔 RAG 与规则优先智能路由的 Go 知识中枢。

## 主要特性

- **OKF v0.2 + Git**：宽松 frontmatter 兼容、渐进式 `index.md`、`log.md`、链接图、信任等级、Attested Computation 契约建模、go-git 版本化正文。
- **PostgreSQL + GORM**：仅存元数据，版本化 SQL 迁移、事务 upsert、ACL、决策审计、迁移游标与 embedding 指纹；库内不存正文。
- **插件化 RAG**：统一 `Adapter` 端口与注册表；内置 `okf`、`memory`、`pgvector`；LlamaIndex / PageIndex / GraphRAG / LightRAG 提供 HTTP/CLI 桥接声明与离线 mock。
- **智能路由**：确定性 `R1`–`R7` 规则优先，AFM-D System One 兜底（5000ms 超时、256 条缓存、低分保护、失败关闭）；读写双程 + 可续跑迁移。
- **接口**：JSON HTTP API、八个 CLI 命令、纯函数检索/路由评测、最小 Vite 状态页。

## 验证

`go test ./...`、`go vet ./...`、`go build ./cmd/agentmem` 均离线通过。

## 已知限制

- 四个 Python 生态引擎为桥接契约 + 确定性 mock，未接真实 SDK（`TODO(real)`）。
- Attested Computation 仅建模，不执行。
- Vite 页面为模板级（健康状态 + 决策列表）。
- `002_pgvector.sql` 为尽力而为；扩展不可用时元数据层仍可启动并输出告警。
