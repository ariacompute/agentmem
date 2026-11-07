# agentmem 实施任务与验收清单

## 阶段 1 · 骨架与 OKF+Git 内核

- [x] `go.mod` / `Makefile` / `.env.example`；`.env` 单一配置源（`--env` / `AGENTMEM_ENV_FILE`、进程环境覆盖、封闭 `AGENTMEM_*` 键白名单、生产 DSN 必填），Docker Compose 用 `env_file` 与 `${...}` 插值读取同一文件。
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

- [x] 通用 HTTP 桥接（超时、重试上限、健康检查、JSON 线协议；SDK 边界由插件清单 `sdk_boundary` 正式声明）。
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
- [x] `cmd/agentmem` 入口与 CLI 命令。
- [x] `internal/eval` 纯函数指标与路由准确率。
- [x] 文档、CI、Docker、compose、最小 Vite 页面、示例 bundle 与评测数据集（Web 控制台后续升级见阶段 9）。

## 阶段 7 · 四引擎 Go 插件化

- [x] `internal/plugins/manifest` 严格清单解析、`sdk_boundary` 正式化 SDK 边界、协议指纹。
- [x] `internal/plugins/<engine>/plugin.yaml` 与 `Adapter` + `Lifecycle` 实现（四份）。
- [x] `pluginbase` 组合泛型 HTTP/CLI 桥接、引擎级引用、离线 mock 可见性、fail-closed 启动。
- [x] `adapter.Registry.StartAll/Close`、config kind 白名单、`service.Assemble` 显式注册装配。
- [x] 仓库内旧版 SDK 边界注释清零；`make check-boundaries` 与 CI 防回潮；`docs/plugins.md`。

## 阶段 8 · Attested Computation

- [x] `internal/compute`：契约加载、参数绑定、SQL 规范化、receipt、确定性 attester、五 runtime 骨架。
- [x] `meta.SQLExecutor` 端口与 `internal/meta/postgres_exec.go` 只读 GORM 执行；离线 fake。
- [x] `computation_runs` 审计：`003_computations.sql`、`MetaStore` 扩展、快照往返、幂等。
- [x] HTTP `/v1/computations`、CLI `compute` / `computations`、`docs/computations.md`。

## 阶段 9 · Web 控制台本地化与 API 文档

- [x] `web/src/lib/languages.js` + `i18n.js`：13 语言目录、`?lang=` > localStorage > 浏览器语言 > 英文、英文浅合并兜底、`<html lang|dir>` 与 RTL。
- [x] `web/src/lib/copy/shell-copy.js` / `api-copy.js`：界面文案与文档正文 13 语言完整译文。
- [x] `web/src/lib/theme.js` + `web/src/style.css` + `index.html` 首屏脚本：纯黑白灰双主题、跟随系统、显式选择持久化、`prefers-reduced-motion`。
- [x] 外壳重构：`web/src/main.js` 顶栏 / 导航 / 语言与主题切换器 / 页脚，`views/status.js` 状态视图、`views/docs.js` 文档视图。
- [x] `web/src/lib/api-content.js`：9 个 method+path 组合、JSON 样例、通用约定与状态码，严格镜像 `internal/handlers/http.go`。
- [x] `web/tests/` 六个零依赖 `node:test` 套件（语言、译文兜底、端点漂移、单色守卫、主题、DOM 冒烟）；`web/package.json` 的 `npm test` 与 CI web job 接入。
- [x] `README.md` / `README_cn.md` Web 章节同步。

## 验收

```bash
go test ./... -count=1
go vet ./...
go build -o bin/agentmem ./cmd/agentmem
go run ./cmd/agentmem validate --env .env.example
go run ./cmd/agentmem eval --dataset testdata/eval.jsonl --k 5
make check-boundaries
cd web && npm test
cd web && npm run build
```

验收条件：以上命令全部成功；不启动 Postgres、不访问网络、不需要密钥；四个插件在离线 mock 下可装配、可写、可查、可启停；`postgres` 计算链路离线可执行且 attestation 失败拒绝展示；13 语言文案完整、端点表与 `http.go` 一致、单色守卫通过，web 仍可用任意静态服务器直接托管。
