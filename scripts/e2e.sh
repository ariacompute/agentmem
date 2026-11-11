#!/usr/bin/env bash
# e2e.sh — agentmem 用户视角黑盒验收脚本。
#
# 只做三件事：编译真实二进制、以子进程调用 CLI、用 curl 访问真实的 HTTP 服务。
# 不 import 任何 internal 包，不修改生产代码，不依赖 Postgres / 网络 / 凭据。
# 所有运行期产物（二进制、.env、bundle 副本、快照、响应体、日志）都在 mktemp
# 临时目录内，退出时由 trap 回收；--keep 可保留现场排错。
#
# 用法:
#   scripts/e2e.sh                     # 全量（含现有质量门禁）
#   scripts/e2e.sh --no-gates          # 只跑黑盒用例，跳过耗时门禁
#   scripts/e2e.sh --filter '^H'       # 只报告 HTTP 组
#   scripts/e2e.sh --keep              # 保留临时目录
#   make e2e                           # 仓库门禁入口
#
# 退出码: 0 = 全部通过；1 = 有用例失败；2 = 用法/环境/构建错误。
# 可选工具（bun、openssl）缺失时对应项记 SKIP，绝不静默通过。

set -uo pipefail

REPO="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)" || exit 2
KEEP=0
NO_GATES=0
FILTER=""

usage() {
  cat <<'USAGE'
用法: scripts/e2e.sh [--no-gates] [--keep] [--filter REGEX] [--help]

  --no-gates       跳过 go vet / go test / make 门禁（二进制构建始终执行）
  --keep           保留临时工作目录，便于排错
  --filter REGEX   只报告编号匹配该正则的用例结果（如 '^H'、'^(C|S)'）
  --help           显示本帮助

退出码: 0 全通过；1 有用例失败；2 用法/环境/构建错误。
USAGE
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    -h|--help) usage; exit 0 ;;
    --keep) KEEP=1 ;;
    --no-gates) NO_GATES=1 ;;
    --filter)
      if [[ $# -lt 2 ]]; then echo "error: --filter 需要一个正则参数" >&2; exit 2; fi
      FILTER="$2"; shift ;;
    --filter=*) FILTER="${1#*=}" ;;
    *) echo "error: 未知参数 $1" >&2; usage >&2; exit 2 ;;
  esac
  shift
done

# --- 环境预检 ---------------------------------------------------------------
for tool in go curl python3 sed grep awk sha256sum; do
  if ! command -v "$tool" >/dev/null 2>&1; then
    echo "error: 缺少必需工具 $tool" >&2
    exit 2
  fi
done
cd "$REPO" || { echo "error: 无法进入仓库根目录" >&2; exit 2; }
for f in go.mod Makefile .env.example testdata/eval.jsonl testdata/bundle/index.md \
         web/index.html web/src/lib/api-content.js; do
  if [[ ! -f "$f" ]]; then echo "error: 缺少 $f" >&2; exit 2; fi
done

# config.Load 会用进程环境覆盖 .env，且拒绝任何未知的 AGENTMEM_* 进程键。
# 先清空继承值，保证每个用例只受自己那份 env 文件控制。
for _k in $(env | sed -n 's/^\(AGENTMEM_[A-Za-z0-9_]*\)=.*/\1/p'); do
  unset "$_k"
done
unset _k
export GOPROXY=off   # 全程离线：只用本地 module cache，绝不联网

WORK="$(mktemp -d "${TMPDIR:-/tmp}/agentmem-e2e.XXXXXX")" || exit 2
cp -R "$REPO/testdata/bundle" "$WORK/bundle" || exit 2
BIN="$WORK/agentmem"
TOOL="$WORK/jsontool.py"
BODY="$WORK/body"
HEADERS="$WORK/headers"
CLI_OUT="$WORK/cli.out"
CLI_ERR="$WORK/cli.err"
SERVER_PID=""
PORT=""

# --- 报告框架 ---------------------------------------------------------------
PASS=0
FAIL=0
SKIP=0
CASE_ID=""
CASE_DESC=""
CASE_ERR=""
CASE_ACTIVE=1

section() { printf '\n== %s ==\n' "$1"; }

begin_case() {
  CASE_ID="$1"
  CASE_DESC="$2"
  CASE_ERR=""
  CASE_ACTIVE=1
  if [[ -n "$FILTER" && ! "$CASE_ID" =~ $FILTER ]]; then CASE_ACTIVE=0; fi
}

ck() {
  local ok="$1"; shift
  if [[ "$ok" -ne 0 && "$CASE_ACTIVE" -eq 1 ]]; then CASE_ERR="${CASE_ERR}; $*"; fi
}

eq() { if [[ "$1" == "$2" ]]; then ck 0 ""; else ck 1 "$3: 期望 '$2'，实际 '$1'"; fi; }
has() { if [[ "$1" == *"$2"* ]]; then ck 0 ""; else ck 1 "$3: 未包含 '$2'"; fi; }
hasnt() { if [[ "$1" != *"$2"* ]]; then ck 0 ""; else ck 1 "$3: 不应包含 '$2'"; fi; }

end_case() {
  [[ "$CASE_ACTIVE" -eq 1 ]] || return 0
  if [[ -z "$CASE_ERR" ]]; then
    printf '  PASS %-4s %s\n' "$CASE_ID" "$CASE_DESC"
    PASS=$((PASS + 1))
  else
    printf '  FAIL %-4s %s | %s\n' "$CASE_ID" "$CASE_DESC" "${CASE_ERR#; }"
    FAIL=$((FAIL + 1))
  fi
}

skip_case() {
  begin_case "$1" "$2"
  [[ "$CASE_ACTIVE" -eq 1 ]] || return 0
  printf '  SKIP %-4s %s (%s)\n' "$1" "$2" "$3"
  SKIP=$((SKIP + 1))
  CASE_ACTIVE=0
}

# 服务起不来属于被测对象故障，不能像可选工具缺失那样 SKIP 掉。
fail_case() {
  begin_case "$1" "$2"
  [[ "$CASE_ACTIVE" -eq 1 ]] || return 0
  printf '  FAIL %-4s %s (%s)\n' "$1" "$2" "$3"
  FAIL=$((FAIL + 1))
  CASE_ACTIVE=0
}

# --- JSON 断言（python3 标准库，替代 jq） -----------------------------------
cat > "$TOOL" <<'PY'
import json
import sys

mode, filename = sys.argv[1], sys.argv[2]
with open(filename, "r", encoding="utf-8", errors="replace") as fh:
    data = json.load(fh)


