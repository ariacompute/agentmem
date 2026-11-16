# 参考 compute sidecar（`bigquery` / `dbt` / `looker` / `python`）

`examples/compute-sidecars/sidecar.py` 是线协议 v1 的一个可运行参考实现：单文件、只用
Python 3 标准库、同时支持两种通道。它不是 agentmem 的一部分，也不被任何 Go 二进制链接；
它演示的是「一个受信任的出向执行器长什么样」。

协议本身见 [`docs/compute-sidecar.md`](../../docs/compute-sidecar.md)。四个 runtime 中
`postgres` 永远进程内执行，声明为 sidecar 会被拒绝。

## 两种通道

| 配置 | 启动方式 | 传输 |
|---|---|---|
| `{"bridge":"cli","command":"agentmem-example"}` | `agentmem-example run` | 请求 JSON 走 stdin，响应 JSON 走 stdout |
| `{"bridge":"http","endpoint":"http://127.0.0.1:8791"}` | `agentmem-example --addr 127.0.0.1:8791` | `POST /run`，请求/响应均为 JSON |

## 接入示例

```bash
AGENTMEM_COMPUTE_SIDECARS='[
  {"runtime":"bigquery","bridge":"cli","command":"agentmem-example","timeout_ms":120000},
  {"runtime":"dbt","bridge":"cli","command":"agentmem-example","timeout_ms":600000},
  {"runtime":"looker","bridge":"http","endpoint":"http://127.0.0.1:8791","timeout_ms":300000},
  {"runtime":"python","bridge":"cli","command":"agentmem-example","timeout_ms":30000}
]'
# bridge=http 的端点属于出向目标，必须同时声明主机白名单：
AGENTMEM_EGRESS_ALLOWED_HOSTS=127.0.0.1
```

把 `sidecar.py` 复制成裸可执行名（`cli` 只接受 `^[A-Za-z0-9_][A-Za-z0-9_.-]*$`，不接受路径或
shell 片段）：

```bash
install -m 0755 examples/compute-sidecars/sidecar.py /usr/local/bin/agentmem-example
```

## 离线演练（无凭据、无网络）

```bash
SIDECAR_OFFLINE=1 agentmem-example run < request.json
```

离线模式按 runtime 返回确定性表（`bigquery` 1250 / `dbt` 1310 / `looker` 1420 /
`python` 1530），`scripts/e2e.sh` 的 `C33`–`C35` 就是这样跑真实二进制的。开关刻意不用
`AGENTMEM_` 前缀：agentmem 的闭白名单会拒绝进程环境里未知的 `AGENTMEM_*` 键，而 `cli`
子进程会继承该环境。

## 真实模式需要的环境

| runtime | 依赖 | 环境变量 |
|---|---|---|
| `bigquery` | `bq`（gcloud SDK） | `BQ_PROJECT`、`GOOGLE_APPLICATION_CREDENTIALS` |
| `dbt` | `dbt` 可执行 | `DBT_PROJECT_DIR`、可选 `DBT_PROFILES_DIR` |
| `looker` | 无（标准库 HTTP） | `LOOKER_BASE_URL`、`LOOKER_CLIENT_ID`、`LOOKER_CLIENT_SECRET` |
| `python` | `python3` | 无（脚本在子解释器中运行） |

参数只以类型化值随行：`bigquery` 绑成 `--parameter=name:TYPE:value`，`dbt` 以
`AGENTMEM_VAR_*` 传入，`looker` 变成 `filters`，`python` 以 `AGENTMEM_PARAM_*` 注入子进程。
正文永远不会被字符串拼接进 SQL 或被 shell 解释。

## 不变量（Go 侧强制，sidecar 必须遵守）

1. 响应只允许 `columns` / `rows` / `body_digest` 三个字段；多一个字段，严格解码就整条拒绝。
2. `body_digest` 必须是**实际执行**正文的 SHA-256。Go 用自家正文重算比对，不符则
   `display_allowed=false`、不返回 `display_value`，审计照常写入。
3. `job_id`、结果与展示值一律由 Go 侧重算，sidecar 提供也不会生效。
4. 单元格只能是 `string` / 整数 / 浮点 / `bool` / `null`；嵌套对象直接判为执行失败。
5. 请求上限 1 MiB、响应上限 4 MiB、行数按 `AGENTMEM_COMPUTE_MAX_ROWS` 截断。

## 上线检查清单

- [ ] sidecar 以最小权限账户运行，只持有该 runtime 需要的只读凭据。
- [ ] **正文即沙箱边界**：`python` runtime 必须在受限文件系统/网络视图内执行脚本；
      `bigquery` / `dbt` / `looker` 的正文是声明式选择器，不得被当作命令行拼接。
- [ ] 凭据来自 secret 存储或进程环境，不写进 `.env`、镜像层或日志。
- [ ] 日志只记录请求行与错误类别，不记录正文、参数值或凭据（本示例已是如此）。
- [ ] sidecar 崩溃、超时或返回非标量单元格时，agentmem 只会看到脱敏的
      `sidecar process failed` / `execution`，不会泄漏主机名与 stderr——请保留该行为。
- [ ] 需要重放审计时，把 `body_digest` 与 `concept_id` 一起写入你自己的日志；agentmem
      的审计只存 verdict，不存正文。
