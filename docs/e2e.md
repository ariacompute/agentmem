# 用户视角黑盒验收（e2e）

`scripts/e2e.sh` 以**最终用户**的方式验证 agentmem：编译出真实的 `cmd/agentmem` 二进制，
用子进程调用 CLI，用 `curl` 访问真实 HTTP 服务。它不 import 任何 `internal/` 包，
不改动生产代码，断言只基于退出码、stdout、HTTP 状态码、响应头与响应体。

```bash
make e2e                                  # 全量（含现有质量门禁）
make e2e E2E_ARGS='--no-gates'            # 只跑黑盒用例
./scripts/e2e.sh --filter '^H' --keep     # 只报告 HTTP 组并保留临时目录
./scripts/e2e.sh --help
```

## 依赖与离线保证

- 必需：`go`、`curl`、`python3`（含标准库 `json`）、`sed`/`grep`/`awk`/`sha256sum`。
  不依赖 `jq`、不新增 Go/前端依赖。
- 可选：`bun`（`web/tests` 的离线 `node:test` 套件）、`openssl`（`make check-trust-store`）。
  缺失时对应项记 `SKIP` 并给出原因，**不静默通过**；必需工具缺失则退出码 2。
- 脚本设 `GOPROXY=off`，只用本地 module cache；`AGENTMEM_PG_DSN` 留空启用离线
  file-fake 存储，System One 与 adapter 走确定性 mock。全程无 Postgres、无网络、
  无凭据，也不产生真实密钥：bearer 令牌运行时随机生成，配置里只写 SHA-256 摘要。
- 启动前清空继承的 `AGENTMEM_*` 进程环境变量，避免封闭白名单误判；所有配置写入
  `mktemp -d` 下的 `.env` 并通过 `--env` 显式传入。二进制、`testdata/bundle` 副本、
  `meta.fake.json`、响应体与日志都在该临时目录，`trap` 回收后台服务与目录。

## 参数与退出码

| 参数 | 作用 |
|---|---|
| `--no-gates` | 跳过第 8 组耗时门禁（二进制构建始终执行） |
| `--keep` | 保留临时工作目录，便于查看 `serve-*.log`、`gate-*.log` |
| `--filter REGEX` | 只报告编号匹配用例的结果（底层请求照常执行，便于保持服务状态一致），如 `'^H'`、`'^(C\|S)'` |
| `--help` | 显示帮助 |

退出码：`0` 全部通过；`1` 有用例失败；`2` 用法/环境/构建错误。
每条用例打印编号与 `PASS`/`FAIL`/`SKIP`，末尾给出汇总计数。失败信息只含期望与实际
状态码、截断的诊断文本，不回显令牌、正文、DSN 或 SQL。

## 用例矩阵

| 组 | 编号 | 覆盖范围 |
|---|---|---|
| 构建 | `G01` | 离线编译真实二进制 |
| 配置 | `F01`–`F19` | `validate` 正向（基准配置、`.env.example`、缺失文件回退、完整 production、egress 白名单）；故障关闭（未知文件键、未知进程环境键、非法 mode/数值/布尔/ADAPTERS/scope、重复键、畸形行、production 缺 DSN/缺鉴权/`sslmode=disable`、出向端点缺白名单、未实现冲突策略） |
| CLI | `C01`–`C28` | `help`/无参数/未知命令；`put` `query` `migrate`（含 `--dry-run`）`decisions` `concepts` `compute`（含重复 `--param`）`computations` `validate` `eval` 的正常链路；缺参、匿名写入、owner 不匹配、非法 visibility、非法参数、不支持 runtime、坏 JSONL、缺失数据集、`eval --env` 与未知 flag 等异常路径 |
| HTTP | `H01`–`H35` | 9 条 method+path 全部端到端（写→读→查询→迁移→决策审计→计算→计算审计→概念列表→健康检查）；405、Content-Type、未知字段、trailing data、畸形 JSON、4 MiB 上限、`top_k`/`limit`/必填字段边界；计算 400/422 且不展示 `display_value`；错误脱敏、安全响应头、404、二进制不托管 Web |
| 安全 | `S01`–`S20` | 6 个 scope 的权限矩阵（正反向）、公开 `/healthz`、401 + `WWW-Authenticate`、未知令牌、`health:read` 不足、全 scope 令牌；请求体与查询串 `subject` 伪造；私有文档 ACL（owner 可读 / 他人统一 404）；`Origin: null`、跨站未授权/白名单放行；405 在鉴权之后；错误体不回显凭据 |
| 限流 | `R01` | 独立低限额档位，突发请求触发 429 及固定错误体 |
| 跨进程 | `X01`–`X05` | 服务停止后 CLI 仍能看到 HTTP 写入的概念、路由审计、计算留痕；快照合法；OKF 正文落盘 |
| Web | `W01`–`W05` | 自带 CSP、无外链 CDN、API 文档镜像 9 条端点、13 语言与 7 个离线测试文件、`bun test` |
| 门禁 | `G02`–`G08` | `go vet`、`go test ./... -count=1`、`go build ./...`、`make validate`、`make check-boundaries`、`make eval`、`make check-trust-store` |

不含真实浏览器 UI 自动化；`make web-audit` 需要联网，因此不纳入离线验收。

## 实现要点

- 三个服务档位顺序启停：`base`（鉴权关闭、高限额）、`auth`（多 scoped 令牌）、
  `rate`（低限额）。CLI 写入放在服务启动前，因为离线快照在进程启动时加载一次、
  退出时写回，运行中并发写入会被覆盖。
- `GET /healthz` 同样经过限流器，就绪探测会消耗 token；`rate` 档位用 `burst=5`
  并在就绪后立即连发请求，保证 429 可稳定复现。
- 断言基准取自实现本身（`internal/handlers`、`internal/config`、`internal/cli`），
  例如 401 为 `authentication required`、405 为 `method not allowed`、超 4 MiB 返回
  `400 invalid JSON`（不是 413）。契约变更时需同步更新本脚本与
  `web/src/lib/api-content.js`。
- 出站 sidecar 的 `/put`、`/query`、`/delete` 不是服务端路由，脚本不会误测它们。

## 排错

```bash
./scripts/e2e.sh --filter '^S' --keep --no-gates
# 输出末尾给出临时目录，其中：
#   serve-*.log      各档位服务启动日志
#   body / headers   最后一次 HTTP 响应
#   cli.out / cli.err 最后一次 CLI 输出
#   base.env / auth.env / rate.env / bad-*.env
#   gate-*.log       门禁输出
```

排查完成后手动 `rm -rf` 该目录即可。
