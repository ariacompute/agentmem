# MCP server

agentmem 内置一个 MCP（Model Context Protocol）server。同一份协议内核经两条传输暴露：

| 传输 | 入口 | 身份来源 | 典型客户端 |
|---|---|---|---|
| Streamable HTTP | `POST` / `GET` / `DELETE /mcp` | 现有 `Authorization: Bearer` 中间件 | 远程 Agent、网关、脚本 |
| stdio | `agentmem mcp` | `--token` / `AGENTMEM_MCP_TOKEN`，或 `--subject` + `--scopes` | Claude Desktop、Cursor、IDE 插件 |

两条传输共用同一个 `internal/mcp` 协议内核、同一份工具表和同一个 `service` 用例层，
因此 ACL、Git 提交串行化、错误红化完全一致，不存在旁路。

## 1. 协议版本

| 版本 | 传输语义 |
|---|---|
| `2025-06-18` | 默认协商值；`outputSchema` + `structuredContent` 正式化；有状态会话 |
| `2025-03-26` | 同上，无 `outputSchema` 正式语义；有状态会话 |
| `2024-11-05` | 兼容回退；无会话，`tools/call` 只返回 `content[0].text` |

- `initialize` 回显客户端请求的版本；请求了未知版本时回落到 `2025-06-18`。
- `MCP-Protocol-Version` 请求头若声明了不支持的版本，直接 `400` + `-32600`，不猜测。
- 每条成功结果同时给 `structuredContent`（对象）与 `content[0].text`（同一对象的 JSON 字符串），
  旧客户端只读 `content` 也不会退化。

## 2. Streamable HTTP

### 2.1 会话生命周期

```
POST /mcp  initialize            -> 200 + Mcp-Session-Id
POST /mcp  tools/*               -> 200 (需带 Mcp-Session-Id)
GET  /mcp                        -> 200 text/event-stream (需会话)
DELETE /mcp                      -> 204 (关闭会话与事件流)
```

- `initialize` 成功后响应头 `Mcp-Session-Id` 下发会话；`2024-11-05` 与 stdio 不下发会话。
- 请求头已声明 `2025-03-26` / `2025-06-18` 却没有会话时：`404` + `-32004`；
  未知或已删除的会话同样是 `404` + `-32004`。错误体固定为
  `{"jsonrpc":"2.0","id":<id>,"error":{"code":-32004,"message":"session not found"}}`。
- 未声明版本且未带会话时按 `2024-11-05` 无状态模式处理，保持老客户端可用。
- 会话表是进程内内存：TTL `AGENTMEM_MCP_SESSION_TTL_MS`（默认 30 分钟），
  容量 `AGENTMEM_MCP_MAX_SESSIONS`（默认 128）。超容量不扩容，`initialize` 返回
  `-32004`；过期会话按需清扫，内存有界。

### 2.2 响应形态

- `Accept` 含 `text/event-stream` 时，一条 JSON-RPC 响应以单个 SSE 事件返回：

```
event: message
data: {"jsonrpc":"2.0","id":1,"result":{...}}
```

- 否则返回 `application/json`。
- 无 `id` 的 `notifications/*` 返回 `202` 且无响应体。
- JSON-RPC batch 数组逐条处理，只把带 `id` 的响应聚合成数组；空数组与超过 32 条的
  batch 返回 `400`。
- 请求体上限 4 MiB，超限 `413` + `request body is too large`。

### 2.3 事件流

`GET /mcp` 在校验会话后返回 `text/event-stream`（`Cache-Control: no-store`、
`X-Accel-Buffering: no`），每 15 秒写一个 `: ping` 注释帧，最长 5 分钟后优雅关闭；
`DELETE /mcp` 或客户端断开都会取消该流。服务端的 `WriteTimeout` 只对这一条连接解除，
不会全局放宽。若中间层不支持 `http.ResponseController` 的写 deadline 解除，
`GET /mcp` 返回 `405` + `Allow: POST, DELETE`，而不是悄悄放宽超时。

## 3. stdio

```bash
agentmem mcp                     # 读取 .env，使用显式身份
agentmem mcp --env /etc/agentmem.env --subject alice --scopes knowledge:read
AGENTMEM_MCP_TOKEN=... agentmem mcp --token-env AGENTMEM_MCP_TOKEN
```

- 逐行 JSON-RPC（JSON Lines）：一行一个请求/响应，`stdout` 只写协议帧，
  日志与错误全部走 `stderr`；EOF 正常退出（退出码 0）。
- 单行超过 4 MiB 时回 `-32700` 并继续服务。
- 身份解析顺序：
  1. `--token`，或 `--token-env <NAME>`（默认读取 `AGENTMEM_MCP_TOKEN`）；
  2. 无令牌且 `AGENTMEM_AUTH_ENABLED=false` 时，允许 `--subject` + `--scopes`；
  3. 两者都没有，或鉴权已启用却未给令牌 —— 拒绝启动，退出码 1。
- 令牌只经 `Authenticator.Authenticate` 换算成 `Principal`，主体与 scope 与 HTTP 完全一致；
  原始令牌不进日志、不进配置、不落盘。

