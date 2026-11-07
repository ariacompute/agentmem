# agentmem v0.1.0

**知识正文有权威，长尾检索可插拔，路由决策可回放。**

## 新特性

- OKF v0.2 + Git：宽松兼容、渐进披露、变更日志、信任等级、版本化正文
- PostgreSQL + GORM：元数据专属存储、版本化迁移、事务 upsert、ACL 与审计
- 插件化 RAG：LlamaIndex / PageIndex / GraphRAG / LightRAG / pgvector
- 智能路由：规则优先、System One 兜底、读写双程、存量双向迁移
- HTTP API、CLI、纯函数评测、最小状态页

## 验证

`go test ./...`、`go vet ./...`、`go build ./cmd/agentmem` 离线全绿。

## 已知限制

四个 Python 引擎为桥接契约 + 离线 mock；Attested Computation 仅建模不执行；Vite 页面为模板级。
