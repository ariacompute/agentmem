# 引擎插件与 SDK 边界

四个 Python 生态引擎以**一等 Go 插件包**的形式编译进 `agentmem` 单二进制，不使用 Go `.so` 动态插件。真实 SDK 的对接边界不是代码注释，而是每个插件 `plugin.yaml` 中受严格校验的 `sdk_boundary` 数据。

## 目录结构

```
internal/plugins/
├── manifest/manifest.go      # 清单契约、严格解析、能力映射、协议指纹
├── pluginbase/base.go        # Adapter + Lifecycle 的共享实现
├── plugins.go                # 显式注册表 RegisterAll / Descriptors / Kinds
├── llamaindex/{plugin.yaml,adapter.go}
├── pageindex/{plugin.yaml,adapter.go}
├── graphrag/{plugin.yaml,adapter.go}
└── lightrag/{plugin.yaml,adapter.go}
```

单向依赖：`plugins/manifest → adapter`，`plugins/pluginbase → bridges + adapter + manifest`，`plugins/<engine> → pluginbase`，`plugins → <engine>`。`service.Assemble` 是唯一装配点。

## 清单字段

| 字段 | 含义 |
|---|---|
| `name` / `kind` | 必须相等；`kind` 是 `adapters[].kind` 的配置值 |
| `engine` / `version` | 引擎标识与插件契约版本，参与指纹 |
| `bridge` | `http` 或 `cli`，决定复用哪条线协议实现 |
| `protocol_version` | 线协议标识，必须与 `sdk_boundary.wire_protocol` 一致 |
| `timeout_ms` | 无配置覆盖时的默认超时 |
| `offline_mock` | 是否允许空 endpoint/command 时退化为确定性离线 mock |
| `citation_scheme` | 引擎级引用前缀，覆盖桥接默认引用 |
| `capabilities` | `vector / semantic / graph / page_level`；`Put/Query/Delete` 由桥接补齐 |
| `sdk_boundary.sidecar` | 真实 SDK 所在进程的职责描述 |
| `sdk_boundary.endpoints` | sidecar 必须实现的端点或子命令 |
| `sdk_boundary.capability_map` | 每个启用能力如何映射到引擎原生能力 |
| `sdk_boundary.credentials_ref` | 凭据引用（环境变量/密钥名），**不得**写凭据值 |

`manifest.Parse` 使用 `KnownFields(true)`：未知键、kind 与 name 不一致、协议版本不一致、能力映射含未声明或未启用能力、边界字段缺失，一律报错。

## sdk_boundary 契约

`sdk_boundary` 说明「agentmem 到此为止，真实 SDK 从这里开始」：

- **sidecar** 用引擎原生 SDK/CLI 实现 `endpoints` 列出的线协议；agentmem 不直接链接任何 Python SDK。
- **凭据** 只在 sidecar 侧从 `credentials_ref` 指向的环境变量/密钥读取，agentmem 配置与日志都不包含凭据值。
- **能力映射** 是 `Capabilities` 与引擎原生检索模式之间的可审计对应关系；修改它即改变插件指纹。
- **协议指纹** 由 `name + version + bridge + protocol_version + capabilities + endpoints + capability_map` 做 SHA-256 派生。指纹变化会触发投影迁移重建，不会静默复用旧索引。

线协议本体见 [`adapters.md`](adapters.md) 的 HTTP/CLI 两张表。

## 生命周期与健康检查

`adapter.Lifecycle` 是可选端口：

```go
type Lifecycle interface {
    Start(ctx context.Context) error
    Close(ctx context.Context) error
}
```

- `Registry.StartAll` 按名字顺序调用 `Start`；`Start` 会实际探测 sidecar 健康端点，失败即让 `Assemble` 返回错误（fail closed），不会静默退化为离线 mock。
- 空 `endpoint`/`command` 时模式为 `offline-mock`，`Start` 探测确定性内存 mock；该模式通过 `Plugin.Status()["mode"]` 与 `Registry.HealthAll` 明确可见。
- `Registry.Close` 逆序、聚合错误地关闭所有插件；`KnowledgeService.Close` 先关插件再关元数据连接。
- `Close` 幂等；关闭后 `Health/Put/Query/Delete` 返回 `ErrClosed`。

`Status()` 输出 `engine / version / state / mode / protocol / sidecar / fingerprint`，不含正文、凭据或 PII。

## 四个插件

| 插件 | kind | bridge | 能力 | 引用方案 |
|---|---|---|---|---|
| LlamaIndex | `llamaindex` | http | vector + semantic | `llamaindex:/` |
| PageIndex | `pageindex` | http | semantic + page_level | `pageindex://page/` |
| GraphRAG | `graphrag` | cli | graph | `graphrag://community/` |
| LightRAG | `lightrag` | http | vector + semantic + graph | `lightrag:/` |

泛型 `http` / `cli` kind 与 `internal/bridges/engines/*.yaml` 继续保留，供自定义引擎使用；四个内置引擎请优先使用各自的插件 kind。

## 新增引擎

1. 新建 `internal/plugins/<name>/plugin.yaml` 并填齐全部字段。
2. 在包内实现 `Descriptor()`、`New(cfg, deps)`、`Register(r)`。
3. 登记到 `internal/plugins/plugins.go` 的 `All()`，并把 kind 加入 `config.Validate()` 白名单。
4. 在 `config.yaml` 启用条目；`endpoint` 为空即离线可测。
5. 补清单校验、契约、生命周期测试，并运行 `make check-boundaries`。

禁止以「不可测的原生客户端」替换桥接：每个外部依赖都必须有 fake，且 `go test ./...` 在无 Postgres、无网络、无凭据下全绿。
