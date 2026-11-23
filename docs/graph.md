# 知识图谱与本体

知识概念既是一篇文档，也是图中的一个节点；概念之间由 frontmatter 声明的关系构成有类型的边。
图的事实只写在 OKF+Git 正文里（可 diff、可回滚、可 review），PostgreSQL 只保存**可丢弃、可重建**的邻接索引。
任何时刻删库重跑一次 `graph reindex`，图就与 Git 完全一致。

## 1. 不变量

- **Git 是唯一权威。** 节点属性、边与本体都来自正文的 frontmatter；索引行只是投影。
- **索引永远可重建。** `graph_nodes` / `graph_edges` 不承载任何正文里不存在的信息；冲突时以正文为准。
- **双门不变。** 图读路径先过 `Meta.CheckACL` 再计数与返回；图写路径仍走 `store.OKFStore.Put`，不新增绕过 ACL 的入口。
- **宽松优先。** 只有「已声明的关系类型把两个类型连错」是硬拒绝；未知类型、未知节点类型、目标缺失都只告警，与 OKF 「仅 `type` 必填」的 conformance 口径一致。
- **不静默降级。** 外部图引擎配置不完整、白名单未放行或调用失败时返回错误，绝不悄悄退回内建实现。

## 2. 声明契约

节点文档（`type` 同时是默认节点类型，与 auto-ontology 的 Concept/Term 对齐）：

```yaml
---
type: metric
title: Gross Margin
graph:
  node:
    label: Gross Margin
    properties:
      unit: percent
  edges:
    - { to: metrics/revenue, type: derived_from }
    - { to: tables/fact_sales, type: computed_from, evidence: "sources[0]" }
---
```

- `graph.node.type` 可覆盖文档 `type`：同一篇正文可以用另一个本体类型出现，不必复制文件。
- `graph.node.label` 缺省取 `title`，再缺省取 id 最后一段。
- `graph.node.properties` 为扁平字符串映射，原样进入索引与 `GET /v1/graph/node`。
- `graph.edges[].to` 只接受同一 bundle 内的概念 id。URL、`#anchor`、绝对路径属于血缘而不是图目标，会被忽略。
- 除显式声明外，还有三类**派生边**，`origin` 字段标明来源，读取方永远能区分「写下的断言」与「机械推导」：

| `origin` | 边类型 | 来源 |
|---|---|---|
| `source` | `sourced_from` | `sources[].resource` |
| `resource` | `references` | 文档的 `resource` |
| `link` | `links_to` | 正文中的 markdown 概念链接 |
| `declared` | 任意 | `graph.edges[]` |

本体文档（bundle 内约定 id 为 `ontology`，但**任何** `type: ontology-schema` 的文档都参与合并，重复类型冲突记为告警）：

```yaml
---
type: ontology-schema
graph_schema:
  node_types:
    metric: {}
    table: {}
    concept: {}
  edge_types:
    derived_from:  { from: [metric], to: [metric] }
    computed_from: { from: [metric], to: [table] }
---
```

`from` / `to` 为空表示该端不约束。多个本体文档合并时按类型名做了确定性排序，同样定义只保留一份。

## 3. 校验语义

| 情形 | 结果 |
|---|---|
| 已声明关系类型，`from` 不含当前节点类型 | `422`（`graph request rejected`） |
| 已声明关系类型，`to` 不含目标节点类型 | `422` |
| 关系类型未在本体声明 | 告警 `unknown_edge_type: <type>` |
| 节点类型未在本体声明 | 告警 `unknown_node_type: <type>` |
| 目标概念不存在 | 告警 `dangling_edge: <type> -> <id>` |
| 没有本体文档 | 不校验，只投影 |

预算（超限即 `422`，不截断）：

| 项 | 上限 |
|---|---|
| 单文档边数 | 64 |
| 节点属性键数 / 单值字节 | 32 / 256 |
| `evidence` 字节 | 1024 |
| 概念 id 与类型名 | 128 字节 |
| 本体声明的节点类型 / 关系类型 | 各 256 |
| 请求体 | 4 MiB（与全部 API 一致） |

## 4. 投影与陈旧判定

