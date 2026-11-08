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
- [x] `web/tests/` 零依赖 `node:test` 套件（语言、译文兜底、端点漂移、单色守卫、主题、DOM 冒烟；CSP 守卫见阶段 11）；`web/package.json` 的 `bun test` 与 CI web job 接入。
- [x] `README.md` / `README_cn.md` Web 章节同步。

## 阶段 10 · eMudhra（emSign）信任库响应

- [x] `scripts/check-emudhra.sh`：离线扫描 CA bundle 的 2 个根 + 8 个中间证书 SHA-256 指纹，退出码 `0` 干净 / `1` 命中 / `2` 无法读取，零网络调用（与 `mcpx` 同一指纹表）。
- [x] `make check-trust-store` 目标并入 `.PHONY` 与 CI；`SECURITY.md` 记录镜像 CA 导出后在宿主扫描的口径，不把 `openssl` 装进运行时镜像。
- [x] `Dockerfile` / `docker-compose.yml`：构建与运行阶段刷新 `ca-certificates`，运行时升到 `alpine:3.21` 非 root 用户。

## 阶段 11 · 全面安全加固

- [x] H1 `internal/auth` + `handlers/security.go`：Bearer → 摘要常量时间比较 → `Principal` 注入 `context`；9 条路由的权限矩阵集中在 `routePermissions`。
- [x] H2 主体只由中间件产生；`/v1/knowledge`、`/v1/query`、`/v1/computations` 移除请求体 `subject`，严格 JSON 解码拒绝该字段。
- [x] H3 `store.TrustedACL` / `AuthorizeUpdate` / `aclFromVisibility`：私有文档按 owner 强隔离，匿名写入拒绝，读无权限与不存在统一 `404`。
- [x] H4 错误脱敏：客户端只见分类文案，日志只记 `class` + `%T`；移除全部 `err.Error()` 回显（含 `/healthz` 与计算审计）。
- [x] H5 配置闭白名单覆盖进程环境 `AGENTMEM_*`；生产强制鉴权 + 令牌 + 拒绝 `sslmode=disable` + 出向端点必配白名单；删除死开关 `AGENTMEM_SYSTEMONE_FAIL_CLOSED`。
- [x] H6 `internal/netsafe`：装配期端点校验 + `RedirectPolicy` 逐跳复检 + 禁止 TLS 降级；System One 与 HTTP 桥接同时接入。
- [x] H7 `internal/safepath`：`okf` 全部读写删与 `vcs` 提交路径统一校验，符号链接逃逸（文件或父目录）拒绝；原子写改 `CreateTemp` + `0o600` + `fsync` + `rename`。
- [x] H8 计算面：危险函数黑名单扩充（文件/网络/会话/大对象），块注释内占位符不绑定，整轮共用超时 context，审计失败不回显。
- [x] H9 CLI 桥接：裸可执行程序名白名单、UTF-8 参数放行、stderr 不进错误；`engines.yaml` 改严格解析。
- [x] H10 服务超时与优雅退出（读头 5s / 读 15s / 写 30s / 空闲 60s / 头 1 MiB）；compose 端口改 `127.0.0.1` 回环，容器内仍 `0.0.0.0`。
- [x] H11 CI/release 最小 `permissions`、`persist-credentials: false`、`image` job 使用 `ARIACOMPUTE_TOKEN`（PAT，仅 `write:packages`）、`go mod verify`、dependabot。
- [x] M1 每客户端令牌桶限流（默认 20 rps / burst 40，桶表上限 10000）+ 认证失败日志按秒节流。
- [x] M2 `AGENTMEM_ALLOWED_ORIGINS` 精确匹配（同源直接放行，不返回 CORS 头）。
- [x] M3 安全响应头：`nosniff`、`DENY`、`no-referrer`、`no-store`、API 侧 CSP。
- [x] M4 严格 JSON（`Content-Type` 校验、4 MiB、`DisallowUnknownFields`、拒绝尾随数据）与参数边界（`top_k` ≤ 100、`limit` 1–1000 默认 20）。
- [x] M5 OKF 解析资源上限（文档 8 MiB、frontmatter 1 MiB、YAML 节点 10000/深度 32/别名 32、条目 100000、资源 1 MiB），不加 `KnownFields(true)`。
- [x] M6 规范化缓存键长度上限；`audit:read` 覆盖 `/v1/decisions` 与 `/v1/computations`。
- [x] M7 Web：`api-content.js` 同步 Bearer/scopes/限流行与 401/403/429 状态，13 语言补真实译文，移除请求样例的 `subject`，新增 CSP meta（inline 主题脚本 SHA-256 白名单）与 `web/tests/security.test.mjs`。
- [x] L1 `docs/security.md`、`SECURITY.md`、`AGENTS.md`、`README*`、`requirements.md`、`releasenotes*` 同步留痕。

## 阶段 12 · Web 渲染检查（impeccable）

- [x] `web/package.json`：新增 `audit`（源码级 `impeccable detect src index.html`）与 `audit:live`（运行态 URL `http://localhost:5173`）脚本。
- [x] `web/.impeccable/config.json`：共享配置（关闭遥测）；`.gitignore` 排除临时缓存、保留该配置。
- [x] `Makefile`：新增 `web-audit` 目标（`cd web && bunx impeccable@latest detect src index.html`）并并入 `.PHONY`。
- [x] `web/src/style.css`：移除 `.notice` 的 3px 侧边强调边框（impeccable 标记的「AI 痕迹」），改为细实线边框 + 极淡底色；复跑 `impeccable detect` 收敛至 0 反模式。
- [x] `web/src/style.css`：提升三级文字 `--text-3` 透明度（暗色 `0.44 → 0.55`、亮色 `0.52 → 0.6`），使三级文字在 `#050505` / `#f5f5f5` 上满足 WCAG AA 对比度。
- [x] `AGENTS.md` / `README.md` / `README_cn.md` / `requirements.md`：补充 impeccable 渲染检查工作流与 `make web-audit` 说明。

## 验收

```bash
go test ./... -count=1
go vet ./...
go build -o bin/agentmem ./cmd/agentmem
go run ./cmd/agentmem validate --env .env.example
go run ./cmd/agentmem eval --dataset testdata/eval.jsonl --k 5
make check-boundaries
make check-trust-store
make web-audit
cd web && bun test
cd web && bun run build
```

验收条件：以上命令全部成功；不启动 Postgres、不访问网络、不需要密钥；四个插件在离线 mock 下可装配、可写、可查、可启停；`postgres` 计算链路离线可执行且 attestation 失败拒绝展示；鉴权矩阵、越权、伪造主体、错误脱敏与限流均有离线用例；系统/镜像 CA bundle 不含 eMudhra 弱证书指纹；13 语言文案完整、端点表与 `http.go` 一致（含 401/403/429）、CSP 哈希自洽、单色守卫通过，web 仍可用任意静态服务器直接托管。
