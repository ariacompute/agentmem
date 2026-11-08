# 安全模型

本文描述 agentmem 的身份、授权、出向管控、错误脱敏与生产 fail-closed 口径。配置项的完整清单见 `.env.example`，漏洞披露流程见仓库根目录的 `SECURITY.md`。

## 1. 信任边界

身份只在 Handler 边界建立一次：

```
请求 → handlers.security（panic 恢复 / 安全响应头 / 限流 / Origin / Bearer）
     → auth.Authenticator（SHA-256 摘要 + 常量时间比较）
     → context 注入 Principal
     → Handler 权限判定 → 可信主体字符串 → service / router / meta / store
```

- 请求体和查询串里的 `subject` **一律被忽略**；`/v1/knowledge`、`/v1/query`、`/v1/computations` 的请求体不再接受该字段（未知字段会被严格 JSON 解码拒绝）。
- 下层沿用既有的 `subject string` 签名，不把 `Principal` 穿透到端口定义；权限判定集中在 `internal/handlers`。
- 因此只有**经 Handler 的流量**被视为可信。CLI 是本地可信操作：`AGENTMEM_AUTH_ENABLED=false` 时本地主体取 `AGENTMEM_ACL_DEFAULT_OWNER`，并携带全部权限。

## 2. 权限矩阵

| 路由 | 所需权限 |
|---|---|
| `GET /healthz` | 公开（无需令牌，仅返回脱敏状态） |
| `GET /v1/knowledge` | `knowledge:read` |
| `POST /v1/query` | `knowledge:read` |
| `GET /v1/concepts` | `knowledge:read` |
| `POST /v1/knowledge` | `knowledge:write` |
| `POST /v1/migrate` | `migrate:run` |
| `GET /v1/decisions` | `audit:read` |
| `GET /v1/computations` | `audit:read` |
| `POST /v1/computations` | `compute:run` |

`health:read` 作为权限常量保留，供只读探针令牌使用；`/healthz` 本身始终公开，以便负载均衡器在鉴权配置错误时仍能判定进程存活。

失败口径：

| 状态 | 触发条件 |
|---|---|
| `401` | 缺少 `Authorization` 头、不是 `Bearer <token>` 形式、令牌未知。响应带 `WWW-Authenticate: Bearer realm="agentmem"` |
| `403` | 令牌有效但缺少所需权限；`Origin` 不在允许列表内 |
| `429` | 超出单进程每客户端令牌桶 |

`GET /v1/knowledge` 对「无权限」和「不存在」返回同一个 `404 document not found or access denied`，避免探测私有文档是否存在。

## 3. 令牌：摘要配置与轮换

配置里只出现令牌的 SHA-256 十六进制摘要，原始令牌不进入文件、日志或错误：

```bash
# 生成摘要
printf %s 'your-token' | sha256sum

# .env
AGENTMEM_AUTH_ENABLED=true
AGENTMEM_AUTH_TOKENS='[
  {"token_sha256":"<64 hex>","subject":"console","scopes":["knowledge:read","knowledge:write"]},
  {"token_sha256":"<64 hex>","subject":"nightly","scopes":["migrate:run","audit:read"]}
]'
```

- JSON 严格解析：未知字段、非 64 位十六进制摘要、重复摘要、空 subject、空 scopes、未知权限都会让 `config.Load` 失败。
- 比较是常量时间（`crypto/subtle.ConstantTimeCompare`），逐个比完所有摘要，耗时与命中位置无关。
- **轮换**：向 `AGENTMEM_AUTH_TOKENS` 追加新摘要 → 重启进程 → 客户端切流 → 删除旧摘要 → 再次重启。轮换窗口内新旧令牌并存。当前实现不支持热加载与集中吊销，见第 9 节。

## 4. 出向管控（SSRF）

`internal/netsafe` 在**装配期**一次性校验，运行期只做逐跳复检：