1. `PUT /v1/graph/node`、`POST /v1/graph/edges` 与 CLI `graph put` / `graph edge` 把声明写进 frontmatter，经 `store.PutAs` 生成 `content_hash` + `commit_sha` 提交 Git，之后才同步索引。
2. 索引同步失败**不回滚正文**：响应里返回告警，投影状态由下一次 `graph reindex` 或读路径的惰性重建补偿。
3. 读路径先比较 `graph_nodes.content_hash` 与 `concepts_meta.content_hash`。不一致（另一个进程刚写入、另一实例刚提交）时，只用这一篇正文重建该节点与其出边，然后返回。
4. 全量重建按 `content_hash` 跳过未变更文档，批量 `ON CONFLICT` upsert 单事务提交，最后 `PruneMissing` 删除 Git 里已不存在的节点（其边随外键语义一并清理）。

遍历预算由 `AGENTMEM_GRAPH_MAX_NODES`（默认 100000，1–1000000）与 `AGENTMEM_GRAPH_MAX_EDGES`（默认 500000，1–5000000）夹紧：递归 CTE 在数据库侧就带上行数上限，超出的结果被截断并置 `truncated: true`，而不是把整张图拉进内存。

## 5. 索引表

`internal/meta/migrations/004_graph.sql`（幂等，每次启动重放）：

| 表 | 主键 | 说明 |
|---|---|---|
| `graph_nodes` | `concept_id` | 类型、标题、描述、可见性、owner、状态、信任层级、属性 JSONB、来源计数、`content_hash`、`commit_sha` |
| `graph_edges` | `(from_id, to_id, edge_type)` | `origin`、`evidence`、属性 JSONB、`content_hash` |

索引里没有正文、没有私有字段以外的凭据，也没有跨概念的聚合状态；`properties` 只承载作者声明的键值。

## 6. HTTP API

读接口用 `knowledge:read`；写接口用 `graph:write`；重建属投影重建，用 `migrate:run`。

| 方法 | 路径 | 权限 | 说明 |
|---|---|---|---|
| GET | `/v1/graph` | `knowledge:read` | 规模、按类型计数、陈旧数量 |
| GET | `/v1/graph/node?id=` | `knowledge:read` | 节点详情：血缘、属性、计算摘要、出边/入边、告警 |
| PUT | `/v1/graph/node` | `graph:write` | 新建或更新节点（可 `replace_edges` 整组替换） |
| GET | `/v1/graph/neighbors?from=&depth=&direction=&edge_types=&node_types=&limit=` | `knowledge:read` | 有界邻域展开（递归 CTE，深度与行数硬上限） |
| GET | `/v1/graph/path?from=&to=&max_depth=&direction=&edge_types=` | `knowledge:read` | 两节点最短可读路径 |
| GET | `/v1/graph/search?q=&node_types=&limit=` | `knowledge:read` | 关键词检索节点 |
| GET | `/v1/graph/schema` | `knowledge:read` | 合并后的类型化本体（含合并告警） |
| POST | `/v1/graph/answerability` | `knowledge:read` | 覆盖率、结论与缺口 |
| POST | `/v1/graph/retrieve` | `knowledge:read` | 文本 + 图邻域 RRF 融合检索 |
| POST | `/v1/graph/index` | `migrate:run` | 从 Git 全量重建投影 |
| POST | `/v1/graph/edges` | `graph:write` | 追加声明关系 |
| POST | `/mcp` | `knowledge:read` | MCP JSON-RPC 传输（工具级再校验 scope） |
| GET | `/mcp` | `knowledge:read` | MCP 事件流（需 `Mcp-Session-Id`） |
| DELETE | `/mcp` | `knowledge:read` | 关闭 MCP 会话（`204`） |

`GET /v1/graph/schema` 的响应与其余端点一样使用 snake_case 键（本体文档本身仍是 YAML frontmatter）：

```json
{
  "node_types": { "metric": { "description": "a measure" } },
  "edge_types": { "derived_from": { "from": ["metric"], "to": ["metric"] } },
  "warnings": []
}
```

约定：