def lookup(obj, path):
    cur = obj
    for part in path.split("."):
        if part == "":
            continue
        if isinstance(cur, dict):
            if part not in cur:
                return False, None
            cur = cur[part]
        elif isinstance(cur, list):
            try:
                idx = int(part)
            except ValueError:
                return False, None
            if idx < 0 or idx >= len(cur):
                return False, None
            cur = cur[idx]
        else:
            return False, None
    return True, cur


def render(value):
    if value is None:
        return "null"
    if value is True:
        return "true"
    if value is False:
        return "false"
    if isinstance(value, (dict, list)):
        return json.dumps(value, ensure_ascii=False, sort_keys=True)
    return str(value)


if mode == "get":
    ok, value = lookup(data, sys.argv[3])
    print(render(value) if ok else "__MISSING__")
elif mode == "exists":
    ok, value = lookup(data, sys.argv[3])
    sys.exit(0 if ok and value is not None else 1)
elif mode == "len":
    ok, value = lookup(data, sys.argv[3])
    print(len(value) if ok and isinstance(value, (list, dict, str)) else -1)
elif mode == "contains":
    ok, value = lookup(data, sys.argv[3])
    needle = sys.argv[4]
    found = ok and (
        (isinstance(value, list) and needle in value)
        or (isinstance(value, str) and needle in value)
        or (isinstance(value, dict) and needle in value)
    )
    sys.exit(0 if found else 1)
else:
    sys.exit(2)
PY

jget() { python3 "$TOOL" get "$1" "$2" 2>/dev/null || echo "__BADJSON__"; }
jexists() { python3 "$TOOL" exists "$1" "$2" >/dev/null 2>&1; }
jlen() { python3 "$TOOL" len "$1" "$2" 2>/dev/null || echo -1; }
jcontains() { python3 "$TOOL" contains "$1" "$2" "$3" >/dev/null 2>&1; }

a_json_eq() { eq "$(jget "$BODY" "$1")" "$2" "JSON $1"; }
a_json_has() { jexists "$BODY" "$1"; ck $? "JSON 缺少 $1"; }
a_json_absent() { if jexists "$BODY" "$1"; then ck 1 "JSON 不应存在 $1"; else ck 0 ""; fi; }
a_json_contains() { jcontains "$BODY" "$1" "$2"; ck $? "JSON $1 不含 '$2'"; }
a_json_len_min() {
  local n; n="$(jlen "$BODY" "$1")"
  if [[ "$n" =~ ^[0-9]+$ ]] && [[ "$n" -ge "$2" ]]; then ck 0 ""; else ck 1 "JSON $1 长度期望 >=$2，实际 $n"; fi
}
a_body_has() { has "$(head -c 2000 "$BODY")" "$1" "响应体"; }
a_body_hasnt() { hasnt "$(head -c 4000 "$BODY")" "$1" "响应体"; }

header_value() {
  grep -i "^$1:" "$HEADERS" 2>/dev/null | head -n 1 | sed -e 's/^[^:]*:[[:space:]]*//' -e 's/\r$//'
}
a_header() {
  local v; v="$(header_value "$1")"
  if [[ "$v" == *"$2"* ]]; then ck 0 ""; else ck 1 "响应头 $1 期望含 '$2'，实际 '$v'"; fi
}

# --- HTTP / CLI 原语 --------------------------------------------------------
STATUS="000"

http_req() { # METHOD PATH [BODY|@FILE] [HEADER...]；NO_CT 可抑制默认 Content-Type
  local method="$1" path="$2" body="${3-}"
  local -a hdrs=()
  if (( $# >= 4 )); then hdrs=("${@:4}"); fi
  local -a args=(-sS --max-time 15 -o "$BODY" -D "$HEADERS" -w '%{http_code}' -X "$method")
  local h ct=0 no_ct=0
  for h in "${hdrs[@]}"; do
    case "$h" in
      NO_CT) no_ct=1 ;;
      Content-Type:*) ct=1; args+=(-H "$h") ;;
      *) args+=(-H "$h") ;;
    esac
  done
  if [[ -n "$body" && "$no_ct" -eq 0 && "$ct" -eq 0 ]]; then
    args+=(-H 'Content-Type: application/json')
  fi
  if [[ "$body" == @* ]]; then
    args+=(--data-binary "@${body#@}")
  elif [[ -n "$body" ]]; then
    args+=(--data-binary "$body")
  fi
  STATUS="$(curl "${args[@]}" "http://127.0.0.1:${PORT}${path}" 2>"$WORK/curl.err")" || STATUS="000"
}

a_status() { eq "$STATUS" "$1" "HTTP 状态"; }

