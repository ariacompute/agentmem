# OKF v0.2 兼容性

## 实现的字段

| 字段 | 处理 |
|---|---|
| `type` | **唯一必填**；为空时 `Document.Validate()` 拒绝写入 |
| `title` / `description` / `resource` / `tags` | 字符串 / 字符串列表 |
| `sources` | `resource` 等客观信号；逐条断言可用 `sources[].id` 作脚注键 |
| `generated` | `{by, at}`，与 `verified` 分离 |
| `verified` | **bare mapping 与 list 都接受**；bare mapping 自动视为单元素列表 |
| `status` | `draft` / `stable` / `deprecated`；缺省按 `stable` |
| `stale_after` | 绝对 ISO 8601 时刻；`IsStale(now)` 比较 |
| `runtime` / `parameters` / `computation` / `executor` / `attester` | Attested Computation 契约建模（只读，不执行） |
| 未知键 | 以 `yaml.Node` 原样保留，round-trip 不丢失 |

## 信任等级（派生，不存储）

| `verified` | TrustTier |
|---|---|
| 空 | `unverified` |
| 仅非 `human:` actor | `machine-confirmed` |
| 含 `human:` actor | `human-reviewed` |

actor 约定：`agent/模型`（含 `/`）、`human:` 前缀、`process:` 前缀。`sources` 只记录客观信号（`author` / `usage_count` / `last_modified` / `usage_window`），不存主观可信度分数。

## 宽松 conformance（硬性）

消费者 MUST NOT 因以下情况拒绝 bundle：

- 缺失任何可选字段；
- 未知 `type` 值；
- 未知 frontmatter 键；
- 断链（未创建的概念链接合法，代表「尚未写的知识」）；
- 缺失 `index.md`。

`ParseDocument` 仅在 frontmatter 分隔符缺失或 YAML 语法非法时报错。`Bundle.Links` 返回链接目标但不校验存在性。

## 渐进式披露

根 `index.md` 只列子目录链接；每层 `index.md` 列本层概念（含 description）与子目录。`Bundle.RebuildIndex(dir)` 只重生成指定目录，避免参考实现 `regenerate_indexes` 的全树遍历。

## Attested Computation

`internal/okf/attested.go` 只建模契约：`runtime`、typed `parameters`、`computation`、`executor.receipt`、`attester.resource`。agentmem **不执行**受批准的计算，也不做 receipt 的 SQL 规范化比对与展示值 fidelity 校验——这是 OKF 规范中推迟到未来版本的能力。

## agentmem 扩展字段

| 字段 | 含义 |
|---|---|
| `visibility` | `private` / `team` / `public`，弥补 OKF connector 无条目级 ACL 的缺口 |
| `owner` | 归属主体 |

两者作为未知键被自然保留，PostgreSQL 侧为运行时权威。

## 与参考实现的差异

- 不实现 Google ADK/Gemini 参考生产者（BQ pass + LLM web crawler）；agentmem 的知识由 CLI/API 写入。
- 不实现静态 Cytoscape.js 图查看器；提供最小 Vite 状态页。
- 不推送到 Dataplex / Unity Catalog；Git 是唯一权威，目录服务是可选投影。
