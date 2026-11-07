# agentmem 实施任务与验收清单

## 阶段 1 · 骨架与 OKF+Git 内核

- [x] `go.mod` / `Makefile` / `config.example.yaml`；严格配置校验（`KnownFields(true)`、`AGENTMEM_*` 覆盖、生产 DSN 必填）。
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

- [x] 通用 HTTP 桥接（超时、重试上限、健康检查、JSON 线协议、`TODO(real)` 标注）。
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
- [x] CLI 八个命令、`cmd/agentmem` 入口。
- [x] `internal/eval` 纯函数指标与路由准确率。
- [x] 文档、CI、Docker、compose、最小 Vite 页面、示例 bundle 与评测数据集。

## 验收

```bash
go test ./... -count=1
go vet ./...
go build -o bin/agentmem ./cmd/agentmem
go run ./cmd/agentmem validate
go run ./cmd/agentmem eval --dataset testdata/eval.jsonl --k 5
```

验收条件：以上命令全部成功；不启动 Postgres、不访问网络、不需要密钥；五个引擎在离线 mock 下可装配、可写、可查。