hc() { # ID DESC METHOD PATH BODY WANT_STATUS [HEADER...]
  local id="$1" desc="$2" method="$3" path="$4" body="$5" want="$6"
  local -a hdrs=()
  if (( $# >= 7 )); then hdrs=("${@:7}"); fi
  begin_case "$id" "$desc"
  http_req "$method" "$path" "$body" "${hdrs[@]}"
  a_status "$want"
}

CLI_RC=0
run_cli() { "$BIN" "$@" >"$CLI_OUT" 2>"$CLI_ERR"; CLI_RC=$?; }
run_cli_env() { # VAR=VALUE ARGS...（只在子进程中注入，用于进程环境白名单用例）
  local var="$1"; shift
  env "$var" "$BIN" "$@" >"$CLI_OUT" 2>"$CLI_ERR"; CLI_RC=$?
}
a_rc() { eq "$CLI_RC" "$1" "CLI 退出码"; }
a_cli_out() { has "$(head -c 2000 "$CLI_OUT")" "$1" "CLI stdout"; }
a_cli_err() { has "$(head -c 2000 "$CLI_ERR")" "$1" "CLI stderr"; }
a_cli_json_eq() { eq "$(jget "$CLI_OUT" "$1")" "$2" "CLI JSON $1"; }
a_cli_json_has() { jexists "$CLI_OUT" "$1"; ck $? "CLI JSON 缺少 $1"; }
a_cli_json_absent() { if jexists "$CLI_OUT" "$1"; then ck 1 "CLI JSON 不应存在 $1"; else ck 0 ""; fi; }
a_cli_json_len_min() {
  local n; n="$(jlen "$CLI_OUT" "$1")"
  if [[ "$n" =~ ^[0-9]+$ ]] && [[ "$n" -ge "$2" ]]; then ck 0 ""; else ck 1 "CLI JSON $1 长度期望 >=$2，实际 $n"; fi
}
a_cli_json_contains() { jcontains "$CLI_OUT" "$1" "$2"; ck $? "CLI JSON $1 不含 '$2'"; }

# --- 服务管理 ---------------------------------------------------------------
start_server() { # NAME ENVFILE PORT
  local name="$1" envfile="$2" port="$3" i code
  PORT="$port"
  "$BIN" serve --env "$envfile" >"$WORK/serve-$name.log" 2>&1 &
  SERVER_PID=$!
  for i in $(seq 1 100); do
    code="$(curl -sS --max-time 1 -o "$WORK/ready.body" -w '%{http_code}' \
      "http://127.0.0.1:$port/healthz" 2>/dev/null || true)"
    if [[ "$code" == "200" ]]; then return 0; fi
    if ! kill -0 "$SERVER_PID" 2>/dev/null; then return 1; fi
    sleep 0.1
  done
  return 1
}

stop_server() {
  if [[ -n "$SERVER_PID" ]]; then
    kill -TERM "$SERVER_PID" 2>/dev/null || true
    wait "$SERVER_PID" 2>/dev/null || true
    SERVER_PID=""
  fi
}

cleanup() {
  stop_server
  if [[ "$KEEP" -eq 1 ]]; then
    printf '\n临时工作目录已保留: %s\n' "$WORK"
  else
    rm -rf "$WORK"
  fi
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

# --- 配置准备 ---------------------------------------------------------------
PORTS="$(python3 -c '
import socket
socks = []
for _ in range(3):
    s = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
    s.bind(("127.0.0.1", 0))
    socks.append(s)
print(" ".join(str(s.getsockname()[1]) for s in socks))
')" || { echo "error: 无法分配本地端口" >&2; exit 2; }
read -r PORT_BASE PORT_AUTH PORT_RATE <<< "$PORTS"

BASE_ENV="$WORK/base.env"
AUTH_ENV="$WORK/auth.env"
RATE_ENV="$WORK/rate.env"

make_env() { # FILE PORT RPS BURST [EXTRA_LINE...]
  local out="$1" port="$2" rps="$3" burst="$4" line
  shift 4
  {
    printf 'AGENTMEM_ADDR=127.0.0.1:%s\n' "$port"
    printf 'AGENTMEM_MODE=dev\n'
    printf 'AGENTMEM_GIT_ROOT=%s/bundle\n' "$WORK"
    printf 'AGENTMEM_GIT_AUTHOR_NAME=agentmem-e2e\n'
    printf 'AGENTMEM_GIT_AUTHOR_EMAIL=e2e@localhost\n'
    printf 'AGENTMEM_PG_DSN=\n'
    printf 'AGENTMEM_RATE_RPS=%s\n' "$rps"
    printf 'AGENTMEM_RATE_BURST=%s\n' "$burst"
    printf 'AGENTMEM_ACL_DEFAULT_OWNER=system\n'
    printf 'AGENTMEM_ACL_DEFAULT_VISIBILITY=private\n'
    # 不在此处写 AGENTMEM_AUTH_ENABLED：默认值已是 false，且鉴权档位要覆盖它，
    # 重复键会被 dotenv 解析器判为错误。
    for line in "$@"; do printf '%s\n' "$line"; done
  } > "$out"
}

bad_env() { # NAME [LINE...]；只写必要键，其余走 Default()
  local name="$1" line
  shift
  {
    printf 'AGENTMEM_ADDR=127.0.0.1:%s\n' "$PORT_BASE"
    printf 'AGENTMEM_GIT_ROOT=%s/bundle\n' "$WORK"
    for line in "$@"; do printf '%s\n' "$line"; done
  } > "$WORK/bad-$name.env"
}

digest() { printf '%s' "$1" | sha256sum | awk '{print $1}'; }

STAMP="$RANDOM$RANDOM$$"
TOK_ALL="e2e-all-$STAMP"
TOK_READ="e2e-read-$STAMP"
TOK_WRITE="e2e-write-$STAMP"
TOK_MIGRATE="e2e-migrate-$STAMP"
TOK_AUDIT="e2e-audit-$STAMP"
TOK_COMPUTE="e2e-compute-$STAMP"
TOK_HEALTH="e2e-health-$STAMP"

AUTH_JSON='['
AUTH_JSON+='{"token_sha256":"'"$(digest "$TOK_ALL")"'","subject":"alice","scopes":["health:read","knowledge:read","knowledge:write","migrate:run","audit:read","compute:run"]},'
AUTH_JSON+='{"token_sha256":"'"$(digest "$TOK_READ")"'","subject":"bob","scopes":["knowledge:read"]},'
AUTH_JSON+='{"token_sha256":"'"$(digest "$TOK_WRITE")"'","subject":"carol","scopes":["knowledge:write"]},'
AUTH_JSON+='{"token_sha256":"'"$(digest "$TOK_MIGRATE")"'","subject":"dave","scopes":["migrate:run"]},'
AUTH_JSON+='{"token_sha256":"'"$(digest "$TOK_AUDIT")"'","subject":"erin","scopes":["audit:read"]},'
AUTH_JSON+='{"token_sha256":"'"$(digest "$TOK_COMPUTE")"'","subject":"frank","scopes":["compute:run"]},'
AUTH_JSON+='{"token_sha256":"'"$(digest "$TOK_HEALTH")"'","subject":"grace","scopes":["health:read"]}'
AUTH_JSON+=']'

PROD_JSON='[{"token_sha256":"'"$(digest "e2e-prod-$STAMP")"'","subject":"prod","scopes":["knowledge:read"]}]'
PROD_DSN='postgres://e2e:placeholder@db.example.test:5432/agentmem?sslmode=require'

make_env "$BASE_ENV" "$PORT_BASE" 100000 1000000
make_env "$AUTH_ENV" "$PORT_AUTH" 100000 1000000 \
  'AGENTMEM_AUTH_ENABLED=true' \
  "AGENTMEM_AUTH_TOKENS='$AUTH_JSON'" \
  'AGENTMEM_ALLOWED_ORIGINS=https://console.example.test'
make_env "$RATE_ENV" "$PORT_RATE" 1 5

ENVARG=(--env "$BASE_ENV")
HTTP_DOC='e2e/http-doc'
CLI_DOC='e2e/cli-doc'
COMPUTE_ID='computations/revenue-postgres'
UNSUPPORTED_ID='computations/revenue-ytd'

printf 'agentmem 用户视角黑盒验收\n仓库: %s\n工作区: %s\n' "$REPO" "$WORK"

# ===========================================================================
section "0. 构建"
# ===========================================================================
begin_case G01 "离线编译真实二进制"
if GOPROXY=off go build -o "$BIN" ./cmd/agentmem >"$WORK/build.log" 2>&1; then
  ck 0 ""
else
  ck 1 "go build 失败: $(head -c 300 "$WORK/build.log")"
fi
end_case
if [[ ! -x "$BIN" ]]; then
  printf '\n无法继续：二进制构建失败。\n' >&2
  exit 2
fi

# ===========================================================================
section "1. 配置校验与故障关闭（CLI validate）"
# ===========================================================================
begin_case F01 "validate 接受离线基准配置"
run_cli validate "${ENVARG[@]}"
a_rc 0
a_cli_json_eq status ok
a_cli_json_eq mode dev
a_cli_json_eq adapters 2
end_case

begin_case F02 "validate 接受仓库模板 .env.example"
run_cli validate --env "$REPO/.env.example"
a_rc 0
a_cli_json_eq status ok
a_cli_json_eq adapters 7
end_case

begin_case F03 "env 文件缺失时回退默认配置"
run_cli validate --env "$WORK/does-not-exist.env"
a_rc 0
a_cli_json_eq status ok
a_cli_json_eq mode dev
end_case

bad_env unknown-key 'AGENTMEM_TYPO_KEY=1'
begin_case F04 "env 文件中的未知 AGENTMEM_* 键被拒绝"
run_cli validate --env "$WORK/bad-unknown-key.env"
a_rc 1
a_cli_err 'unknown keys AGENTMEM_TYPO_KEY'
end_case

begin_case F05 "进程环境中的未知 AGENTMEM_* 键被拒绝"
run_cli_env 'AGENTMEM_TYPO_KEY=1' validate "${ENVARG[@]}"
a_rc 1
a_cli_err 'unknown process environment keys'
end_case

bad_env mode 'AGENTMEM_MODE=staging'
begin_case F06 "非法 mode 被拒绝"
run_cli validate --env "$WORK/bad-mode.env"
a_rc 1
a_cli_err 'must be dev or production'
end_case

bad_env prod-no-dsn 'AGENTMEM_MODE=production'
begin_case F07 "production 缺少 DSN 被拒绝"
run_cli validate --env "$WORK/bad-prod-no-dsn.env"
a_rc 1
a_cli_err 'postgres.dsn is required in production'
end_case

bad_env prod-no-auth 'AGENTMEM_MODE=production' "AGENTMEM_PG_DSN=$PROD_DSN"
begin_case F08 "production 缺少鉴权被拒绝"
run_cli validate --env "$WORK/bad-prod-no-auth.env"
a_rc 1
a_cli_err 'production requires AGENTMEM_AUTH_ENABLED=true'
end_case

bad_env prod-insecure 'AGENTMEM_MODE=production' \
  'AGENTMEM_PG_DSN=postgres://e2e:placeholder@db.example.test:5432/agentmem?sslmode=disable' \
  'AGENTMEM_AUTH_ENABLED=true' "AGENTMEM_AUTH_TOKENS='$PROD_JSON'"
begin_case F09 "production 拒绝 sslmode=disable"
run_cli validate --env "$WORK/bad-prod-insecure.env"
a_rc 1
a_cli_err 'must not disable TLS'
end_case

bad_env prod-ok 'AGENTMEM_MODE=production' "AGENTMEM_PG_DSN=$PROD_DSN" \
  'AGENTMEM_AUTH_ENABLED=true' "AGENTMEM_AUTH_TOKENS='$PROD_JSON'"
begin_case F10 "完整 production 配置通过校验"
run_cli validate --env "$WORK/bad-prod-ok.env"
a_rc 0
a_cli_json_eq mode production
end_case

bad_env egress-missing 'AGENTMEM_SYSTEMONE_ENDPOINT=https://example.test/route'
begin_case F11 "配置出向端点但不配白名单被拒绝"
run_cli validate --env "$WORK/bad-egress-missing.env"
a_rc 1
a_cli_err 'outbound endpoints require AGENTMEM_EGRESS_ALLOWED_HOSTS'
end_case

bad_env egress-ok 'AGENTMEM_SYSTEMONE_ENDPOINT=https://example.test/route' \
  'AGENTMEM_EGRESS_ALLOWED_HOSTS=example.test'
begin_case F12 "配置出向端点并配白名单通过校验"
run_cli validate --env "$WORK/bad-egress-ok.env"
a_rc 0
a_cli_json_eq status ok
end_case

bad_env adapters "AGENTMEM_ADAPTERS='not-json'"
begin_case F13 "非法 AGENTMEM_ADAPTERS JSON 被拒绝"
run_cli validate --env "$WORK/bad-adapters.env"
a_rc 1
a_cli_err 'AGENTMEM_ADAPTERS'
end_case

bad_env scope 'AGENTMEM_AUTH_ENABLED=true' \
  "AGENTMEM_AUTH_TOKENS='[{\"token_sha256\":\"$(digest "e2e-bad-$STAMP")\",\"subject\":\"x\",\"scopes\":[\"bogus:scope\"]}]'"
begin_case F14 "未知权限 scope 被拒绝"
run_cli validate --env "$WORK/bad-scope.env"
a_rc 1
a_cli_err 'unknown scope'
end_case

bad_env conflict 'AGENTMEM_CONFLICT_POLICY=rag_wins'
begin_case F15 "未实现的冲突策略被拒绝"
run_cli validate --env "$WORK/bad-conflict.env"
a_rc 1
a_cli_err 'conflict_policy'
end_case

bad_env rate 'AGENTMEM_RATE_RPS=abc'
begin_case F16 "非法数值被拒绝"
run_cli validate --env "$WORK/bad-rate.env"
a_rc 1
a_cli_err 'expected an integer'
end_case

bad_env bool 'AGENTMEM_AUTH_ENABLED=maybe'
begin_case F17 "非法布尔被拒绝"
run_cli validate --env "$WORK/bad-bool.env"
a_rc 1
a_cli_err 'expected a boolean'
end_case

bad_env duplicate 'AGENTMEM_MODE=dev' 'AGENTMEM_MODE=dev'
begin_case F18 "重复键被拒绝"
run_cli validate --env "$WORK/bad-duplicate.env"
a_rc 1
a_cli_err 'duplicate key'
end_case

bad_env malformed 'THIS_LINE_HAS_NO_EQUALS'
begin_case F19 "畸形 KEY=VALUE 行被拒绝"
run_cli validate --env "$WORK/bad-malformed.env"
a_rc 1
a_cli_err 'expected KEY=VALUE'
end_case

# ===========================================================================
section "2. CLI 全命令（服务启动前，避免快照被覆写）"
# ===========================================================================
begin_case C01 "无参数输出 usage"
run_cli
a_rc 0
a_cli_out 'Commands:'
a_cli_out '  put         Write a knowledge concept'
end_case

begin_case C02 "help 输出 usage"
run_cli help
a_rc 0
a_cli_out 'agentmem - OKF+Git knowledge hub'
end_case

begin_case C03 "--help 输出 usage"
run_cli --help
a_rc 0
a_cli_out '  eval        Run ranking'
end_case

begin_case C04 "未知命令报错"
run_cli no-such-command
a_rc 1
a_cli_err 'unknown command "no-such-command"'
end_case

begin_case C05 "put 写入知识概念"
run_cli put "${ENVARG[@]}" --id "$CLI_DOC" --subject alice --owner alice \
  --type Metric --title 'E2E churn' --description 'e2e description' \
  --body '# Definition' --visibility public
a_rc 0
a_cli_json_eq concept_id "$CLI_DOC"
a_cli_json_has content_hash
a_cli_json_has commit_sha
a_cli_json_has decision
end_case

begin_case C06 "put 缺少 --id 报错"
run_cli put "${ENVARG[@]}" --subject alice
a_rc 1
a_cli_err 'put: --id is required'
end_case

begin_case C07 "put 匿名写入被拒绝"
run_cli put "${ENVARG[@]}" --id e2e/anon
a_rc 1
a_cli_err 'anonymous write rejected'
end_case

begin_case C08 "put 的 owner 与主体不一致被拒绝"
run_cli put "${ENVARG[@]}" --id e2e/owner-mismatch --subject alice --owner mallory
a_rc 1
a_cli_err 'owner does not match'
end_case

begin_case C09 "put 非法 visibility 被拒绝"
run_cli put "${ENVARG[@]}" --id e2e/bad-vis --subject alice --visibility bogus
a_rc 1
end_case

begin_case C10 "query 路由并执行查询"
run_cli query "${ENVARG[@]}" --q 'define churn' --subject alice --top-k 3
a_rc 0
a_cli_json_eq query 'define churn'
a_cli_json_has decision
a_cli_json_has hits
end_case

begin_case C11 "query 缺少 --q 报错"
run_cli query "${ENVARG[@]}" --subject alice
a_rc 1
a_cli_err 'query: --q is required'
end_case

begin_case C12 "migrate 执行增量对账"
run_cli migrate "${ENVARG[@]}" --job e2e-cli --direction canonical_to_rag \
  --target memory --limit 10
a_rc 0
a_cli_json_eq job e2e-cli
a_cli_json_eq direction canonical_to_rag
a_cli_json_eq target memory
a_cli_json_has done
end_case

begin_case C13 "migrate --dry-run 只做计划"
run_cli migrate "${ENVARG[@]}" --job e2e-dry --dry-run --limit 5
a_rc 0
a_cli_json_eq dry_run true
a_cli_json_has planned
end_case

begin_case C14 "migrate 非法方向报错"
run_cli migrate "${ENVARG[@]}" --direction wrong-way
a_rc 1
end_case

begin_case C15 "decisions 输出路由审计"
run_cli decisions "${ENVARG[@]}" --limit 10
a_rc 0
a_cli_json_len_min '' 1
a_cli_json_has 0.concept_id
end_case

begin_case C16 "concepts 列出概念 id"
run_cli concepts "${ENVARG[@]}"
a_rc 0
a_cli_json_len_min '' 1
end_case

begin_case C17 "compute 执行 Attested Computation"
run_cli compute "${ENVARG[@]}" --id "$COMPUTE_ID" --subject alice --param year=2026
a_rc 0
a_cli_json_eq concept_id "$COMPUTE_ID"
a_cli_json_eq display_allowed true
a_cli_json_has display_value
a_cli_json_eq verdict.passed true
end_case

begin_case C18 "compute 缺少 --id 报错"
run_cli compute "${ENVARG[@]}" --subject alice
a_rc 1
a_cli_err 'compute: --id is required'
end_case

begin_case C19 "compute 非法 --param 报错"
run_cli compute "${ENVARG[@]}" --id "$COMPUTE_ID" --subject alice --param year
a_rc 1
a_cli_err 'compute: --param must be name=value'
end_case

begin_case C20 "compute 非法参数不展示数值"
run_cli compute "${ENVARG[@]}" --id "$COMPUTE_ID" --subject alice --param year=notanint
a_rc 1
end_case

begin_case C21 "compute 不支持的 runtime 被拒绝"
run_cli compute "${ENVARG[@]}" --id "$UNSUPPORTED_ID" --subject alice --param year=2026
a_rc 1
a_cli_err 'has a contract but no executor'
end_case

begin_case C22 "computations 输出计算审计"
run_cli computations "${ENVARG[@]}" --id "$COMPUTE_ID" --limit 10
a_rc 0
a_cli_json_len_min '' 1
a_cli_json_has 0.concept_id
end_case

begin_case C23 "eval 输出排名指标"
run_cli eval --dataset "$REPO/testdata/eval.jsonl" --k 3
a_rc 0
a_cli_json_eq k 3
a_cli_json_eq samples 4
a_cli_json_has recall_at_k
a_cli_json_has precision_at_k
a_cli_json_has ndcg_at_k
a_cli_json_has mrr
a_cli_json_has map
a_cli_json_has hit_rate_at_k
end_case

begin_case C24 "eval 使用默认数据集路径"
run_cli eval
a_rc 0
a_cli_json_eq k 5
end_case

printf '{not json\n' > "$WORK/bad.jsonl"
begin_case C25 "eval 遇到坏 JSONL 报错"
run_cli eval --dataset "$WORK/bad.jsonl"
a_rc 1
a_cli_err 'eval: line 1'
end_case

begin_case C26 "eval 数据集不存在报错"
run_cli eval --dataset "$WORK/missing.jsonl"
a_rc 1
a_cli_err 'no such file or directory'
end_case

begin_case C27 "eval 不接受 --env（唯一不读配置的命令）"
run_cli eval --env "$BASE_ENV"
a_rc 1
a_cli_err 'flag provided but not defined'
end_case

begin_case C28 "concepts 未知 flag 报错"
run_cli concepts "${ENVARG[@]}" --bogus
a_rc 1
a_cli_err 'flag provided but not defined'
end_case

# ===========================================================================
section "3. HTTP API 全路由（鉴权关闭档位）"
# ===========================================================================
if start_server base "$BASE_ENV" "$PORT_BASE"; then
  begin_case H01 "serve 以离线 fake 启动"
  has "$(head -c 500 "$WORK/serve-base.log")" 'postgres=offline fake' '启动日志'
  end_case

  hc H02 "GET /healthz 报告依赖健康" GET /healthz '' 200
  a_json_eq status ok
  a_json_eq postgres 'fake (offline)'
  a_json_eq 'adapter:okf' ok
  a_json_eq 'adapter:memory' ok
  a_json_eq compute_runtimes 'bigquery,dbt,looker,postgres,python'
  a_header Content-Type 'application/json; charset=utf-8'
  end_case

  hc H03 "POST /v1/knowledge 写入概念" POST /v1/knowledge \
    '{"concept_id":"e2e/http-doc","document":{"type":"Concept","title":"E2E HTTP","description":"written over HTTP","body":"# E2E","tags":["e2e"],"visibility":"public"}}' 200
  a_json_eq concept_id "$HTTP_DOC"
  a_json_has content_hash
  a_json_has commit_sha
  a_json_has decision
  end_case

  hc H04 "GET /v1/knowledge 读取规范正文" GET '/v1/knowledge?id=e2e%2Fhttp-doc' '' 200
  a_json_eq concept_id "$HTTP_DOC"
  a_json_eq type Concept
  a_json_eq title 'E2E HTTP'
  a_json_eq visibility public
  a_json_has body
  end_case

  hc H05 "POST /v1/query 双路检索" POST /v1/query '{"query":"define churn","top_k":3}' 200
  a_json_eq query 'define churn'
  a_json_has decision
  a_json_has hits
  end_case

  hc H06 "POST /v1/migrate 增量对账" POST /v1/migrate \
    '{"job":"e2e-http","direction":"canonical_to_rag","target":"memory","dry_run":true,"limit":10}' 200
  a_json_eq job e2e-http
  a_json_eq dry_run true
  a_json_has done
  a_json_has scanned
  end_case

  hc H07 "GET /v1/decisions 路由审计" GET '/v1/decisions?limit=5' '' 200
  a_json_len_min '' 1
  a_json_has 0.concept_id
  end_case

  hc H08 "GET /v1/concepts 概念列表" GET /v1/concepts '' 200
  a_json_len_min '' 1
  end_case

  hc H09 "POST /v1/computations 成功展示已验证数值" POST /v1/computations \
    '{"concept_id":"computations/revenue-postgres","parameters":{"year":2026}}' 200
  a_json_eq concept_id "$COMPUTE_ID"
  a_json_eq display_allowed true
  a_json_eq runtime postgres
  a_json_has display_value
  a_json_eq verdict.passed true
  a_json_eq verdict.provenance true
  a_json_eq verdict.fidelity true
  end_case

  hc H10 "GET /v1/computations 计算审计" GET '/v1/computations?limit=5' '' 200
  a_json_len_min '' 1
  end_case

  hc H11 "错误方法返回 405" POST /healthz '' 405
  a_json_eq error 'method not allowed'
  end_case

  hc H12 "另一条路由的错误方法也返回 405" DELETE /v1/knowledge '' 405
  a_json_eq error 'method not allowed'
  end_case

  hc H13 "缺少 JSON Content-Type 被拒绝" POST /v1/query '{"query":"x"}' 400 NO_CT
  a_json_eq error 'Content-Type must be application/json'
  end_case

  hc H14 "非 JSON Content-Type 被拒绝" POST /v1/query '{"query":"x"}' 400 'Content-Type: text/plain'
  a_json_eq error 'Content-Type must be application/json'
  end_case

  hc H15 "未知字段被拒绝" POST /v1/query '{"query":"x","bogus":1}' 400
  a_json_eq error 'invalid JSON'
  end_case

  hc H16 "trailing data 被拒绝" POST /v1/query '{"query":"x"} {"a":1}' 400
  a_json_eq error 'unexpected trailing data'
  end_case

  hc H17 "畸形 JSON 被拒绝" POST /v1/query '{' 400
  a_json_eq error 'invalid JSON'
  end_case

  python3 -c 'import sys; sys.stdout.write("{\"query\":\"" + "x" * (4 * 1024 * 1024 + 1024) + "\"}")' > "$WORK/big.json"
  hc H18 "超过 4 MiB 的请求体被拒绝" POST /v1/query "@$WORK/big.json" 400
  a_json_eq error 'invalid JSON'
  end_case

  hc H19 "top_k 越界被拒绝" POST /v1/query '{"query":"x","top_k":101}' 400
  a_json_eq error 'top_k is out of range'
  end_case

  hc H20 "空 query 被拒绝" POST /v1/query '{"query":""}' 400
  a_json_eq error 'query is empty'
  end_case

  begin_case H21 "limit 的三种越界值都被拒绝"
  http_req GET '/v1/decisions?limit=0' ''
  eq "$STATUS" 400 'limit=0'
  http_req GET '/v1/decisions?limit=1001' ''
  eq "$STATUS" 400 'limit=1001'
  http_req GET '/v1/decisions?limit=abc' ''
  eq "$STATUS" 400 'limit=abc'
  a_json_eq error 'limit is out of range'
  end_case

  hc H22 "GET /v1/knowledge 缺少 id 被拒绝" GET /v1/knowledge '' 400
  a_json_eq error 'id is required'
  end_case

  hc H23 "POST /v1/knowledge 缺少必填字段被拒绝" POST /v1/knowledge \
    '{"concept_id":"","document":{"type":""}}' 400
  a_json_eq error 'concept_id and document.type are required'
  end_case

  hc H24 "POST /v1/computations 缺少 concept_id 被拒绝" POST /v1/computations \
    '{"parameters":{}}' 400
  a_json_eq error 'concept_id is required'
  end_case

  hc H25 "非法迁移参数被拒绝且错误已脱敏" POST /v1/migrate '{"direction":"wrong-way"}' 400
  a_json_eq error 'migration rejected'
  a_body_hasnt 'dsn'
  a_body_hasnt 'SELECT'
  end_case

  hc H26 "计算参数类型错误返回 400 且不展示数值" POST /v1/computations \
    '{"concept_id":"computations/revenue-postgres","parameters":{"year":"notanint"}}' 400
  a_json_eq error 'computation rejected'
  a_json_absent envelope.display_value
  a_body_hasnt 'SELECT'
  a_body_hasnt 'postgres://'
  end_case

  hc H27 "计算缺少必填参数返回 400" POST /v1/computations \
    '{"concept_id":"computations/revenue-postgres","parameters":{}}' 400
  a_json_eq error 'computation rejected'
  a_json_absent envelope.display_value
  end_case

  hc H28 "计算参数篡改 SQL 保留字段返回 400" POST /v1/computations \
    '{"concept_id":"computations/revenue-postgres","parameters":{"year":2026,"sql":"SELECT 1"}}' 400
  a_json_eq error 'computation rejected'
  a_json_absent envelope.display_value
  end_case

  hc H29 "不支持的 runtime 返回 422 且不展示数值" POST /v1/computations \
    '{"concept_id":"computations/revenue-ytd","parameters":{"year":2026}}' 422
  a_json_has envelope
  a_json_absent envelope.display_value
  a_body_hasnt 'SELECT'
  end_case

  hc H30 "不存在的计算概念返回 400" POST /v1/computations \
    '{"concept_id":"computations/does-not-exist","parameters":{}}' 400
  a_json_absent envelope.display_value
  end_case

  hc H31 "Go 二进制不托管 Web 静态资源" GET / '' 404
  a_header X-Content-Type-Options nosniff
  end_case

  hc H32 "私有文档与不存在的文档统一 404" GET '/v1/knowledge?id=e2e%2Fmissing' '' 404
  a_json_eq error 'document not found or access denied'
  end_case

  hc H33 "写入时 owner 与主体不一致被拒绝" POST /v1/knowledge \
    '{"concept_id":"e2e/owner-mismatch","document":{"type":"Concept","owner":"mallory"}}' 400
  a_json_eq error 'write rejected'
  end_case

  hc H34 "安全响应头齐备" GET /healthz '' 200
  a_header X-Content-Type-Options nosniff
  a_header X-Frame-Options DENY
  a_header Referrer-Policy no-referrer
  a_header Cache-Control no-store
  a_header Content-Security-Policy "default-src 'none'"
  end_case

  hc H35 "未知路径返回 404" GET /v1/not-a-route '' 404
  a_body_has '404 page not found'
  end_case
else
  fail_case H00 "HTTP 用例组" "服务未能启动: $(head -c 200 "$WORK/serve-base.log" | tr '\n' ' ')"
fi
stop_server

# ===========================================================================
section "4. 鉴权、权限矩阵与身份安全（鉴权开启档位）"
# ===========================================================================
if start_server auth "$AUTH_ENV" "$PORT_AUTH"; then
  hc S01 "GET /healthz 无需令牌" GET /healthz '' 200
  a_json_eq status ok
  end_case

  hc S02 "缺少令牌返回 401 并带 WWW-Authenticate" GET /v1/concepts '' 401
  a_json_eq error 'authentication required'
  a_header WWW-Authenticate 'Bearer realm="agentmem"'
  end_case

  hc S03 "未知令牌返回 401" GET /v1/concepts '' 401 "Authorization: Bearer not-a-real-token"
  a_json_eq error 'authentication required'
  end_case

  hc S04 "knowledge:read 只能读，不能写" GET /v1/concepts '' 200 "Authorization: Bearer $TOK_READ"
  http_req POST /v1/knowledge \
    '{"concept_id":"e2e/scope-denied","document":{"type":"Concept"}}' \
    "Authorization: Bearer $TOK_READ"
  eq "$STATUS" 403 'knowledge:read 写入'
  end_case

  hc S05 "knowledge:write 只能写，不能读" POST /v1/knowledge \
    '{"concept_id":"e2e/scope-write","document":{"type":"Concept","visibility":"public"}}' 200 "Authorization: Bearer $TOK_WRITE"
  http_req GET /v1/concepts '' "Authorization: Bearer $TOK_WRITE"
  eq "$STATUS" 403 'knowledge:write 读取'
  end_case

  hc S06 "migrate:run 只能迁移，不能审计" POST /v1/migrate \
    '{"direction":"canonical_to_rag","target":"memory","dry_run":true,"limit":5}' 200 "Authorization: Bearer $TOK_MIGRATE"
  http_req GET /v1/decisions '' "Authorization: Bearer $TOK_MIGRATE"
  eq "$STATUS" 403 'migrate:run 审计'
  end_case

  hc S07 "audit:read 只能审计，不能查询" GET /v1/decisions '' 200 "Authorization: Bearer $TOK_AUDIT"
  http_req POST /v1/query '{"query":"x"}' "Authorization: Bearer $TOK_AUDIT"
  eq "$STATUS" 403 'audit:read 查询'
  end_case

  hc S08 "compute:run 只能计算，不能读审计" POST /v1/computations \
    '{"concept_id":"computations/revenue-postgres","parameters":{"year":2026}}' 200 "Authorization: Bearer $TOK_COMPUTE"
  a_json_eq display_allowed true
  http_req GET /v1/computations '' "Authorization: Bearer $TOK_COMPUTE"
  eq "$STATUS" 403 'compute:run 审计'
  end_case

  hc S09 "health:read 不足以访问受保护路由" GET /v1/concepts '' 403 "Authorization: Bearer $TOK_HEALTH"
  a_json_eq error 'permission denied'
  end_case

  hc S10 "全 scope 令牌可访问读写与审计" GET /v1/knowledge?id=e2e%2Fhttp-doc '' 200 "Authorization: Bearer $TOK_ALL"
  a_json_eq concept_id "$HTTP_DOC"
  http_req GET /v1/computations '' "Authorization: Bearer $TOK_ALL"
  eq "$STATUS" 200 '全 scope 计算审计'
  end_case

  hc S11 "请求体中的 subject 伪造被拒绝" POST /v1/query \
    '{"query":"x","subject":"mallory"}' 400 "Authorization: Bearer $TOK_ALL"
  a_json_eq error 'invalid JSON'
  end_case

  hc S12 "写入私有文档" POST /v1/knowledge \
    '{"concept_id":"e2e/private-doc","document":{"type":"Concept","title":"Private","visibility":"private"}}' 200 "Authorization: Bearer $TOK_ALL"
  a_json_eq concept_id 'e2e/private-doc'
  end_case

  hc S13 "他人读取私有文档统一 404" GET '/v1/knowledge?id=e2e%2Fprivate-doc' '' 404 "Authorization: Bearer $TOK_READ"
  a_json_eq error 'document not found or access denied'
  end_case

  hc S14 "owner 本人可读私有文档" GET '/v1/knowledge?id=e2e%2Fprivate-doc' '' 200 "Authorization: Bearer $TOK_ALL"
  a_json_eq visibility private
  end_case

  hc S15 "查询串中的 subject 伪造不改变身份" GET '/v1/knowledge?id=e2e%2Fprivate-doc&subject=alice' '' 404 "Authorization: Bearer $TOK_READ"
  a_json_eq error 'document not found or access denied'
  end_case

  hc S16 "Origin: null 被拒绝" GET /healthz '' 403 'Origin: null'
  a_json_eq error 'origin not allowed'
  end_case

  hc S17 "未列入白名单的跨站 Origin 被拒绝" GET /v1/concepts '' 403 \
    'Origin: https://evil.example.test' "Authorization: Bearer $TOK_ALL"
  a_json_eq error 'origin not allowed'
  end_case

  hc S18 "白名单内的跨站 Origin 被允许" GET /v1/concepts '' 200 \
    'Origin: https://console.example.test' "Authorization: Bearer $TOK_ALL"
  end_case

  hc S19 "405 判定发生在鉴权之后" POST /healthz '' 401
  a_json_eq error 'authentication required'
  http_req POST /healthz '' "Authorization: Bearer $TOK_ALL"
  eq "$STATUS" 405 '带令牌的错误方法'
  end_case

  hc S20 "错误响应不回显凭据或 DSN" GET /v1/concepts '' 401
  a_body_hasnt 'postgres://'
  a_body_hasnt 'e2e-all-'
  a_body_hasnt 'SELECT'
  end_case
else
  fail_case S00 "鉴权用例组" "服务未能启动: $(head -c 200 "$WORK/serve-auth.log" | tr '\n' ' ')"
fi
stop_server

# ===========================================================================
section "5. 限流（独立低限额档位）"
# ===========================================================================
if start_server rate "$RATE_ENV" "$PORT_RATE"; then
  begin_case R01 "超过令牌桶速率返回 429"
  rate_found=0
  for _i in $(seq 1 30); do
    http_req GET /healthz ''
    if [[ "$STATUS" == "429" ]]; then
      rate_found=1
      cp "$BODY" "$WORK/rate429.body"
      break
    fi
  done
  if [[ "$rate_found" -eq 1 ]]; then ck 0 ""; else ck 1 "连续 30 次请求未出现 429"; fi
  has "$(head -c 500 "$WORK/rate429.body")" 'rate limit exceeded' '429 响应体'
  end_case
else
  fail_case R00 "限流用例组" "服务未能启动: $(head -c 200 "$WORK/serve-rate.log" | tr '\n' ' ')"
fi
stop_server

# ===========================================================================
section "6. 跨进程状态一致性（所有服务已停止）"
# ===========================================================================
begin_case X01 "CLI concepts 能看到 HTTP 写入的概念"
run_cli concepts "${ENVARG[@]}"
a_rc 0
a_cli_json_contains '' "$HTTP_DOC"
end_case

begin_case X02 "CLI decisions 保留完整审计轨迹"
run_cli decisions "${ENVARG[@]}" --limit 100
a_rc 0
a_cli_json_len_min '' 1
a_cli_json_has 0.concept_id
end_case

begin_case X03 "CLI computations 保留 HTTP 计算留痕"
run_cli computations "${ENVARG[@]}" --limit 100
a_rc 0
a_cli_json_len_min '' 1
a_cli_json_has 0.verdict
end_case

begin_case X04 "离线快照落盘且为合法 JSON"
if [[ -f "$WORK/meta.fake.json" ]]; then ck 0 ""; else ck 1 '缺少 meta.fake.json'; fi
python3 -c 'import json,sys; json.load(open(sys.argv[1], encoding="utf-8"))' \
  "$WORK/meta.fake.json" >/dev/null 2>&1
ck $? '快照不是合法 JSON'
end_case

begin_case X05 "OKF+Git 正文写入临时 bundle"
e2e_md=0
if [[ -d "$WORK/bundle/e2e" ]]; then
  e2e_md="$(find "$WORK/bundle/e2e" -type f -name '*.md' | wc -l)"
fi
if [[ "$e2e_md" -ge 1 ]]; then ck 0 ""; else ck 1 'bundle 中未找到 e2e 概念正文'; fi
end_case

# ===========================================================================
section "7. Web 控制台（静态自检，不含浏览器 UI 自动化）"
# ===========================================================================
begin_case W01 "index.html 自带严格 CSP"
web_html="$(cat "$REPO/web/index.html")"
has "$web_html" 'http-equiv="Content-Security-Policy"' 'CSP meta'
has "$web_html" "default-src 'none'" 'default-src'
end_case

begin_case W02 "无外链 CDN 资源"
if grep -Eq '(src|href)="https?://' "$REPO/web/index.html"; then
  ck 1 'index.html 引用了外部资源'
else
  ck 0 ''
fi
end_case

begin_case W03 "API 文档镜像 9 条 method+path"
web_routes="$(grep -c "    method: '" "$REPO/web/src/lib/api-content.js")"
eq "$web_routes" 9 '端点条数'
end_case

begin_case W04 "13 语言目录齐备"
web_langs="$(cat "$REPO/web/src/lib/languages.js")"
has "$web_langs" 'zh-TW' '繁体中文'
has "$web_langs" 'hi' '印地语'
web_tests="$(ls "$REPO"/web/tests/*.test.mjs 2>/dev/null | wc -l)"
eq "$web_tests" 7 '离线测试文件数'
end_case

if command -v bun >/dev/null 2>&1; then
  begin_case W05 "bun test（Web 离线 node:test 套件）"
  (cd "$REPO/web" && bun test) >"$WORK/web-test.log" 2>&1
  web_test_rc=$?
  ck "$web_test_rc" "bun test 退出码 $web_test_rc: $(tail -n 3 "$WORK/web-test.log" | tr '\n' ' ' | head -c 200)"
  end_case
else
  skip_case W05 "bun test（Web 离线 node:test 套件）" "bun 未安装"
fi

# ===========================================================================
section "8. 现有质量门禁"
# ===========================================================================
run_gate() { # ID DESC COMMAND...
  local id="$1" desc="$2"
  shift 2
  begin_case "$id" "$desc"
  "$@" >"$WORK/gate-$id.log" 2>&1
  local rc=$?
  ck "$rc" "退出码 $rc: $(tail -n 3 "$WORK/gate-$id.log" | tr '\n' ' ' | head -c 200)"
  end_case
}

if [[ "$NO_GATES" -eq 1 ]]; then
  skip_case G00 "现有质量门禁" "--no-gates 已指定"
else
  run_gate G02 "go vet ./..." go vet ./...
  run_gate G03 "go test ./... -count=1（离线）" go test ./... -count=1
  run_gate G04 "go build ./..." go build ./...
  run_gate G05 "make validate（严格校验 .env.example）" make validate
  run_gate G06 "make check-boundaries" make check-boundaries
  run_gate G07 "make eval" make eval
  if command -v openssl >/dev/null 2>&1; then
    run_gate G08 "make check-trust-store" make check-trust-store
  else
    skip_case G08 "make check-trust-store" "openssl 未安装"
  fi
fi

# ===========================================================================
section "汇总"
# ===========================================================================
printf 'PASS=%d  FAIL=%d  SKIP=%d  TOTAL=%d\n' "$PASS" "$FAIL" "$SKIP" "$((PASS + FAIL + SKIP))"
if (( FAIL > 0 )); then
  printf '结论: FAIL\n'
  exit 1
fi
printf '结论: PASS\n'
exit 0
