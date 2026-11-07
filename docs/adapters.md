# 适配器插件

## 统一契约

```go
type Adapter interface {
    Name() string
    Capabilities() Capabilities
    Put(ctx context.Context, conceptID string, doc *okf.Document) error
    Query(ctx context.Context, q Query) ([]Hit, error)
    Delete(ctx context.Context, conceptID string) error
    Fingerprint() string
    Health(ctx context.Context) error
}
```

`conceptID` 显式传入，因为 OKF 文档本身不含身份字段。`Hit` 必须带 `Source` 与 `Citation`，供读取合并保留出处。未实现的能力返回 `adapter.ErrUnsupported`（`errors.Is` 可判定），禁止静默 no-op。

## 注册表

`adapter.Registry` 采用**显式 kind 工厂 + 配置驱动装配**，不使用 Go `.so` 插件（跨平台与版本兼容性差）。`Build` 只装配 `enabled: true` 的条目；未知 kind 返回 `ErrUnsupported`；重名报错。

```yaml
adapters:
  - name: "okf"
    kind: "okf"
    enabled: true
  - name: "memory"
    kind: "memory"
    enabled: true
  - name: "llamaindex"
    kind: "http"
    enabled: false
    endpoint: ""
    timeout_ms: 3000
```

## 内置实现

| 名称 | 检索方式 | 说明 |
|---|---|---|
| `okf` | 词元匹配的确定性路径/标题/描述查找 | 只查询、不写入；正文由 `OKFStore` 独占 |
| `memory` | 倒排词元重叠 + 哈希向量余弦 | O(N·d) 小规模基线，离线默认 |
| `pgvector` | `meta.VectorStore` + 参数化原生 SQL | 与元数据层复用 DSN；无 DSN 时回落内存 fake |

`Embedder` 端口默认使用确定性 `HashingEmbedder`（词法而非语义）。`Fingerprint()` 由适配器名 + embedding 身份派生，变更即触发迁移重建。生产应注入真实 embedding 模型（`TODO(real)`）。

## 五个第三方引擎

| 引擎 | kind | 能力标签 | 桥接 |
|---|---|---|---|
| LlamaIndex | `http` | vector / semantic | `engines/llamaindex.yaml` |
| PageIndex | `http` | semantic / page_level | `engines/pageindex.yaml` |
| GraphRAG | `http` | graph | `engines/graphrag.yaml` |
| LightRAG | `http` | vector / semantic / graph | `engines/lightrag.yaml` |
| pgvector | `pgvector` | vector / semantic | 原生实现 |

前四者的接入深度为**契约 + 可测实现**：提供通用 HTTP/CLI 桥接、YAML 声明与确定性离线 mock，不接各自原生 SDK（`TODO(real)`）。真实部署需要一个实现下述线协议的 sidecar。

## HTTP 线协议

| 方法 | 路径 | 请求 | 响应 |
|---|---|---|---|
| GET | `/healthz` | — | 2xx |
| POST | `/put` | `{concept_id, type, title, description, body, tags}` | 2xx |
| POST | `/query` | `{text, top_k, vector?}` | `{"hits":[{concept_id, score, snippet, source, citation}]}` |
| POST | `/delete` | `{concept_id}` | 2xx |

客户端有超时、重试上限（仅传输错误/5xx，最多 1 次重试）、4xx 立即失败与 4MB 响应上限。

## CLI 线协议

`command put --id <id>`（stdin 为 put JSON）、`command query --text <t> --top-k <n>`（stdout 为 query JSON）、`command delete --id <id>`、`command health`。所有参数经白名单校验，执行不经过 shell。

## 离线 mock

`endpoint`/`command` 为空时，桥接显式退化为 `MemoryAdapter`（`Health` 正常、能力可见），不会静默变成空操作。这保证 `go test ./...` 无网络、无密钥即可覆盖五个引擎的装配、写入与查询。

## 新增引擎步骤

1. 在 `internal/bridges/engines/` 添加 `<name>.yaml`（name / kind / capabilities / timeout_ms / offline_mock / notes）。
2. 在 `config.yaml` 的 `adapters` 中启用同名条目并填 `endpoint`。
3. 若协议不同，实现 `adapter.Adapter` 并用 `registry.RegisterKind("<kind>", factory)` 注册工厂。
4. 补一条契约测试（复用 `contract_test.go` 的断言风格）。
