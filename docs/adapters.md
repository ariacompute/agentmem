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

`Embedder` 端口默认使用确定性 `HashingEmbedder`（词法而非语义）。`Fingerprint()` 由适配器名 + `Embedder.Identity()` 派生，变更即触发迁移重建。生产通过 `Embedder` 端口注入模型实现，指纹会自动失效旧投影。

## 五个第三方引擎

| 引擎 | 插件 kind | 线协议 | 能力标签 |
|---|---|---|---|
| LlamaIndex | `llamaindex` | HTTP | vector / semantic |
| PageIndex | `pageindex` | HTTP | semantic / page_level |
| GraphRAG | `graphrag` | CLI stdio | graph |
| LightRAG | `lightrag` | HTTP | vector / semantic / graph |
| pgvector | `pgvector`（内置） | 参数化原生 SQL | vector / semantic |

前四者现在是**一等 Go 插件包**（`internal/plugins/<engine>/`），各自持有 `plugin.yaml` 清单、`Adapter` 实现、生命周期与健康检查、配置 kind 与注册表装配。真实 SDK 对接边界由清单中的 `sdk_boundary` 字段正式声明（sidecar、线协议版本、端点/命令、能力映射、凭据引用、协议指纹），不依赖代码注释；详见 [`plugins.md`](plugins.md)。`engines/*.yaml` 仅作为泛型 `http`/`cli` kind 的向后兼容声明保留。

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

1. 新建 `internal/plugins/<name>/plugin.yaml`，填齐 `name/kind/engine/version/bridge/protocol_version/timeout_ms/offline_mock/citation_scheme/capabilities/sdk_boundary`。清单采用严格解析，未知键报错。
2. 在同一个包中实现 `Descriptor()`、`New(cfg, deps)`、`Register(r)`，用 `pluginbase.New` 组合泛型桥接。
3. 在 `internal/plugins/plugins.go` 的 `All()` 中登记，并在 `config.Validate()` 的 kind 白名单中加入同名 kind。
4. 在 `config.yaml` 的 `adapters` 中启用条目并填 `endpoint`（为空即确定性离线 mock）。
5. 补清单校验、契约与生命周期测试；完整说明见 [`plugins.md`](plugins.md)。
