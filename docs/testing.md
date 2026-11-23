# 测试与覆盖率

本仓库把「每个包的语句覆盖率 100%」当作可执行门禁，而不是一次性报告。

```bash
make cover           # go test ./... -covermode=atomic + 门禁
make test            # 只跑测试，不产出 profile
```

## 1. 口径

- **按包统计的语句覆盖率**：`go test ./... -count=1 -covermode=atomic -coverprofile=coverage.out`。
  每个测试二进制只插桩自己的包，因此「包 100%」等价于「这个包有充分的包内测试」。
- **不使用 `-coverpkg=./...`**：那会把「被别的包的测试顺带跑到」也算作覆盖，
  掩盖包内测试缺失。典型例子是 `internal/service/mcp_backend.go`：它只在
  `handlers` / `cli` 的测试里被调用，按包统计时必须由 `service` 自己的测试触碰。
- **包集合完整性**：门禁同时对照 `go list ./...`，任何包在 profile 中缺席
  （没有测试文件）即失败，新增包无法静默绕过。
- 覆盖率是**语句**覆盖率（`go tool cover` 的定义），不是分支覆盖率；分支质量靠
  用例本身的行为断言保证，见第 4 节。

## 2. 门禁脚本

`scripts/coverage` 解析 profile 并按包聚合，输出明细与总计：

```bash
go run ./scripts/coverage -profile coverage.out
go run ./scripts/coverage -profile coverage.out -min 99
go run ./scripts/coverage -profile coverage.out -exclude exceptions.txt
```

| 退出码 | 含义 |
|---|---|
| `0` | 所有（未被排除的）包达到阈值 |
| `1` | 有包低于阈值，或有包没有覆盖数据 |
| `2` | 参数、profile 或排除文件不可用 |

排除文件每行形如 `<package-prefix>:<reason>`，空行与 `#` 注释被忽略；条目必须带
理由，且必须命中真实包（过期的排除项同样失败）。**当前仓库不需要任何排除项**，
这是刻意维持的约束：新增排除项必须在同一个提交里说明「为何离线不可测」以及
「由哪条作业覆盖」。

`make cover` 会先跑全量测试再执行门禁；CI 的 `go` job 在 `go test` 之后调用它。

## 3. 数据库层用替身

`internal/meta` 的生产存储是 PostgreSQL + GORM，但离线测试不启动数据库：

- `internal/meta/postgres_mock_test.go` 的 `newMockStore` 用 `sqlmock` +
  `gorm.Open(postgres.New(postgres.Config{Conn: db}))` 构造真实的 GORM 句柄，
  再脚本化 SQL 期望。副作用是 SQL 拼装、事务边界、空集合分支与错误映射全部可测。
- 每个用例结束都断言 `mock.ExpectationsWereMet()`，避免「脚本化但没执行」的假覆盖。
- `TestPostgresIntegration` 由 `AGENTMEM_TEST_PG_DSN` 门控，仍只属于每日
  `postgres-integration` 作业：替身证明代码路径，真实数据库证明 SQL 语义，两者互补。
- `sqlmock` 已是直接依赖（`go.mod`），不引入新的下载或网络需求。

## 4. 用例质量

- 新增用例必须断言**行为**：状态码、固定错误文案、错误码、不落盘、不泄露、
  `mock.ExpectationsWereMet()`。只调用函数凑覆盖率的用例不接受。
- MCP 与 HTTP 的错误路径优先断言「固定分类文案」，因为对外契约就是那几句话。
- 并发与超时用例不依赖固定 `sleep`：用通道、注册状态轮询或可缩短的间隔变量。

## 5. 接缝与死代码

为了覆盖「在当前 API 下不可达」的防御性语句，允许两种手段，且都必须在此登记：

**包级接缝（默认为生产实现）**

| 接缝 | 位置 | 用途 |
|---|---|---|
| `randRead` | `internal/mcp/session.go` | 会话 id 的熵源失败分支 |
| `mcpSSEPingPeriod` / `mcpSSEMaxLifetime` | `internal/handlers/mcp.go` | 事件流心跳与寿命分支（默认 15s / 5min） |
| `scanRow` | `internal/meta/postgres_exec.go` | `(*sql.Rows).Scan` 失败分支 |
| `encodeNode` | `internal/okf/frontmatter.go` | YAML 节点编码失败分支 |
| `newRequest` | `internal/router/systemone.go` | 请求构造失败分支 |
| `parseURL` | `internal/graph/remote.go` | 已被 `netsafe` 校验后的解析失败分支 |
| `nodeFrom` | `internal/graph/writer.go` | 已通过 `SetGraph` 后的转换失败分支 |

既有接缝（`assemble`、`assembleService`、`buildAuth`、`mcpStdin`、`osExit` 等）保持原样。

**删除的死代码**

| 位置 | 原因 |
|---|---|
| `handlers.writeMCPFailure`、`cli.mcpFailurePayload`、`mcp` 协议编解码 | 固定结构体/字符串的 `json.Marshal` 不可能失败 |
| `okf.Marshal` 的 `encoder.Close` 分支 | 写入 `bytes.Buffer` 的 Close 不会失败 |
| `meta.PostgresStore.GraphNeighbors` / `PruneGraphNodes` 的 `json.Marshal([]string)` 分支 | 编码 `[]string` 不可能失败 |
| `meta.shortestPathByLevel` 的重复 parent 检查 | 与 `visited` 同时置位，重复边已由 default 分支处理 |

删除后均由 `go vet`、`go test ./...`、`make e2e` 验证语义未变。

## 6. 分层

| 层次 | 位置 | 关注点 |
|---|---|---|
| 包内单元测试 | 各 `internal/*` | 边界、错误映射、不变量 |
| 数据库替身 | `internal/meta` | SQL 拼装、事务、行扫描 |
| 黑盒验收 | `scripts/e2e.sh` | 真实二进制、真实 HTTP、CLI 与 MCP 传输 |
| 覆盖率门禁 | `scripts/coverage` | 包级 100%、缺包检测、排除策略 |

`make e2e` 的 `G09` 用一份故意不达标的 profile 证明门禁会失败，`G10` 证明
`make cover` 同时接在 `Makefile` 与 CI 上。