- 仅允许 `http` / `https`；端点不得内嵌凭据（`user:pass@`）或 fragment。
- 主机必须命中 `AGENTMEM_EGRESS_ALLOWED_HOSTS`：裸主机名只匹配自身，`*.example.com` 只匹配子域（不包含 `example.com` 本身）。
- IP 字面量若为回环、链路本地（含 `169.254.169.254` 元数据地址）、私网、未指定或组播地址，**只有在该地址被逐字点名**时才允许——通配条目不能给它授权，本地 sidecar 场景除外。
- 校验期不做 DNS 解析，保证判定确定且离线；域名解析出的私网地址不在这一层拦截（见第 9 节剩余风险）。
- `RedirectPolicy` 限制跳数（默认 3）并对每一跳重新校验目标，禁止 `https → http` 降级；`HTTPBridge` 与 System One 的 `http.Client` 都设置该策略。
- 非空出向端点（System One 或非 `cli` 适配器）而未配置任何主机时，`config.Load` 直接失败。
- `cli` 适配器的 `endpoint` 是可执行程序名，不是 URL，因此不参与主机白名单；它受第 6 节的命令规则约束。

## 5. 限流、超时与来源校验

- 单进程令牌桶：`AGENTMEM_RATE_RPS`（默认 20）/ `AGENTMEM_RATE_BURST`（默认 40），按 `RemoteAddr` 主机计。桶表上限 10000 个键并有定期清理，避免伪造源地址撑爆内存。
- HTTP 服务端超时（`internal/cli/root.go` 常量）：读头 5s、读 15s、写 30s、空闲 60s、`MaxHeaderBytes` 1 MiB；收到 `SIGINT`/`SIGTERM` 后最多 10s 优雅退出。
- 跨站来源：同源请求直接放行；跨源必须命中 `AGENTMEM_ALLOWED_ORIGINS`（精确匹配 scheme+host+port，不接受带路径的条目）。**不返回 CORS 响应头**——跨源控制台需要自行经同源反向代理转发。
- 安全响应头：`X-Content-Type-Options: nosniff`、`X-Frame-Options: DENY`、`Referrer-Policy: no-referrer`、`Cache-Control: no-store`，以及 API 侧的 `Content-Security-Policy: default-src 'none'; frame-ancestors 'none'; base-uri 'none'`。

## 6. 文件与执行面

| 面 | 措施 |
|---|---|
| OKF 路径 | `internal/safepath` 拒绝 NUL、反斜杠、绝对路径、`..` 段、Windows 保留名、冒号、控制字符、尾部空格/点与超长 id；`Resolve` 做 `Clean` + `EvalSymlinks` + root 包含性校验，符号链接逃逸（文件本身或任一父目录）都会失败 |
| 原子写 | `os.CreateTemp`（`0o600`）+ `fsync` + `rename`，写完再提交 Git 路径 |
| 解析资源上限 | frontmatter 文档大小、YAML 节点数/深度、别名展开、目录条目数与总字节数都有上限；**不加 `KnownFields(true)`**，未知键与未知类型仍被接受（OKF 宽松兼容是硬约束） |
| 计算体 | 只允许单条 `SELECT` / `WITH`；分号即拒绝；`pg_read_file`、`pg_ls_dir`、`dblink`、`lo_export`、`pg_sleep`、`set_config` 等副作用函数在词法层被拒；执行走只读事务与参数化绑定，行数上限来自 `AGENTMEM_COMPUTE_MAX_ROWS`（默认 100），整轮耗时由 `AGENTMEM_COMPUTE_TIMEOUT_MS` 的 context 兜住 |
| CLI 桥接 | 命令必须是裸可执行程序名（`^[A-Za-z0-9_][A-Za-z0-9_.-]*$`），永不经过 shell；参数拒绝控制字符与 shell 元字符但允许 UTF-8；外部进程 stderr 不进入错误、日志或响应 |
| 请求体 | `Content-Type: application/json` 必需，`MaxBytesReader` 4 MiB，`DisallowUnknownFields`，拒绝尾随数据；`top_k` 上限 100，`limit` 1–1000（默认 20） |

## 7. 错误脱敏

对外只暴露固定分类文案与稳定状态码：

```json
{"error": "internal error"}
{"error": "write rejected"}
{"error": "computation rejected", "envelope": { "...": "..." }}
```

- 不再有 `err.Error()` 回显；`pgx`/GORM 错误可能携带完整 DSN 与 SQL 参数。
- 内部 `slog`（JSON 到 stderr）只记录 `class` 与错误类型 `%T`，不记录正文、PII、凭据或 SQL 参数。
- 认证/权限失败日志按秒节流并汇总 `suppressed` 计数，避免刷屏式拒绝变成日志 DoS。
- 计算失败时 `envelope` 永不携带 `display_value`，`verdict` 的 `reasons` 只给类别。

## 8. 生产 fail-closed 清单

`AGENTMEM_MODE=production` 时 `config.Load` 强制：