- `depth` / `max_depth` 由 `AGENTMEM_GRAPH_MAX_DEPTH`（默认 3，合法区间 1–8）夹紧；`limit` 上限 200（服务端常量 `graphMaxLimit`），越界一律 `400`。
- 私有节点与不存在节点返回同一个 `404`（`node not found or access denied`），邻居与路径结果先过滤再计数，规模差异不泄露私有节点存在性。
- 错误体只有固定分类文案：`not found` / `node not found or access denied` / `graph request rejected` / `internal error`。
- `AGENTMEM_GRAPH_ENABLED=false` 时 `/v1/graph*` 与 `/mcp` 一律 JSON `404`，其余路由与鉴权顺序不变。

## 7. CLI

```bash
agentmem graph stats
agentmem graph node --id metrics/gross_margin
agentmem graph neighbors --from metrics/gross_margin --depth 2 --direction both
agentmem graph path --from metrics/gross_margin --to tables/fact_sales
agentmem graph search --q margin --node-types metric
agentmem graph answerable --q "what is gross margin"
agentmem graph retrieve --q "gross margin" --top-k 5
agentmem graph put --id metrics/gross_margin --node-type metric --label "Gross Margin"
agentmem graph edge --id metrics/gross_margin --to tables/fact_sales --type computed_from
agentmem graph reindex
```

CLI 走本地可信路径（`AGENTMEM_AUTH_ENABLED=false` 时用 `AGENTMEM_ACL_DEFAULT_OWNER` 作为本地主体），但写入仍受存储层 ACL 与本体校验约束，不因为「本地」而放松。

## 8. MCP

`/mcp` 是 MCP server 的 Streamable HTTP 传输，支持 `initialize` / `ping` /
`tools/list` / `tools/call`，`notifications/*` 且不带 `id` 的通知返回 `202`，
请求体超过 4 MiB 返回 `413`；`GET /mcp` 为会话打开事件流，`DELETE /mcp` 关闭会话。
完整协议（版本协商、会话、错误码、stdio 传输、客户端配置）见 `docs/mcp.md`。

图相关工具（全部 `knowledge:read`）：

| 工具 | 参数 | 对应读路径 |
|---|---|---|
| `search_terms` | `query`、`node_types`、`limit` | `GET /v1/graph/search` |
| `get_node` | `concept_id` | `GET /v1/graph/node` |
| `neighbors` | `concept_id`、`depth`、`direction`、`edge_types`、`node_types`、`limit` | `GET /v1/graph/neighbors` |
| `path` | `from`、`to`、`max_depth`、`direction`、`edge_types` | `GET /v1/graph/path` |
| `check_answerable` | `question` | `POST /v1/graph/answerability` |
| `graph_schema` | — | `GET /v1/graph/schema` |
| `graph_stats` | — | `GET /v1/graph` |
| `graph_retrieve` | `query`、`top_k` | `POST /v1/graph/retrieve` |

写工具默认不登记：`AGENTMEM_MCP_WRITE=true` 才出现
`put_knowledge` / `put_graph_node` / `append_graph_edge` / `run_computation` /
`rebuild_graph_index`，并逐个校验 `knowledge:write` / `graph:write` /
`compute:run` / `migrate:run`。关闭时调用写工具名按未知工具（`-32602`）处理，
与 REST/CLI 共用同一 `service` 与 ACL，没有旁路。

## 9. 检索融合与可回答性

- **RRF**：`graph.retrieve` 把关键词命中与图邻域命中按 \\(\\sum 1/(k+rank)\\) 融合，`k` 由 `AGENTMEM_GRAPH_RRF_K`（默认 60）控制；同分按 id 排序，因此结果可复现。选用 RRF 而不是分数归一化，是因为两路排序器的分数量纲不可比。
- **覆盖率**：问题先按非字母数字边界切词（拉丁词整词保留，CJK 连续串整体保留，不引入分词器），丢弃单字符与停用词，最多保留 8 个词；每个词解析到最优节点，然后

  \\[\\text{coverage} = 0.6\\times\\text{词覆盖} + 0.15\\times\\text{已认证比例} + 0.15\\times\\text{有来源比例} + 0.1\\times\\text{未过期比例}\\]

  分母是命中节点数；词覆盖是「解析到节点的词数 / 总词数」。命中节点为空时覆盖率直接归零。
