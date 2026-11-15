# 计算面 sidecar 线协议 v1

`bigquery` / `dbt` / `python` / `looker` 四个 runtime 不在本进程执行：Go 侧只负责加载受批准正文、绑定参数、校验回执与审计。真正的执行由 **出向 sidecar** 完成，通过 `AGENTMEM_COMPUTE_SIDECARS` 声明：

```bash
AGENTMEM_COMPUTE_SIDECARS='[
  {"runtime":"bigquery","bridge":"http","endpoint":"https://bq-sidecar.internal","timeout_ms":8000},
  {"runtime":"python","bridge":"cli","command":"agentmem-py","timeout_ms":8000}
]'
```

- `runtime` 只能取 `bigquery` / `dbt` / `looker` / `python`；`postgres` 永远进程内执行，声明为 sidecar 会被拒绝。
- `bridge` 只能取 `http` 或 `cli`。`http` 必填 `endpoint`，且必须命中 `AGENTMEM_EGRESS_ALLOWED_HOSTS`；`cli` 必填 `command`，只能是裸可执行程序名（`^[A-Za-z0-9_][A-Za-z0-9_.-]*$`），永不经过 shell。
- 同一 runtime 只能声明一次；未声明的 runtime 保持 fail-closed，`POST /v1/computations` 返回 `422`、CLI 返回「no executor is configured」。

## 请求与响应

| 通道 | 传输 |
|---|---|
| `http` | `POST {endpoint}/run`，请求体与响应体均为 JSON（`Content-Type: application/json`） |
| `cli` | 执行 `{command} run`，请求 JSON 写入 stdin，响应 JSON 从 stdout 读取 |

请求：

```json
{
  "runtime": "python",
  "concept_id": "computations/revenue-python",
  "contract_hash": "<sha256 hex>",
  "body_kind": "script",
  "body": "import json\nprint(json.dumps({\"revenue\": 1250}))\n",
  "parameters": {"year": 2026}
}
```

响应（只允许这三个字段，未知字段按严格 JSON 解码拒绝）：

```json
{
  "columns": ["revenue"],
  "rows": [[1250]],
  "body_digest": "<sha256(body) 的十六进制>"
}
```

## 不可转让的凭据

- `body_digest` 必须是 sidecar **实际执行**的正文的 SHA-256。Go 侧用自家正文重算并比对，不符即判定 provenance 失败：`display_allowed=false`、`display_value` 为空，审计仍照常写入。
- `job_id`、`result`、`display_value` 一律由 Go 侧从 `rows` 重算，sidecar 提供这些字段不会生效（严格解码会直接拒绝未知字段）。
- `rows` 的单元格只接受 `string` / 整数 / 浮点 / `bool` / `null`；嵌套对象或数组直接判为执行失败，避免出现无法复算的展示值。

## 边界与预算

| 项 | 值 |
|---|---|
| 请求体上限 | 1 MiB（含正文与参数） |
| 响应体上限 | 4 MiB，超出即失败而非截断 |
| 单次调用超时 | `timeout_ms`（默认 5000），且不会超过 `AGENTMEM_COMPUTE_TIMEOUT_MS` 的整轮 deadline |
| 行数 | 超过 `AGENTMEM_COMPUTE_MAX_ROWS`（默认 100）截断 |
| 重试 | 仅 `http` 通道的传输错误与 `5xx` 重试 1 次；`4xx` 与 CLI 不重试 |
| 出向校验 | 每次调用前复核 endpoint；重定向逐跳复检（最多 3 跳）并禁止 TLS 降级 |

## 错误分类（对外）

| 场景 | 错误类别 | HTTP |
|---|---|---|
| 未配置 sidecar | `unsupported` | `422` |
| 端点不在白名单 / 非 2xx / 载荷非法 / 行超限 / 进程失败 | `execution` | `502` |
| `body_digest` 不符 | `attestation_failed`（判定被拒绝，`display_allowed=false`） | `422` |
| 参数或契约错误 | `invalid_params` / `contract` | `400` |

对外文案固定，绝不包含 URL、主机名、外部 stderr、脚本正文或参数值；内部日志只记录 `class` 与错误类型 `%T`。

## 正文形态

`body_kind` 由 runtime 推导，sidecar 只把它当作数据处理：

| runtime | `body_kind` | 正文来源 |
|---|---|---|
| `postgres` / `bigquery` | `sql` | ```` ```sql ```` 内联块或 `.sql` 资源；只允许单条 `SELECT` / `WITH` |
| `python` | `script` | ```` ```python ```` 内联块或 `.py` 资源 |
| `dbt` | `model` | ```` ```yaml ```` 内联块或 `.yaml` / `.yml` 资源（单个文档） |
| `looker` | `query` | ```` ```yaml ```` / ```` ```json ```` 内联块或 `.yaml` / `.yml` / `.json` 资源 |

非 SQL 正文由 sidecar 解释，因此 sidecar **本身就是执行沙箱边界**：以最小权限运行、只挂载读取数据所需凭据、不把正文当命令或 shell 片段执行。签名与信任边界见 `SECURITY.md` 与 `docs/security.md`。

## 离线验证

`go test ./internal/compute` 用 `httptest` 假服务覆盖 HTTP 通道、用注入的 `bridges.Runner` 覆盖 CLI 通道；`scripts/e2e.sh` 用本地 `python3` 桩（`agentmem-py` 与 `http_sidecar.py`）端到端验证真实二进制。两者都不触网、不需要凭据。