1. `AGENTMEM_AUTH_ENABLED=true` 且至少一枚令牌；
2. `AGENTMEM_PG_DSN` / `AGENTMEM_VECTOR_DSN` 不得包含 `sslmode=disable`；
3. 非空出向端点必须配置 `AGENTMEM_EGRESS_ALLOWED_HOSTS`；
4. 其余校验（adapter kind 白名单、ACL 可见性枚举、限流取值范围、frontmatter 上限）与模式无关，始终生效。

容器与交付面：镜像以非 root 用户运行且仅 `/app/data` 可写，运行时镜像随 Alpine 版本刷新 CA bundle；`docker-compose.yml` 把 `8080` 与 `5432` 绑定在 `127.0.0.1`，容器内仍监听 `0.0.0.0`（容器网络所需）。CI 默认仅 `contents: read`，`persist-credentials: false`；GitHub Release 资产上传用 `github.token`，镜像推送用仓库 secret `ARIACOMPUTE_TOKEN`（只授 `write:packages`，需定期轮换；见 `SECURITY.md` 的剩余风险）。

信任库：Go 1.27.3 内置 `crypto/x509` 的 eMudhra（emSign）弱中间 CA 屏蔽列表。`make check-trust-store` 在离线环境按 2 个根 + 8 个中间证书的 SHA-256 指纹扫描系统/镜像 CA bundle，退出码 `0` 干净、`1` 命中、`2` 无法读取；不做网络探测（正向/拦截代理会产生误判）。

## 9. 威胁模型与剩余风险

| 威胁 | 现状 | 处理 |
|---|---|---|
| 未授权读写 | 网络可达即可访问 | 已修复：Bearer + 权限矩阵；生产强制开启 |
| 请求自报身份（越权读私有文档） | `subject` 来自请求体/查询串 | 已修复：主体仅由中间件注入 |
| 私有文档存在性泄露 | 404 与 403 可区分 | 已修复：统一 404 文案 |
| 令牌泄露 | 摘要写在版本化 `.env` | 缓解：只存摘要；`.env` 被 gitignore；建议按环境注入 `AGENTMEM_AUTH_TOKENS` |
| 内部错误/DSN 泄露 | 直接回显 `err.Error()` | 已修复：分类文案 + 类型日志 |
| SSRF（System One / HTTP 桥接） | 任意 URL | 已修复：装配期白名单 + 逐跳复检 + 私网字面量默认拒绝 |
| DNS 重绑定 / 白名单域名解析到私网 | 校验期不解析 DNS | **部分覆盖**：白名单本身即信任边界，运维只能登记可信域名 |
| 路径穿越 / 符号链接逃逸 | 直接 `filepath.Join` | 已修复：`safepath` 双侧解析 |
| 资源耗尽（解析、行数、连接） | 无上限 | 已修复：大小/节点/深度/行数/超时上限 |
| 计算体内危险函数 | 仅关键字黑名单 | 已修复：补充文件、网络、会话与导出类函数 |
| 单点暴力破解 | 无限制 | 缓解：每客户端令牌桶 + 日志节流 |
| 多实例限流绕过 | 单进程内存桶 | **未覆盖**：需前置网关或服务网格限流 |
| 令牌热轮换/集中吊销 | `AGENTMEM_AUTH_TOKENS` 静态 | **未覆盖**：改配置后重启；多租户建议前置 IdP |
| 供应链钉版本 | actions 仍为浮动 tag | 部分覆盖：最小权限 + dependabot；完整 commit SHA 需先核验 |
| 基础镜像漂移 | 浮动 tag | 部分覆盖：固定 Alpine 主版本并每次构建刷新 CA；digest 钉住待补 |

## 10. 验收

```bash
go test ./... -count=1                 # 全离线，含 auth/safepath/netsafe 与鉴权矩阵
go vet ./...
go build -o bin/agentmem ./cmd/agentmem
go run ./cmd/agentmem validate --env .env.example
make check-boundaries
make check-trust-store
cd web && npm test && npm run build     # 端点表镜像、13 语言译文、CSP 与单色守卫
```

`internal/handlers/http_test.go` 覆盖匿名写、越权、伪造 `subject`、401/403、错误脱敏、限流、Origin、严格 JSON 与参数边界；`internal/okf`、`internal/vcs`、`internal/bridges`、`internal/compute`、`internal/meta` 各自带有对应的离线回归用例。