- **结论三档**：`answerable`（≥ `AGENTMEM_GRAPH_COVERAGE_MIN`，默认 0.6）、`partial`（≥ 该阈值的 0.6 倍）、`insufficient`。即使覆盖率达标，只要命中集合里**没有任何已认证或任何有来源的节点**，也降级为 `partial`。
- **缺口七类**：`missing_term` / `dangling_edge` / `stale` / `unverified` / `no_source` / `unknown_edge_type` / `empty_index`，最多返回 50 条（按类型与 id 排序）。证据不足时返回缺口清单，**不生成答案**；缺口的修复动作是补文档、补来源或重建索引。
- 整条链路不起 LLM、不做 embedding，因此离线、确定、可断言。

## 10. 外部图引擎（可选）

```dotenv
AGENTMEM_GRAPH_BACKEND=remote
AGENTMEM_GRAPH_REMOTE_URL=https://graph.internal
AGENTMEM_GRAPH_REMOTE_TOKEN_ENV=GRAPH_ENGINE_TOKEN
AGENTMEM_GRAPH_REMOTE_TIMEOUT_MS=5000
AGENTMEM_EGRESS_ALLOWED_HOSTS=graph.internal
```

- `builtin`（默认）用本仓库实现；`remote` 只把**只读查询**转发给外部引擎，此时写入与重建会被拒绝（`graph writes require the builtin backend`），因为外部引擎的副本无法随写入更新——静默分叉比拒绝更糟。端点只写基址，请求路径由客户端补 `/v1/graph/*`。
- URL 主机必须在 `AGENTMEM_EGRESS_ALLOWED_HOSTS` 内；凭据只以**环境变量名**引用（`REMOTE_TOKEN_ENV`），启动时读取，不写入配置、不进日志、不出现在错误体里。
- `remote` 缺 URL、缺令牌、主机未放行或 `production` 模式下配置不完整 → **启动失败**，而不是静默退回 `builtin`。
- 外部响应超时、非 2xx 或结构不符 → 返回固定错误类别，不外泄响应正文。

## 11. 控制台图视图

`?view=graph`（顶栏第三个 tab）是纯黑白灰的结构化视图，不做力导向画布：

- 查询与索引状态条：关键词、节点类型、索引新鲜度徽标（`fresh` / `stale` / `rebuilding`）、重建索引（写入类，需二次确认）。
- 结果列表与节点详情同屏：宽屏双栏（列表 380px + 粘性详情）；窄屏单列，表格用容器查询自动堆叠并逐格注入列名。
- 详情含来源与血缘、属性、受批准计算摘要、出边/入边表格（可点击跳转，方向键在行内移动焦点）、告警。
- 邻域探索：深度 1–3、方向分段控件、关系类型 chips，超过上限时显式提示已截断。
- 可回答性：覆盖率用原生 `<progress>` 表达（自带 `progressbar` 语义），并列结论、命中术语与缺口清单。
- 令牌复用文档视图 try-it 的 `sessionStorage`，只经 `Authorization` 头发送；`role="status"` + `aria-live="polite"` 播报进行中状态；`prefers-reduced-motion: reduce` 下禁用全部动效。

## 12. 与 omnigraph / auto-ontology 的关系

| 维度 | 本实现 | omniGraph | auto-ontology |
|---|---|---|---|
| 权威存储 | OKF+Git 正文 | 图数据库 | 源库 + 模型 |
| 分支 / 回滚 | 复用 `vcs` 的 Log/Show/Diff 与 OKF 回放；索引可丢弃 | 原生分支与时间旅行 | 无 |
| 建模来源 | 人/Agent 在 frontmatter 声明 + 本体约束 | 声明的 schema | LLM profiling、外键推断、列 embedding |
| 合并 | 不做三方合并，冲突由 Git 呈现 | 原生 merge | 无 |

**非目标（本轮）**：LLM 自动建模与列级 embedding、图算法的服务端计算（社区发现、PageRank）、图三元组的细粒度授权、以及把索引当作权威存储。分支、合并与时间旅行由 Git 提供等价能力，因此不另建原语。
