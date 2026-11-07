# Attested Computation

Attested Computation 把「受批准的计算」与「Agent 的自由发挥」分离：计算体由 OKF+Git 保存，Agent 只能填声明过的参数值；执行结果必须经确定性 Go attester 验证后才允许展示。

## 完整链路

```
POST /v1/computations 或 agentmem compute
  -> ContractLoader  : 从 OKF 文档读 runtime/parameters/computation/executor/attester
  -> ParameterBinder : 类型校验、必填校验、拒绝未声明参数与计算体改写
  -> RuntimeRegistry : 五 runtime 契约，只有 postgres 有 executor
  -> SQLExecutor     : 只读事务、参数化绑定、行数上限、超时
  -> Receipt         : job_id / executed_sql / result / rows
  -> Attester        : SQL 规范化 provenance + 展示值 fidelity（确定性 Go，无 LLM）
  -> Gate            : 失败则不返回 display_value
  -> Audit           : computation_runs（成功、拒绝、错误都留痕）
```

## 契约来源

`internal/okf/attested.go` 负责声明；正文来源有两种：

| `computation` 值 | 来源 |
|---|---|
| `references/revenue.sql` 等 `.sql` 相对路径 | `Bundle.ReadResource` 读取 bundle 内资源（路径穿越/绝对路径/符号链接逃逸均被拒，1 MiB 上限） |
| `inline` | 文档正文首个 ```sql 围栏代码块 |

OKF 解析仍然宽松：未知键、未知 runtime、缺失 executor/attester 都不会让 bundle 被拒绝。严格校验发生在 `Attested.Validate()`，即**执行前**，不是解析时。

## 参数绑定

- 参数类型：`integer` / `number` / `string` / `boolean` / `date`。
- Agent 只能提供声明的参数名。未声明名称返回 `ErrInvalidParams`；提供 `computation` / `sql` / `statement` / `runtime` / `attester` / `executor` / `contract` / `body` 返回 `ErrComputationRewrite`。
- `:name` 占位符在引号与注释之外替换为 `$N`，`Args` 按参数声明顺序对齐；**从不拼接值到 SQL 文本**。
- `params_digest` 是排序后参数值的 SHA-256 摘要，用于 receipt 与审计关联，不存明文值。
- `SafeStatement` 拒绝多语句、非 `SELECT`/`WITH` 开头、以及 INSERT/UPDATE/DELETE/DROP/INTO/SET 等变更或会话关键字。

## Runtime 矩阵

| Runtime | 状态 | 行为 |
|---|---|---|
| `postgres` | 可执行 | 通过 `meta.SQLExecutor` 端口执行；生产为 `internal/meta/postgres_exec.go` 的 GORM 只读事务，离线为 `meta.FakeStore` / `compute.FakeExecutor` 的确定性结果 |
| `bigquery` | 契约骨架 | `ErrUnsupported` |
| `dbt` | 契约骨架 | `ErrUnsupported` |
| `python` | 契约骨架 | `ErrUnsupported` |
| `looker` | 契约骨架 | `ErrUnsupported` |

`errors.Is(err, compute.ErrUnsupported)` 可判定。未知 runtime 不会静默落到其他执行器。

## Receipt 与 Attester

`Receipt` 字段：`JobID`、`Runtime`、`ContractHash`、`ParamsDigest`、`ExecutedSQL`（保留 `$N` 占位符，不含明文值）、`Result`、`DisplayValue`、`Rows`、`RowCount`。

`Attester` 是纯 Go 判定，无 LLM、无网络：

- **Provenance**：`contract_hash`、`params_digest`、`runtime`、`job_id` 一致，且 `CanonicalSQL(receipt.ExecutedSQL) == CanonicalSQL(bound.Statement)`。`CanonicalSQL` 去注释、折叠空白、统一关键字大小写、命名占位符归一，但保留 `$N` 顺序，因此参数重排不能冒充同一语句。
- **Fidelity**：从 `receipt.rows` 重新派生规范结果，要求 `receipt.result`、`receipt.display_value` 与派生值完全一致，且行数一致。
- **Staleness**：`stale_after` 过期即 fail closed（拒绝展示并写入审计）。文档级 `verified`（人类确认）与运行级 attestation 正交，互不替代。

任一检查失败返回 `ErrAttestationFailed`；响应 `display_allowed=false`、`display_value` 缺失、`verdict.reasons` 给出原因。

## HTTP

`POST /v1/computations`

```json
{"concept_id":"computations/revenue-postgres","subject":"analyst:1","parameters":{"year":2026}}
```

成功 `200`：

```json
{
  "concept_id": "computations/revenue-postgres",
  "runtime": "postgres",
  "display_allowed": true,
  "display_value": "2026607",
  "verified": true,
  "verdict": {"passed": true, "provenance": true, "fidelity": true, "stale": false, "reasons": []}
}
```

- attestation 失败：`422` + 同一 envelope（`display_allowed=false`，无 `display_value`）。
- 参数/契约错误：`400` + `{"error": ..., "envelope": ...}`；`Unsupported`：`422`；执行器故障：`502`。错误 envelope 同样不含可展示值。

`GET /v1/computations?concept_id=&limit=` 返回 `computation_runs` 审计（最新在前）。

## CLI

```bash
go run ./cmd/agentmem compute \
  --id computations/revenue-postgres --subject analyst:1 --param year=2026
go run ./cmd/agentmem computations --id computations/revenue-postgres --limit 20
```

`--param` 可重复，格式 `name=value`；值以字符串传入并由 binder 按声明类型强制转换。

## 配置与审计

```yaml
compute:
  timeout_ms: 5000   # 0 使用默认 5000
  max_rows: 100      # 0 使用默认 100
```

`003_computations.sql` 建立 `computation_runs` 表（含 `(job_id, verdict)` 唯一键实现幂等），迁移由 `schema_migrations` + advisory lock 管理。审计记录只保存 `concept_id / subject / runtime / contract_hash / params_digest / job_id / verdict / 布尔判定 / reasons / error_class / created_at`：**不保存 SQL 文本、参数值、结果值或知识正文**。离线 fake 的快照 `data/meta.fake.json` 同步包含该审计列表，CLI 跨进程可读。

日志只输出类别与摘要，不输出正文、凭据、PII 或 GORM SQL 参数；`postgres_exec.go` 在计算会话上将 GORM logger 设为 `Silent`。

## 离线验收

`go test ./internal/compute/... ./internal/meta/... ./internal/handlers/... ./internal/cli/...` 在无 Postgres、无网络、无凭据下覆盖：参数篡改、SQL 伪造、展示值篡改、job id 伪造、过期契约、四 runtime `Unsupported`、成功链路、失败留痕、HTTP 与 CLI 入口、快照往返。