## 4. 工具

`tools/list` 的顺序固定；`inputSchema` / `outputSchema` 都是数据，传输层不解释。

### 4.1 只读（13 个，`AGENTMEM_MCP_WRITE` 无关）

| 工具 | 参数 | scope | 对应读路径 |
|---|---|---|---|
| `search_terms` | `query`、`node_types`、`limit` | `knowledge:read` | `GET /v1/graph/search` |
| `get_node` | `concept_id` | `knowledge:read` | `GET /v1/graph/node` |
| `neighbors` | `concept_id`、`depth`、`direction`、`edge_types`、`node_types`、`limit` | `knowledge:read` | `GET /v1/graph/neighbors` |
| `path` | `from`、`to`、`max_depth`、`direction`、`edge_types` | `knowledge:read` | `GET /v1/graph/path` |
| `check_answerable` | `question` | `knowledge:read` | `POST /v1/graph/answerability` |
| `graph_schema` | — | `knowledge:read` | `GET /v1/graph/schema` |
| `graph_stats` | — | `knowledge:read` | `GET /v1/graph` |
| `graph_retrieve` | `query`、`top_k` | `knowledge:read` | `POST /v1/graph/retrieve` |
| `query` | `query`、`top_k` | `knowledge:read` | `POST /v1/query` |
| `list_concepts` | — | `knowledge:read` | `GET /v1/concepts` |
| `read_knowledge` | `concept_id` | `knowledge:read` | `GET /v1/knowledge` |
| `list_decisions` | `concept_id`、`limit` | `audit:read` | `GET /v1/decisions` |
| `list_computations` | `concept_id`、`limit` | `audit:read` | `GET /v1/computations` |

### 4.2 写入（5 个，仅在 `AGENTMEM_MCP_WRITE=true` 时登记）

| 工具 | 参数 | scope | 对应写路径 |
|---|---|---|---|
| `put_knowledge` | `concept_id`、`document` | `knowledge:write` | `POST /v1/knowledge` |
| `put_graph_node` | `concept_id`、`node_type`、`label`、`description`、`properties`、`edges`、`replace_edges` | `graph:write` | `PUT /v1/graph/node` |
| `append_graph_edge` | `concept_id`、`edges` | `graph:write` | `POST /v1/graph/edges` |
| `run_computation` | `concept_id`、`parameters` | `compute:run` | `POST /v1/computations` |
| `rebuild_graph_index` | — | `migrate:run` | `POST /v1/graph/index` |

写入工具的三道闸口：

1. **可见性**：`AGENTMEM_MCP_WRITE=false`（默认）时不出现在 `tools/list`，
   调用其名字一律 `-32602 unknown tool`，与未知工具不可区分；
2. **scope**：开启后仍逐个校验上表的 scope，缺失返回 `-32003 forbidden`，
   且发生在调用 `service` 之前，不会产生任何 Git 提交；
3. **ACL**：进入 `service` 后与 REST/CLI 走同一套行级 ACL 与本体校验。

## 5. 错误码

| 码 | 含义 | HTTP |
|---|---|---|
| `-32700` | JSON 解析失败 / 单行过大 | `400` |
| `-32600` | 非法请求（版本不支持、batch 越界） | `400` |
| `-32601` | 未知方法 / 图已禁用 | `200` |
| `-32602` | 非法参数 / 未知工具 / 写入被拒 | `200` |
| `-32603` | 内部错误 | `200` |
| `-32002` | 不存在或不可见（同一句文案） | `200` |
| `-32003` | 缺 scope | `200` |
| `-32004` | 会话缺失 / 过期 / 容量超限 | `404` / `429` |

所有对外文案都是固定分类字符串；`%T` 等诊断只进服务端日志，不回显正文、SQL、DSN 或令牌。

## 6. 客户端配置

Claude Desktop / Cursor（stdio）：

```json
{
  "mcpServers": {
    "agentmem": {
      "command": "agentmem",
      "args": ["mcp", "--env", "/etc/agentmem.env", "--subject", "alice", "--scopes", "knowledge:read"]
    }
  }
}
```

远程 Streamable HTTP：

```json
{
  "mcpServers": {
    "agentmem": {
      "url": "https://agentmem.example.com/mcp",
      "headers": { "Authorization": "Bearer <knowledge:read token>" }
    }
  }
}
```

## 7. 关闭与排查

- `AGENTMEM_GRAPH_ENABLED=false` 时 `/mcp` 三个方法一律 JSON `404`，与 `/v1/graph*` 一致。
- `AGENTMEM_CONSOLE_ENABLED=false` 只影响控制台页面，不影响 `/mcp`。
- `agentmem mcp` 启动后会在 `stderr` 打印一行 `stdio ready: subject=… scopes=…`，
  令牌只以变量名出现。
- 客户端「工具列表为空」：确认 `AGENTMEM_MCP_WRITE` 是否符合预期（写工具默认不登记），
  以及令牌是否具备 `knowledge:read`（路由级）与工具级 scope。
