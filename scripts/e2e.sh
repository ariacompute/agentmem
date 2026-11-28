#!/usr/bin/env bash
# e2e.sh — agentmem black-box acceptance script from the user's perspective.
#
# It does only three things: build the real binary, invoke the CLI as a child process, and use curl to hit the real HTTP server.
# It imports no internal packages, modifies no production code, and depends on no Postgres / network / credentials.
# All runtime artifacts (binary, .env, bundle copy, snapshots, response bodies, logs) live in a mktemp
# temporary directory, reclaimed by a trap on exit; --keep preserves it for troubleshooting.
#
# Usage:
#   scripts/e2e.sh                     # full run (including existing quality gates)
#   scripts/e2e.sh --no-gates          # skip the slow G02-G08 gates; G09/G10 still run
#   scripts/e2e.sh --filter '^H'       # report only the HTTP group
#   scripts/e2e.sh --keep              # keep the temporary directory
#   make e2e                           # repository gate entry point
#
# Exit codes: 0 = all passed; 1 = some case failed; 2 = usage/environment/build error.
# When optional tools (bun, openssl) are missing, the corresponding item is marked SKIP, never silently passed.

set -uo pipefail

REPO="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)" || exit 2
KEEP=0
NO_GATES=0
FILTER=""

usage() {
  cat <<'USAGE'
Usage: scripts/e2e.sh [--no-gates] [--keep] [--filter REGEX] [--help]

  --no-gates       skip go vet / go test / make gates (binary build always runs);
                   G09/G10, which prove the coverage gate itself, still run
  --keep           keep the temporary working directory for troubleshooting
  --filter REGEX   report only cases whose ID matches this regex (e.g. '^H', '^(C|S)')
  --help           show this help

Exit codes: 0 all passed; 1 some case failed; 2 usage/environment/build error.
USAGE
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    -h|--help) usage; exit 0 ;;
    --keep) KEEP=1 ;;
    --no-gates) NO_GATES=1 ;;
    --filter)
      if [[ $# -lt 2 ]]; then echo "error: --filter requires a regex argument" >&2; exit 2; fi
      FILTER="$2"; shift ;;
    --filter=*) FILTER="${1#*=}" ;;
    *) echo "error: unknown argument $1" >&2; usage >&2; exit 2 ;;
  esac
  shift
done

# --- Environment preflight ---------------------------------------------------
for tool in go curl python3 sed grep awk sort uniq sha256sum timeout; do
  if ! command -v "$tool" >/dev/null 2>&1; then
    echo "error: missing required tool $tool" >&2
    exit 2
  fi
done
cd "$REPO" || { echo "error: cannot enter repository root" >&2; exit 2; }

# A case ID is the public handle used by --filter and the documented matrix, so two
# cases may never share one: a duplicate would silently report the wrong group.
dup_ids="$(grep -o 'begin_case [A-Z][0-9][0-9]' "$REPO/scripts/e2e.sh" | awk '{print $2}' | sort | uniq -d)"
if [[ -n "$dup_ids" ]]; then
  echo "error: duplicate case IDs: $(echo "$dup_ids" | tr '\n' ' ')" >&2
  exit 2
fi
for f in go.mod Makefile .env.example testdata/eval.jsonl testdata/bundle/index.md \
         web/index.html web/src/lib/api-content.js internal/webui/dist/index.html; do
  if [[ ! -f "$f" ]]; then echo "error: missing $f" >&2; exit 2; fi
done

# config.Load overlays the process environment on top of .env and rejects any unknown AGENTMEM_* process keys.
# Clear inherited values first so each case is controlled only by its own env file.
for _k in $(env | sed -n 's/^\(AGENTMEM_[A-Za-z0-9_]*\)=.*/\1/p'); do
  unset "$_k"
done
unset _k
export GOPROXY=off   # offline throughout: use only the local module cache, never the network

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

# --- Compute-plane sidecar stubs (local python3 implementing wire protocol v1, never networked) ---
SIDECAR_DIR="$WORK/sidecar"
mkdir -p "$SIDECAR_DIR" || exit 2
cat > "$SIDECAR_DIR/agentmem-py" <<'PY'
#!/usr/bin/env python3
"""CLI sidecar stub: reads the JSON request on stdin, writes the JSON reply."""
import hashlib
import json
import sys

req = json.load(sys.stdin)
body = req.get("body", "")
print(json.dumps({
    "columns": ["revenue"],
    "rows": [[1250]],
    "body_digest": hashlib.sha256(body.encode("utf-8")).hexdigest(),
}))
PY
cat > "$SIDECAR_DIR/agentmem-fail" <<'SH'
#!/bin/sh
# Always fails: the CLI sidecar channel must surface a typed, redacted error.
exit 3
SH
cat > "$SIDECAR_DIR/http_sidecar.py" <<'PY'
#!/usr/bin/env python3
"""HTTP sidecar stub implementing POST /run of the v1 wire protocol."""
import hashlib
import json
import sys
from http.server import BaseHTTPRequestHandler, HTTPServer


class Handler(BaseHTTPRequestHandler):
    def do_POST(self):
        length = int(self.headers.get("Content-Length") or 0)
        req = json.loads(self.rfile.read(length) or b"{}")
        body = req.get("body", "")
        payload = json.dumps({
            "columns": ["revenue"],
            "rows": [[1250]],
            "body_digest": hashlib.sha256(body.encode("utf-8")).hexdigest(),
        }).encode("utf-8")
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(payload)))
        self.end_headers()
        self.wfile.write(payload)

    def log_message(self, *args):
        pass


HTTPServer(("127.0.0.1", int(sys.argv[1])), Handler).serve_forever()
PY
# The shipped reference sidecar covers bigquery / dbt / looker without a
# network: SIDECAR_OFFLINE=1 makes it return deterministic per-runtime tables.
# It is copied instead of symlinked so the executable bit is explicit.
cp "$REPO/examples/compute-sidecars/sidecar.py" "$SIDECAR_DIR/agentmem-example"
chmod +x "$SIDECAR_DIR/agentmem-py" "$SIDECAR_DIR/agentmem-fail" "$SIDECAR_DIR/agentmem-example"
SIDECAR_PID=""
ENGINE_PID=""

# --- Reporting framework ----------------------------------------------------
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

eq() { if [[ "$1" == "$2" ]]; then ck 0 ""; else ck 1 "$3: expected '$2', got '$1'"; fi; }
# A lower bound keeps asset counts from becoming a magic number that every
# added file has to bump; the suite itself still proves the files work.
ge() { if [[ "$1" -ge "$2" ]]; then ck 0 ""; else ck 1 "$3: expected at least '$2', got '$1'"; fi; }
has() { if [[ "$1" == *"$2"* ]]; then ck 0 ""; else ck 1 "$3: does not contain '$2'"; fi; }
hasnt() { if [[ "$1" != *"$2"* ]]; then ck 0 ""; else ck 1 "$3: should not contain '$2'"; fi; }

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

# A server that fails to start is a fault in the system under test; it must not be SKIPped like a missing optional tool.
fail_case() {
  begin_case "$1" "$2"
  [[ "$CASE_ACTIVE" -eq 1 ]] || return 0
  printf '  FAIL %-4s %s (%s)\n' "$1" "$2" "$3"
  FAIL=$((FAIL + 1))
  CASE_ACTIVE=0
}

# --- JSON assertions (python3 standard library, replacing jq) -----------------
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
a_json_has() { jexists "$BODY" "$1"; ck $? "JSON missing $1"; }
a_json_absent() { if jexists "$BODY" "$1"; then ck 1 "JSON should not have $1"; else ck 0 ""; fi; }
a_json_contains() { jcontains "$BODY" "$1" "$2"; ck $? "JSON $1 does not contain '$2'"; }
a_json_len_min() {
  local n; n="$(jlen "$BODY" "$1")"
  if [[ "$n" =~ ^[0-9]+$ ]] && [[ "$n" -ge "$2" ]]; then ck 0 ""; else ck 1 "JSON $1 length expected >=$2, got $n"; fi
}
a_body_has() { has "$(head -c 2000 "$BODY")" "$1" "response body"; }
a_body_hasnt() { hasnt "$(head -c 4000 "$BODY")" "$1" "response body"; }

header_value() {
  grep -i "^$1:" "$HEADERS" 2>/dev/null | head -n 1 | sed -e 's/^[^:]*:[[:space:]]*//' -e 's/\r$//'
}
a_header() {
  local v; v="$(header_value "$1")"
  if [[ "$v" == *"$2"* ]]; then ck 0 ""; else ck 1 "response header $1 expected to contain '$2', got '$v'"; fi
}

# --- HTTP / CLI primitives ---------------------------------------------------
STATUS="000"

http_req() { # METHOD PATH [BODY|@FILE] [HEADER...]; NO_CT suppresses the default Content-Type
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

a_status() { eq "$STATUS" "$1" "HTTP status"; }

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
run_cli_env() { # VAR=VALUE ARGS... (injected only into the child process, for process-env allowlist cases)
  local var="$1"; shift
  env "$var" "$BIN" "$@" >"$CLI_OUT" 2>"$CLI_ERR"; CLI_RC=$?
}
a_rc() { eq "$CLI_RC" "$1" "CLI exit code"; }
a_cli_out() { has "$(head -c 2000 "$CLI_OUT")" "$1" "CLI stdout"; }
a_cli_err() { has "$(head -c 2000 "$CLI_ERR")" "$1" "CLI stderr"; }
a_cli_json_eq() { eq "$(jget "$CLI_OUT" "$1")" "$2" "CLI JSON $1"; }
a_cli_json_has() { jexists "$CLI_OUT" "$1"; ck $? "CLI JSON missing $1"; }
a_cli_json_absent() { if jexists "$CLI_OUT" "$1"; then ck 1 "CLI JSON should not have $1"; else ck 0 ""; fi; }
a_cli_json_len_min() {
  local n; n="$(jlen "$CLI_OUT" "$1")"
  if [[ "$n" =~ ^[0-9]+$ ]] && [[ "$n" -ge "$2" ]]; then ck 0 ""; else ck 1 "CLI JSON $1 length expected >=$2, got $n"; fi
}
a_cli_json_contains() { jcontains "$CLI_OUT" "$1" "$2"; ck $? "CLI JSON $1 does not contain '$2'"; }

# --- Server management -------------------------------------------------------
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

stop_http_sidecar() {
  if [[ -n "$SIDECAR_PID" ]]; then
    kill -TERM "$SIDECAR_PID" 2>/dev/null || true
    wait "$SIDECAR_PID" 2>/dev/null || true
    SIDECAR_PID=""
  fi
}

stop_engine() {
  if [[ -n "$ENGINE_PID" ]]; then
    kill -TERM "$ENGINE_PID" 2>/dev/null || true
    wait "$ENGINE_PID" 2>/dev/null || true
    ENGINE_PID=""
  fi
}

cleanup() {
  stop_engine
  stop_http_sidecar
  stop_server
  if [[ "$KEEP" -eq 1 ]]; then
    printf '\nTemporary working directory kept: %s\n' "$WORK"
  else
    rm -rf "$WORK"
  fi
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

# --- Configuration setup -----------------------------------------------------
PORTS="$(python3 -c '
import socket
socks = []
for _ in range(10):
    s = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
    s.bind(("127.0.0.1", 0))
    socks.append(s)
print(" ".join(str(s.getsockname()[1]) for s in socks))
')" || { echo "error: cannot allocate local ports" >&2; exit 2; }
read -r PORT_BASE PORT_AUTH PORT_RATE PORT_SIDE PORT_CONSOLE PORT_GRAPH_OFF \
  PORT_ONTOLOGY PORT_REMOTE PORT_ENGINE PORT_MCP_CAP <<< "$PORTS"

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
    # Do not write AGENTMEM_AUTH_ENABLED here: the default is already false and the auth profile overrides it;
    # duplicate keys are treated as an error by the dotenv parser.
    for line in "$@"; do printf '%s\n' "$line"; done
  } > "$out"
}

bad_env() { # NAME [LINE...]; write only required keys, the rest fall back to Default()
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
TOK_GRAPH="e2e-graph-$STAMP"

AUTH_JSON='['
AUTH_JSON+='{"token_sha256":"'"$(digest "$TOK_ALL")"'","subject":"alice","scopes":["health:read","knowledge:read","knowledge:write","migrate:run","audit:read","compute:run"]},'
AUTH_JSON+='{"token_sha256":"'"$(digest "$TOK_READ")"'","subject":"bob","scopes":["knowledge:read"]},'
AUTH_JSON+='{"token_sha256":"'"$(digest "$TOK_WRITE")"'","subject":"carol","scopes":["knowledge:write"]},'
AUTH_JSON+='{"token_sha256":"'"$(digest "$TOK_MIGRATE")"'","subject":"dave","scopes":["migrate:run"]},'
AUTH_JSON+='{"token_sha256":"'"$(digest "$TOK_AUDIT")"'","subject":"erin","scopes":["audit:read"]},'
AUTH_JSON+='{"token_sha256":"'"$(digest "$TOK_COMPUTE")"'","subject":"frank","scopes":["compute:run"]},'
AUTH_JSON+='{"token_sha256":"'"$(digest "$TOK_HEALTH")"'","subject":"grace","scopes":["health:read"]},'
AUTH_JSON+='{"token_sha256":"'"$(digest "$TOK_GRAPH")"'","subject":"nina","scopes":["graph:write"]}'
AUTH_JSON+=']'

PROD_JSON='[{"token_sha256":"'"$(digest "e2e-prod-$STAMP")"'","subject":"prod","scopes":["knowledge:read"]}]'

# Remote-engine profile: reads need knowledge:read, rejected writes need
# graph:write and the rejected rebuild needs migrate:run, so the scope check
# cannot mask the backend-level rejection.
TOK_ENGINE="e2e-engine-client-$STAMP"
REMOTE_AUTH_JSON='[{"token_sha256":"'"$(digest "$TOK_ENGINE")"'","subject":"alice",'
REMOTE_AUTH_JSON+='"scopes":["knowledge:read","graph:write","migrate:run"]}]'
PROD_DSN='postgres://e2e:placeholder@db.example.test:5432/agentmem?sslmode=require'

make_env "$BASE_ENV" "$PORT_BASE" 100000 1000000
make_env "$AUTH_ENV" "$PORT_AUTH" 100000 1000000 \
  'AGENTMEM_AUTH_ENABLED=true' \
  "AGENTMEM_AUTH_TOKENS='$AUTH_JSON'" \
  'AGENTMEM_ALLOWED_ORIGINS=https://console.example.test' \
  'AGENTMEM_MCP_WRITE=true'
make_env "$RATE_ENV" "$PORT_RATE" 1 5

# Compute-plane sidecar config: the python runtime goes through a CLI child process and a local HTTP loopback stub.
SIDECAR_ENV="$WORK/sidecar-cli.env"
SIDECAR_HTTP_ENV="$WORK/sidecar-http.env"
SIDECAR_FAIL_ENV="$WORK/sidecar-fail.env"
make_env "$SIDECAR_ENV" "$PORT_BASE" 100000 1000000 \
  "AGENTMEM_COMPUTE_SIDECARS='[{\"runtime\":\"python\",\"bridge\":\"cli\",\"command\":\"agentmem-py\",\"timeout_ms\":5000}]'"
make_env "$SIDECAR_HTTP_ENV" "$PORT_BASE" 100000 1000000 \
  'AGENTMEM_EGRESS_ALLOWED_HOSTS=127.0.0.1' \
  "AGENTMEM_COMPUTE_SIDECARS='[{\"runtime\":\"python\",\"bridge\":\"http\",\"endpoint\":\"http://127.0.0.1:$PORT_SIDE\",\"timeout_ms\":5000}]'"
make_env "$SIDECAR_FAIL_ENV" "$PORT_BASE" 100000 1000000 \
  "AGENTMEM_COMPUTE_SIDECARS='[{\"runtime\":\"python\",\"bridge\":\"cli\",\"command\":\"agentmem-fail\",\"timeout_ms\":5000}]'"
# The reference sidecar (examples/compute-sidecars) serves the three runtimes
# that used to be covered only by the fail-closed path.
SIDECAR_MULTI_ENV="$WORK/sidecar-multi.env"
make_env "$SIDECAR_MULTI_ENV" "$PORT_BASE" 100000 1000000 \
  "AGENTMEM_COMPUTE_SIDECARS='[{\"runtime\":\"bigquery\",\"bridge\":\"cli\",\"command\":\"agentmem-example\",\"timeout_ms\":10000},{\"runtime\":\"dbt\",\"bridge\":\"cli\",\"command\":\"agentmem-example\",\"timeout_ms\":10000},{\"runtime\":\"looker\",\"bridge\":\"cli\",\"command\":\"agentmem-example\",\"timeout_ms\":10000}]'"

# API-only deployment: the embedded console is switched off at configuration
# time, so GET / and /assets/* must answer JSON 404 while the API keeps working.
CONSOLE_ENV="$WORK/console-off.env"
make_env "$CONSOLE_ENV" "$PORT_CONSOLE" 100000 1000000 'AGENTMEM_CONSOLE_ENABLED=false'

# One-session MCP profile: the transport must refuse the second initialize with the
# documented capacity error and keep the live session usable, then free the slot on
# DELETE. The default cap (128) cannot be reached from a fast acceptance run.
MCP_CAP_ENV="$WORK/mcp-cap.env"
make_env "$MCP_CAP_ENV" "$PORT_MCP_CAP" 100000 1000000 'AGENTMEM_MCP_MAX_SESSIONS=1'

# Typed ontology profile: the schema document is seeded into the bundle copy, so
# a mismatched declared relation must be rejected end to end.
ONTOLOGY_ENV="$WORK/ontology.env"
make_env "$ONTOLOGY_ENV" "$PORT_ONTOLOGY" 100000 1000000

# Graph switched off: every graph surface must answer the same JSON 404 as an
# unknown path while the rest of the API keeps working.
GRAPH_OFF_ENV="$WORK/graph-off.env"
make_env "$GRAPH_OFF_ENV" "$PORT_GRAPH_OFF" 100000 1000000 'AGENTMEM_GRAPH_ENABLED=false'

# Remote graph engine: reads are proxied to a loopback stub, writes and rebuilds
# are rejected because the engine owns a copy we cannot update.
REMOTE_ENV="$WORK/remote.env"
make_env "$REMOTE_ENV" "$PORT_REMOTE" 100000 1000000 \
  'AGENTMEM_GRAPH_BACKEND=remote' \
  "AGENTMEM_GRAPH_REMOTE_URL=http://127.0.0.1:$PORT_ENGINE" \
  'AGENTMEM_GRAPH_REMOTE_TOKEN_ENV=GRAPH_ENGINE_TOKEN' \
  'AGENTMEM_GRAPH_REMOTE_TIMEOUT_MS=5000' \
  'AGENTMEM_EGRESS_ALLOWED_HOSTS=127.0.0.1' \
  'AGENTMEM_AUTH_ENABLED=true' \
  "AGENTMEM_AUTH_TOKENS='$REMOTE_AUTH_JSON'"

# Same backend, but the endpoint host is outside the egress allowlist: startup
# must fail instead of silently falling back to the builtin engine.
REMOTE_BAD_HOST_ENV="$WORK/remote-bad-host.env"
make_env "$REMOTE_BAD_HOST_ENV" "$PORT_REMOTE" 100000 1000000 \
  'AGENTMEM_GRAPH_BACKEND=remote' \
  "AGENTMEM_GRAPH_REMOTE_URL=http://127.0.0.1:$PORT_ENGINE" \
  'AGENTMEM_GRAPH_REMOTE_TOKEN_ENV=GRAPH_ENGINE_TOKEN' \
  'AGENTMEM_EGRESS_ALLOWED_HOSTS=graph.internal'

# Allowlisted endpoint with an unset token variable: also a startup failure.
REMOTE_NO_TOKEN_ENV="$WORK/remote-no-token.env"
make_env "$REMOTE_NO_TOKEN_ENV" "$PORT_REMOTE" 100000 1000000 \
  'AGENTMEM_GRAPH_BACKEND=remote' \
  "AGENTMEM_GRAPH_REMOTE_URL=http://127.0.0.1:$PORT_ENGINE" \
  'AGENTMEM_GRAPH_REMOTE_TOKEN_ENV=GRAPH_ENGINE_TOKEN' \
  'AGENTMEM_EGRESS_ALLOWED_HOSTS=127.0.0.1'

GRAPH_ENGINE_TOKEN="e2e-engine-$RANDOM$RANDOM$$"
ENGINE_LOG="$WORK/engine.log"
: > "$ENGINE_LOG"

ENVARG=(--env "$BASE_ENV")
PY_COMPUTE_ID='computations/revenue-python'
BQ_COMPUTE_ID='computations/revenue-ytd'
DBT_COMPUTE_ID='computations/revenue-dbt'
LOOKER_COMPUTE_ID='computations/revenue-looker'
HTTP_DOC='e2e/http-doc'
CLI_DOC='e2e/cli-doc'
COMPUTE_ID='computations/revenue-postgres'
UNSUPPORTED_ID='computations/revenue-ytd'

printf 'agentmem black-box acceptance (user perspective)\nRepository: %s\nWorkspace: %s\n' "$REPO" "$WORK"

# ===========================================================================
section "0. Build"
# ===========================================================================
begin_case G01 "build the real binary offline"
if GOPROXY=off go build -o "$BIN" ./cmd/agentmem >"$WORK/build.log" 2>&1; then
  ck 0 ""
else
  ck 1 "go build failed: $(head -c 300 "$WORK/build.log")"
fi
end_case
if [[ ! -x "$BIN" ]]; then
  printf '\nCannot continue: binary build failed.\n' >&2
  exit 2
fi

# ===========================================================================
section "1. Configuration validation and fail-closed (CLI validate)"
# ===========================================================================
begin_case F01 "validate accepts the offline baseline config"
run_cli validate "${ENVARG[@]}"
a_rc 0
a_cli_json_eq status ok
a_cli_json_eq mode dev
a_cli_json_eq adapters 2
end_case

begin_case F02 "validate accepts the repo template .env.example"
run_cli validate --env "$REPO/.env.example"
a_rc 0
a_cli_json_eq status ok
a_cli_json_eq adapters 7
end_case

begin_case F03 "falls back to defaults when the env file is missing"
run_cli validate --env "$WORK/does-not-exist.env"
a_rc 0
a_cli_json_eq status ok
a_cli_json_eq mode dev
end_case

bad_env unknown-key 'AGENTMEM_TYPO_KEY=1'
begin_case F04 "unknown AGENTMEM_* key in env file is rejected"
run_cli validate --env "$WORK/bad-unknown-key.env"
a_rc 1
a_cli_err 'unknown keys AGENTMEM_TYPO_KEY'
end_case

begin_case F05 "unknown AGENTMEM_* key in process env is rejected"
run_cli_env 'AGENTMEM_TYPO_KEY=1' validate "${ENVARG[@]}"
a_rc 1
a_cli_err 'unknown process environment keys'
end_case

bad_env mode 'AGENTMEM_MODE=staging'
begin_case F06 "invalid mode is rejected"
run_cli validate --env "$WORK/bad-mode.env"
a_rc 1
a_cli_err 'must be dev or production'
end_case

bad_env prod-no-dsn 'AGENTMEM_MODE=production'
begin_case F07 "production without DSN is rejected"
run_cli validate --env "$WORK/bad-prod-no-dsn.env"
a_rc 1
a_cli_err 'postgres.dsn is required in production'
end_case

bad_env prod-no-auth 'AGENTMEM_MODE=production' "AGENTMEM_PG_DSN=$PROD_DSN"
begin_case F08 "production without auth is rejected"
run_cli validate --env "$WORK/bad-prod-no-auth.env"
a_rc 1
a_cli_err 'production requires AGENTMEM_AUTH_ENABLED=true'
end_case

bad_env prod-insecure 'AGENTMEM_MODE=production' \
  'AGENTMEM_PG_DSN=postgres://e2e:placeholder@db.example.test:5432/agentmem?sslmode=disable' \
  'AGENTMEM_AUTH_ENABLED=true' "AGENTMEM_AUTH_TOKENS='$PROD_JSON'"
begin_case F09 "production rejects sslmode=disable"
run_cli validate --env "$WORK/bad-prod-insecure.env"
a_rc 1
a_cli_err 'must not disable TLS'
end_case

bad_env prod-ok 'AGENTMEM_MODE=production' "AGENTMEM_PG_DSN=$PROD_DSN" \
  'AGENTMEM_AUTH_ENABLED=true' "AGENTMEM_AUTH_TOKENS='$PROD_JSON'"
begin_case F10 "complete production config passes validation"
run_cli validate --env "$WORK/bad-prod-ok.env"
a_rc 0
a_cli_json_eq mode production
end_case

bad_env egress-missing 'AGENTMEM_SYSTEMONE_ENDPOINT=https://example.test/route'
begin_case F11 "outbound endpoint without allowlist is rejected"
run_cli validate --env "$WORK/bad-egress-missing.env"
a_rc 1
a_cli_err 'outbound endpoints require AGENTMEM_EGRESS_ALLOWED_HOSTS'
end_case

bad_env egress-ok 'AGENTMEM_SYSTEMONE_ENDPOINT=https://example.test/route' \
  'AGENTMEM_EGRESS_ALLOWED_HOSTS=example.test'
begin_case F12 "outbound endpoint with allowlist passes validation"
run_cli validate --env "$WORK/bad-egress-ok.env"
a_rc 0
a_cli_json_eq status ok
end_case

bad_env adapters "AGENTMEM_ADAPTERS='not-json'"
begin_case F13 "invalid AGENTMEM_ADAPTERS JSON is rejected"
run_cli validate --env "$WORK/bad-adapters.env"
a_rc 1
a_cli_err 'AGENTMEM_ADAPTERS'
end_case

bad_env scope 'AGENTMEM_AUTH_ENABLED=true' \
  "AGENTMEM_AUTH_TOKENS='[{\"token_sha256\":\"$(digest "e2e-bad-$STAMP")\",\"subject\":\"x\",\"scopes\":[\"bogus:scope\"]}]'"
begin_case F14 "unknown scope is rejected"
run_cli validate --env "$WORK/bad-scope.env"
a_rc 1
a_cli_err 'unknown scope'
end_case

bad_env conflict 'AGENTMEM_CONFLICT_POLICY=rag_wins'
begin_case F15 "unimplemented conflict policy is rejected"
run_cli validate --env "$WORK/bad-conflict.env"
a_rc 1
a_cli_err 'conflict_policy'
end_case

bad_env rate 'AGENTMEM_RATE_RPS=abc'
begin_case F16 "invalid number is rejected"
run_cli validate --env "$WORK/bad-rate.env"
a_rc 1
a_cli_err 'expected an integer'
end_case

bad_env bool 'AGENTMEM_AUTH_ENABLED=maybe'
begin_case F17 "invalid boolean is rejected"
run_cli validate --env "$WORK/bad-bool.env"
a_rc 1
a_cli_err 'expected a boolean'
end_case

bad_env duplicate 'AGENTMEM_MODE=dev' 'AGENTMEM_MODE=dev'
begin_case F18 "duplicate key is rejected"
run_cli validate --env "$WORK/bad-duplicate.env"
a_rc 1
a_cli_err 'duplicate key'
end_case

bad_env malformed 'THIS_LINE_HAS_NO_EQUALS'
begin_case F19 "malformed KEY=VALUE line is rejected"
run_cli validate --env "$WORK/bad-malformed.env"
a_rc 1
a_cli_err 'expected KEY=VALUE'
end_case

bad_env graph-no-url 'AGENTMEM_GRAPH_BACKEND=remote'
begin_case F20 "remote graph backend without an endpoint is rejected"
run_cli validate --env "$WORK/bad-graph-no-url.env"
a_rc 1
a_cli_err 'graph.backend remote requires AGENTMEM_GRAPH_REMOTE_URL'
end_case

bad_env graph-bad-token-env 'AGENTMEM_GRAPH_BACKEND=remote' \
  'AGENTMEM_GRAPH_REMOTE_URL=http://127.0.0.1:1/v1' 'AGENTMEM_GRAPH_REMOTE_TOKEN_ENV=bad name'
begin_case F21 "remote token variable must be a variable name, not a value"
run_cli validate --env "$WORK/bad-graph-bad-token-env.env"
a_rc 1
a_cli_err 'graph.remote_token_env must name an environment variable'
end_case

bad_env graph-backend 'AGENTMEM_GRAPH_BACKEND=bogus'
begin_case F22 "unknown graph backend is rejected"
run_cli validate --env "$WORK/bad-graph-backend.env"
a_rc 1
a_cli_err 'graph.backend'
end_case

bad_env graph-depth 'AGENTMEM_GRAPH_MAX_DEPTH=9'
begin_case F23 "out-of-range graph depth is rejected"
run_cli validate --env "$WORK/bad-graph-depth.env"
a_rc 1
a_cli_err 'graph.max_depth must be 1..8'
end_case

bad_env graph-edges 'AGENTMEM_GRAPH_MAX_EDGES=0'
begin_case F24 "out-of-range graph edge budget is rejected"
run_cli validate --env "$WORK/bad-graph-edges.env"
a_rc 1
a_cli_err 'graph.max_edges must be 1..5000000'
end_case

bad_env graph-off-ok 'AGENTMEM_GRAPH_ENABLED=false' 'AGENTMEM_GRAPH_BACKEND=remote'
begin_case F25 "a disabled graph needs no remote endpoint"
run_cli validate --env "$WORK/bad-graph-off-ok.env"
a_rc 0
a_cli_json_eq status ok
end_case

bad_env graph-off-bounds 'AGENTMEM_GRAPH_ENABLED=false' 'AGENTMEM_GRAPH_MAX_DEPTH=9'
begin_case F26 "a disabled graph still validates its bounds"
run_cli validate --env "$WORK/bad-graph-off-bounds.env"
a_rc 1
a_cli_err 'graph.max_depth must be 1..8'
end_case

bad_env mcp-ttl 'AGENTMEM_MCP_SESSION_TTL_MS=1000'
begin_case F27 "an out-of-range MCP session TTL is rejected"
run_cli validate --env "$WORK/bad-mcp-ttl.env"
a_rc 1
a_cli_err 'mcp.session_ttl_ms must be 60000..86400000'
end_case

bad_env mcp-sessions 'AGENTMEM_MCP_MAX_SESSIONS=0'
begin_case F28 "an out-of-range MCP session cap is rejected"
run_cli validate --env "$WORK/bad-mcp-sessions.env"
a_rc 1
a_cli_err 'mcp.max_sessions must be 1..4096'
end_case

# ===========================================================================
section "2. All CLI commands (before server start, to avoid snapshot overwrite)"
# ===========================================================================
begin_case C01 "no arguments prints usage"
run_cli
a_rc 0
a_cli_out 'Commands:'
a_cli_out '  put         Write a knowledge concept'
end_case

begin_case C02 "help prints usage"
run_cli help
a_rc 0
a_cli_out 'agentmem - OKF+Git knowledge hub'
end_case

begin_case C03 "--help prints usage"
run_cli --help
a_rc 0
a_cli_out '  eval        Run ranking'
end_case

begin_case C04 "unknown command errors out"
run_cli no-such-command
a_rc 1
a_cli_err 'unknown command "no-such-command"'
end_case

begin_case C05 "put writes a knowledge concept"
run_cli put "${ENVARG[@]}" --id "$CLI_DOC" --subject alice --owner alice \
  --type Metric --title 'E2E churn' --description 'e2e description' \
  --body '# Definition' --visibility public
a_rc 0
a_cli_json_eq concept_id "$CLI_DOC"
a_cli_json_has content_hash
a_cli_json_has commit_sha
a_cli_json_has decision
end_case

begin_case C06 "put without --id errors out"
run_cli put "${ENVARG[@]}" --subject alice
a_rc 1
a_cli_err 'put: --id is required'
end_case

begin_case C07 "put anonymous write rejected"
run_cli put "${ENVARG[@]}" --id e2e/anon
a_rc 1
a_cli_err 'anonymous write rejected'
end_case

begin_case C08 "put with owner not matching subject is rejected"
run_cli put "${ENVARG[@]}" --id e2e/owner-mismatch --subject alice --owner mallory
a_rc 1
a_cli_err 'owner does not match'
end_case

begin_case C09 "put with invalid visibility is rejected"
run_cli put "${ENVARG[@]}" --id e2e/bad-vis --subject alice --visibility bogus
a_rc 1
end_case

begin_case C10 "query routes and executes"
run_cli query "${ENVARG[@]}" --q 'define churn' --subject alice --top-k 3
a_rc 0
a_cli_json_eq query 'define churn'
a_cli_json_has decision
a_cli_json_has hits
end_case

begin_case C11 "query without --q errors out"
run_cli query "${ENVARG[@]}" --subject alice
a_rc 1
a_cli_err 'query: --q is required'
end_case

begin_case C12 "migrate performs incremental reconciliation"
run_cli migrate "${ENVARG[@]}" --job e2e-cli --direction canonical_to_rag \
  --target memory --limit 10
a_rc 0
a_cli_json_eq job e2e-cli
a_cli_json_eq direction canonical_to_rag
a_cli_json_eq target memory
a_cli_json_has done
end_case

begin_case C13 "migrate --dry-run only plans"
run_cli migrate "${ENVARG[@]}" --job e2e-dry --dry-run --limit 5
a_rc 0
a_cli_json_eq dry_run true
a_cli_json_has planned
end_case

begin_case C14 "migrate with invalid direction errors out"
run_cli migrate "${ENVARG[@]}" --direction wrong-way
a_rc 1
end_case

begin_case C15 "decisions prints routing audit"
run_cli decisions "${ENVARG[@]}" --limit 10
a_rc 0
a_cli_json_len_min '' 1
a_cli_json_has 0.concept_id
end_case

begin_case C16 "concepts lists concept IDs"
run_cli concepts "${ENVARG[@]}"
a_rc 0
a_cli_json_len_min '' 1
end_case

begin_case C17 "compute runs an Attested Computation"
run_cli compute "${ENVARG[@]}" --id "$COMPUTE_ID" --subject alice --param year=2026
a_rc 0
a_cli_json_eq concept_id "$COMPUTE_ID"
a_cli_json_eq display_allowed true
a_cli_json_has display_value
a_cli_json_eq verdict.passed true
end_case

begin_case C18 "compute without --id errors out"
run_cli compute "${ENVARG[@]}" --subject alice
a_rc 1
a_cli_err 'compute: --id is required'
end_case

begin_case C19 "compute with invalid --param errors out"
run_cli compute "${ENVARG[@]}" --id "$COMPUTE_ID" --subject alice --param year
a_rc 1
a_cli_err 'compute: --param must be name=value'
end_case

begin_case C20 "compute with invalid parameter does not display a value"
run_cli compute "${ENVARG[@]}" --id "$COMPUTE_ID" --subject alice --param year=notanint
a_rc 1
end_case

begin_case C21 "compute with unsupported runtime is rejected"
run_cli compute "${ENVARG[@]}" --id "$UNSUPPORTED_ID" --subject alice --param year=2026
a_rc 1
a_cli_err 'has a contract but no executor'
end_case

begin_case C22 "computations prints computation audit"
run_cli computations "${ENVARG[@]}" --id "$COMPUTE_ID" --limit 10
a_rc 0
a_cli_json_len_min '' 1
a_cli_json_has 0.concept_id
end_case

begin_case C23 "eval prints ranking metrics"
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

begin_case C24 "eval uses the default dataset path"
run_cli eval
a_rc 0
a_cli_json_eq k 5
end_case

printf '{not json\n' > "$WORK/bad.jsonl"
begin_case C25 "eval errors on bad JSONL"
run_cli eval --dataset "$WORK/bad.jsonl"
a_rc 1
a_cli_err 'eval: line 1'
end_case

begin_case C26 "eval errors when dataset does not exist"
run_cli eval --dataset "$WORK/missing.jsonl"
a_rc 1
a_cli_err 'no such file or directory'
end_case

start_http_sidecar() {
  python3 "$SIDECAR_DIR/http_sidecar.py" "$PORT_SIDE" >"$WORK/sidecar-http.log" 2>&1 &
  SIDECAR_PID=$!
  local i code
  for i in $(seq 1 100); do
    code="$(curl -sS --max-time 1 -o /dev/null -w '%{http_code}' -X POST \
      -H 'Content-Type: application/json' --data '{"body":"probe"}' \
      "http://127.0.0.1:$PORT_SIDE/run" 2>/dev/null || true)"
    if [[ "$code" == "200" ]]; then return 0; fi
    if ! kill -0 "$SIDECAR_PID" 2>/dev/null; then SIDECAR_PID=""; return 1; fi
    sleep 0.1
  done
  stop_http_sidecar
  return 1
}

# --- Compute-plane sidecar (offline python3 stub) -----------------------------
begin_case C29 "compute runs python runtime via CLI sidecar"
PATH="$SIDECAR_DIR:$PATH" "$BIN" compute --env "$SIDECAR_ENV" --id "$PY_COMPUTE_ID" \
  --subject alice --param year=2026 >"$CLI_OUT" 2>"$CLI_ERR"
CLI_RC=$?
a_rc 0
a_cli_json_eq runtime python
a_cli_json_eq display_allowed true
a_cli_json_has display_value
a_cli_json_eq verdict.passed true
a_cli_json_eq verdict.fidelity true
a_cli_json_absent error_class
end_case

begin_case C30 "compute runs python runtime via HTTP sidecar"
if start_http_sidecar; then
  run_cli compute --env "$SIDECAR_HTTP_ENV" --id "$PY_COMPUTE_ID" --subject alice --param year=2026
  a_rc 0
  a_cli_json_eq runtime python
  a_cli_json_eq display_allowed true
  a_cli_json_has display_value
  a_cli_json_eq verdict.passed true
  stop_http_sidecar
else
  ck 1 "local HTTP sidecar stub failed to start"
fi
end_case

begin_case C31 "sidecar failure shows no value and does not echo the body"
PATH="$SIDECAR_DIR:$PATH" "$BIN" compute --env "$SIDECAR_FAIL_ENV" --id "$PY_COMPUTE_ID" \
  --subject alice --param year=2026 >"$CLI_OUT" 2>"$CLI_ERR"
CLI_RC=$?
a_rc 1
a_cli_err 'sidecar process failed'
a_cli_json_absent display_value
if grep -q 'import json' "$CLI_OUT" "$CLI_ERR" 2>/dev/null; then
  ck 1 "sidecar failure echoed the script body"
else
  ck 0 ""
fi
end_case

begin_case C32 "runtime without a configured sidecar stays fail-closed"
run_cli compute "${ENVARG[@]}" --id "$PY_COMPUTE_ID" --subject alice --param year=2026
a_rc 1
a_cli_err 'has a contract but no executor'
end_case

# The reference sidecar from examples/compute-sidecars runs in its offline mode,
# so these three cases prove the bigquery / dbt / looker paths end to end (body
# kind, parameter pass-through, digest echo, attestation) without a warehouse.
run_example_sidecar() { # CONCEPT_ID
  SIDECAR_OFFLINE=1 PATH="$SIDECAR_DIR:$PATH" "$BIN" compute --env "$SIDECAR_MULTI_ENV" \
    --id "$1" --subject alice --param year=2026 >"$CLI_OUT" 2>"$CLI_ERR"
  CLI_RC=$?
}

begin_case C33 "compute runs the bigquery runtime through the reference sidecar"
run_example_sidecar "$BQ_COMPUTE_ID"
a_rc 0
a_cli_json_eq runtime bigquery
a_cli_json_eq display_allowed true
a_cli_json_eq display_value 1250
a_cli_json_eq verdict.passed true
a_cli_json_absent error_class
end_case

begin_case C34 "compute runs the dbt runtime through the reference sidecar"
run_example_sidecar "$DBT_COMPUTE_ID"
a_rc 0
a_cli_json_eq runtime dbt
a_cli_json_eq display_allowed true
a_cli_json_eq display_value 1310
a_cli_json_eq verdict.fidelity true
end_case

begin_case C35 "compute runs the looker runtime through the reference sidecar"
run_example_sidecar "$LOOKER_COMPUTE_ID"
a_rc 0
a_cli_json_eq runtime looker
a_cli_json_eq display_allowed true
a_cli_json_eq display_value 1420
a_cli_json_eq verdict.passed true
end_case

begin_case C27 "eval does not accept --env (the only command that reads no config)"
run_cli eval --env "$BASE_ENV"
a_rc 1
a_cli_err 'flag provided but not defined'
end_case

begin_case C28 "concepts errors on unknown flag"
run_cli concepts "${ENVARG[@]}" --bogus
a_rc 1
a_cli_err 'flag provided but not defined'
end_case

# ===========================================================================
begin_case C36 "graph put declares a node and commits it"
run_cli graph put "${ENVARG[@]}" --subject system --id e2e/cli-graph --node-type metric --label "CLI graph"
a_rc 0
a_cli_json_eq concept_id e2e/cli-graph
a_cli_json_has content_hash
a_cli_json_has commit_sha
end_case

begin_case C37 "graph put creates the edge target under the same owner"
run_cli graph put "${ENVARG[@]}" --subject system --id e2e/cli-target --node-type table --label "CLI target"
a_rc 0
a_cli_json_eq concept_id e2e/cli-target
a_cli_json_eq unchanged false
end_case

begin_case C38 "graph edge appends a declared relation between readable nodes"
run_cli graph edge "${ENVARG[@]}" --subject system --id e2e/cli-graph --to e2e/cli-target --type computed_from --evidence 'sources[0]'
a_rc 0
a_cli_json_eq concept_id e2e/cli-graph
a_cli_json_eq unchanged false
end_case

begin_case C39 "graph node and neighbors read the declaration back"
run_cli graph node "${ENVARG[@]}" --subject system --id e2e/cli-graph
a_rc 0
a_cli_json_eq concept_id e2e/cli-graph
a_cli_json_eq node_type metric
a_cli_json_eq out.0.to_id e2e/cli-target
a_cli_json_eq out.0.edge_type computed_from
a_cli_json_eq out.0.origin declared
a_cli_json_eq out.0.evidence 'sources[0]'
run_cli graph neighbors "${ENVARG[@]}" --subject system --from e2e/cli-graph --depth 1 --direction out
a_rc 0
a_cli_json_eq root e2e/cli-graph
a_cli_json_len_min nodes 1
end_case

begin_case C40 "graph answerable reports coverage and reindex rebuilds the projection"
run_cli graph answerable "${ENVARG[@]}" --subject system --q "cli graph"
a_rc 0
a_cli_json_has coverage
a_cli_json_has verdict
run_cli graph reindex "${ENVARG[@]}"
a_rc 0
a_cli_json_has nodes
a_cli_json_has edges
a_cli_json_has pruned
end_case

begin_case C41 "graph search and path are deterministic reads"
run_cli graph search "${ENVARG[@]}" --subject system --q cli
a_rc 0
a_cli_json_len_min "" 1
a_cli_json_has 0.concept_id
run_cli graph path "${ENVARG[@]}" --subject system --from e2e/cli-graph --to e2e/cli-target
a_rc 0
a_cli_json_eq from e2e/cli-graph
a_cli_json_eq to e2e/cli-target
a_cli_json_eq found true
a_cli_json_eq hops 1
end_case

begin_case C42 "graph edge to a missing target stays permissive and is reported on read"
run_cli graph edge "${ENVARG[@]}" --subject system --id e2e/cli-graph --to e2e/cli-missing --type derived_from
a_rc 0
a_cli_json_eq concept_id e2e/cli-graph
run_cli graph node "${ENVARG[@]}" --subject system --id e2e/cli-graph
a_rc 0
a_cli_json_len_min warnings 1
end_case

begin_case C43 "mcp over stdio answers one JSON-RPC frame per line"
printf '%s\n' \
'{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}' \
'{"jsonrpc":"2.0","id":2,"method":"tools/list"}' \
| "$BIN" mcp "${ENVARG[@]}" --subject e2e --scopes knowledge:read >"$CLI_OUT" 2>"$CLI_ERR"
CLI_RC=$?
a_rc 0
eq "$(wc -l < "$CLI_OUT" | tr -d ' ')" 2 'stdio frame count'
a_cli_out 'protocolVersion'
a_cli_out 'search_terms'
a_cli_err 'stdio ready'
end_case

begin_case C44 "mcp over stdio refuses to start without an identity"
run_cli mcp "${ENVARG[@]}"
a_rc 1
a_cli_err 'mcp: provide --token, or --subject with --scopes'
if [[ -s "$CLI_OUT" ]]; then ck 1 'stdout must stay empty when the command refuses to start'; else ck 0 ''; fi
end_case

begin_case C45 "mcp over stdio writes nothing for a notification and types an unknown method"
printf '%s\n' \
'{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05"}}' \
'{"jsonrpc":"2.0","method":"notifications/initialized"}' \
'{"jsonrpc":"2.0","id":2,"method":"resources/list"}' \
| "$BIN" mcp "${ENVARG[@]}" --subject e2e --scopes knowledge:read >"$CLI_OUT" 2>"$CLI_ERR"
CLI_RC=$?
a_rc 0
# Two frames for three inputs: a notification must produce no line at all.
eq "$(wc -l < "$CLI_OUT" | tr -d ' ')" 2 'stdio frame count'
a_cli_out '"protocolVersion":"2024-11-05"'
a_cli_out 'method not found'
a_cli_out '-32601'
end_case

begin_case C46 "mcp over stdio answers a batch on one line"
printf '%s\n' \
'[{"jsonrpc":"2.0","id":1,"method":"ping"},{"jsonrpc":"2.0","id":2,"method":"tools/list"}]' \
| "$BIN" mcp "${ENVARG[@]}" --subject e2e --scopes knowledge:read >"$CLI_OUT" 2>"$CLI_ERR"
CLI_RC=$?
a_rc 0
eq "$(wc -l < "$CLI_OUT" | tr -d ' ')" 1 'one frame per batch'
a_cli_out '{"jsonrpc":"2.0","id":1,"result":{}}'
a_cli_out '"id":2'
a_cli_out 'search_terms'
end_case

section "3. All HTTP API routes (auth-disabled profile)"
# ===========================================================================
if start_server base "$BASE_ENV" "$PORT_BASE"; then
  begin_case H01 "serve starts with the offline fake"
  has "$(head -c 500 "$WORK/serve-base.log")" 'postgres=offline fake' 'startup log'
  end_case

  hc H02 "GET /healthz reports dependency health" GET /healthz '' 200
  a_json_eq status ok
  a_json_eq postgres 'fake (offline)'
  a_json_eq 'adapter:okf' ok
  a_json_eq 'adapter:memory' ok
  a_json_eq compute_runtimes 'bigquery,dbt,looker,postgres,python'
  a_header Content-Type 'application/json; charset=utf-8'
  end_case

  hc H03 "POST /v1/knowledge writes a concept" POST /v1/knowledge \
    '{"concept_id":"e2e/http-doc","document":{"type":"Concept","title":"E2E HTTP","description":"written over HTTP","body":"# E2E","tags":["e2e"],"visibility":"public"}}' 200
  a_json_eq concept_id "$HTTP_DOC"
  a_json_has content_hash
  a_json_has commit_sha
  a_json_has decision
  end_case

  hc H04 "GET /v1/knowledge reads the canonical body" GET '/v1/knowledge?id=e2e%2Fhttp-doc' '' 200
  a_json_eq concept_id "$HTTP_DOC"
  a_json_eq type Concept
  a_json_eq title 'E2E HTTP'
  a_json_eq visibility public
  a_json_has body
  end_case

  hc H05 "POST /v1/query dual-path retrieval" POST /v1/query '{"query":"define churn","top_k":3}' 200
  a_json_eq query 'define churn'
  a_json_has decision
  a_json_has hits
  end_case

  hc H06 "POST /v1/migrate incremental reconciliation" POST /v1/migrate \
    '{"job":"e2e-http","direction":"canonical_to_rag","target":"memory","dry_run":true,"limit":10}' 200
  a_json_eq job e2e-http
  a_json_eq dry_run true
  a_json_has done
  a_json_has scanned
  end_case

  hc H07 "GET /v1/decisions routing audit" GET '/v1/decisions?limit=5' '' 200
  a_json_len_min '' 1
  a_json_has 0.concept_id
  end_case

  hc H08 "GET /v1/concepts concept list" GET /v1/concepts '' 200
  a_json_len_min '' 1
  end_case

  hc H09 "POST /v1/computations successfully displays a verified value" POST /v1/computations \
    '{"concept_id":"computations/revenue-postgres","parameters":{"year":2026}}' 200
  a_json_eq concept_id "$COMPUTE_ID"
  a_json_eq display_allowed true
  a_json_eq runtime postgres
  a_json_has display_value
  a_json_eq verdict.passed true
  a_json_eq verdict.provenance true
  a_json_eq verdict.fidelity true
  end_case

  hc H10 "GET /v1/computations computation audit" GET '/v1/computations?limit=5' '' 200
  a_json_len_min '' 1
  end_case

  hc H11 "wrong method returns 405" POST /healthz '' 405
  a_json_eq error 'method not allowed'
  end_case

  hc H12 "wrong method on another route also returns 405" DELETE /v1/knowledge '' 405
  a_json_eq error 'method not allowed'
  end_case

  hc H13 "missing JSON Content-Type is rejected" POST /v1/query '{"query":"x"}' 400 NO_CT
  a_json_eq error 'Content-Type must be application/json'
  end_case

  hc H14 "non-JSON Content-Type is rejected" POST /v1/query '{"query":"x"}' 400 'Content-Type: text/plain'
  a_json_eq error 'Content-Type must be application/json'
  end_case

  hc H15 "unknown field is rejected" POST /v1/query '{"query":"x","bogus":1}' 400
  a_json_eq error 'invalid JSON'
  end_case

  hc H16 "trailing data is rejected" POST /v1/query '{"query":"x"} {"a":1}' 400
  a_json_eq error 'unexpected trailing data'
  end_case

  hc H17 "malformed JSON is rejected" POST /v1/query '{' 400
  a_json_eq error 'invalid JSON'
  end_case

  python3 -c 'import sys; sys.stdout.write("{\"query\":\"" + "x" * (4 * 1024 * 1024 + 1024) + "\"}")' > "$WORK/big.json"
  hc H18 "request body over 4 MiB is rejected" POST /v1/query "@$WORK/big.json" 400
  a_json_eq error 'invalid JSON'
  end_case

  hc H19 "out-of-range top_k is rejected" POST /v1/query '{"query":"x","top_k":101}' 400
  a_json_eq error 'top_k is out of range'
  end_case

  hc H20 "empty query is rejected" POST /v1/query '{"query":""}' 400
  a_json_eq error 'query is empty'
  end_case

  begin_case H21 "all three out-of-range limit values are rejected"
  http_req GET '/v1/decisions?limit=0' ''
  eq "$STATUS" 400 'limit=0'
  http_req GET '/v1/decisions?limit=1001' ''
  eq "$STATUS" 400 'limit=1001'
  http_req GET '/v1/decisions?limit=abc' ''
  eq "$STATUS" 400 'limit=abc'
  a_json_eq error 'limit is out of range'
  end_case

  hc H22 "GET /v1/knowledge without id is rejected" GET /v1/knowledge '' 400
  a_json_eq error 'id is required'
  end_case

  hc H23 "POST /v1/knowledge missing required fields is rejected" POST /v1/knowledge \
    '{"concept_id":"","document":{"type":""}}' 400
  a_json_eq error 'concept_id and document.type are required'
  end_case

  hc H24 "POST /v1/computations without concept_id is rejected" POST /v1/computations \
    '{"parameters":{}}' 400
  a_json_eq error 'concept_id is required'
  end_case

  hc H25 "invalid migration parameter rejected with redacted error" POST /v1/migrate '{"direction":"wrong-way"}' 400
  a_json_eq error 'migration rejected'
  a_body_hasnt 'dsn'
  a_body_hasnt 'SELECT'
  end_case

  hc H26 "wrong computation parameter type returns 400 with no value shown" POST /v1/computations \
    '{"concept_id":"computations/revenue-postgres","parameters":{"year":"notanint"}}' 400
  a_json_eq error 'computation rejected'
  a_json_absent envelope.display_value
  a_body_hasnt 'SELECT'
  a_body_hasnt 'postgres://'
  end_case

  hc H27 "computation missing required parameter returns 400" POST /v1/computations \
    '{"concept_id":"computations/revenue-postgres","parameters":{}}' 400
  a_json_eq error 'computation rejected'
  a_json_absent envelope.display_value
  end_case

  hc H28 "computation parameter tampering with reserved SQL field returns 400" POST /v1/computations \
    '{"concept_id":"computations/revenue-postgres","parameters":{"year":2026,"sql":"SELECT 1"}}' 400
  a_json_eq error 'computation rejected'
  a_json_absent envelope.display_value
  end_case

  hc H29 "unsupported runtime returns 422 with no value shown" POST /v1/computations \
    '{"concept_id":"computations/revenue-ytd","parameters":{"year":2026}}' 422
  a_json_has envelope
  a_json_absent envelope.display_value
  a_body_hasnt 'SELECT'
  end_case

  hc H30 "nonexistent computation concept returns 400" POST /v1/computations \
    '{"concept_id":"computations/does-not-exist","parameters":{}}' 400
  a_json_absent envelope.display_value
  end_case

  hc H31 "Go binary hosts the console home page" GET / '' 200
  a_header X-Content-Type-Options nosniff
  a_header Content-Type text/html
  a_header Cache-Control no-cache
  a_header Content-Security-Policy "script-src 'self'"
  has "$(cat "$BODY")" 'id="app"' 'console root node'
  console_asset="$(grep -oE '/assets/[A-Za-z0-9._-]+' "$BODY" | head -n 1)"
  end_case

  begin_case H36 "console static assets are served by the binary and are long-cacheable"
  if [[ -n "$console_asset" ]]; then
    http_req GET "$console_asset" ''
    a_status 200
    a_header Cache-Control 'immutable'
    hasnt "$(cat "$BODY")" '<html' 'static asset should not be the home page'
  else
    ck 1 "home page references no /assets/ resource"
  fi
  end_case

  hc H37 "unregistered /v1 path still returns JSON 404" GET /v1/unknown '' 404
  a_json_eq error 'not found'
  end_case

  hc H38 "console does not take over unregistered static paths" GET /assets/missing.js '' 404
  a_json_eq error 'not found'
  end_case

  hc H39 "console rejects write methods (JSON 405)" POST / '' 405
  a_json_eq error 'method not allowed'
  end_case

  hc H32 "private and nonexistent documents both return 404" GET '/v1/knowledge?id=e2e%2Fmissing' '' 404
  a_json_eq error 'document not found or access denied'
  end_case

  hc H33 "write with owner not matching subject is rejected" POST /v1/knowledge \
    '{"concept_id":"e2e/owner-mismatch","document":{"type":"Concept","owner":"mallory"}}' 400
  a_json_eq error 'write rejected'
  end_case

  hc H34 "security response headers are complete" GET /healthz '' 200
  a_header X-Content-Type-Options nosniff
  a_header X-Frame-Options DENY
  a_header Referrer-Policy no-referrer
  a_header Cache-Control no-store
  a_header Content-Security-Policy "default-src 'none'"
  end_case

  hc H35 "unknown path returns JSON 404 (console does not take over API prefix)" GET /v1/not-a-route '' 404
  a_json_eq error 'not found'
  a_header Content-Type application/json
  end_case
  begin_case H40 "graph node write commits a declaration with content hash and commit sha"
  http_req PUT /v1/graph/node '{"concept_id":"e2e/graph-a","node_type":"metric","label":"Graph A","replace_edges":true,"edges":[{"to":"e2e/graph-b","type":"derived_from","evidence":"e2e"}]}'
  a_status 200
  a_json_eq concept_id e2e/graph-a
  a_json_has content_hash
  a_json_has commit_sha
  a_json_eq unchanged false
  end_case

  begin_case H41 "creating the target closes the dangling edge"
  http_req PUT /v1/graph/node '{"concept_id":"e2e/graph-b","node_type":"table","label":"Graph B"}'
  a_status 200
  a_json_eq concept_id e2e/graph-b
  a_json_eq unchanged false
  end_case

  begin_case H42 "node detail lists the declared out edge with its origin"
  http_req GET '/v1/graph/node?id=e2e%2Fgraph-a' ''
  a_status 200
  a_json_eq concept_id e2e/graph-a
  a_json_eq node_type metric
  a_json_eq label 'Graph A'
  a_json_eq out.0.to_id e2e/graph-b
  a_json_eq out.0.edge_type derived_from
  a_json_eq out.0.origin declared
  a_json_eq out.0.evidence e2e
  end_case

  begin_case H43 "neighbors expands only the requested direction"
  http_req GET '/v1/graph/neighbors?from=e2e%2Fgraph-a&depth=1&direction=out' ''
  a_status 200
  a_json_eq root e2e/graph-a
  a_json_len_min nodes 1
  a_json_eq truncated false
  a_json_eq depths.e2e/graph-a 0
  a_json_eq depths.e2e/graph-b 1
  end_case

  begin_case H44 "shortest path finds the declared hop"
  http_req GET '/v1/graph/path?from=e2e%2Fgraph-a&to=e2e%2Fgraph-b&max_depth=3' ''
  a_status 200
  a_json_eq from e2e/graph-a
  a_json_eq to e2e/graph-b
  a_json_eq found true
  a_json_eq hops 1
  end_case

  begin_case H45 "graph search returns the declared nodes"
  http_req GET '/v1/graph/search?q=graph&limit=10' ''
  a_status 200
  a_json_len_min "" 1
  a_json_has 0.concept_id
  end_case

  begin_case H46 "graph schema answers even with no ontology document"
  http_req GET /v1/graph/schema ''
  a_status 200
  a_json_has node_types
  a_json_has edge_types
  end_case

  begin_case H47 "graph stats count the projection"
  http_req GET /v1/graph ''
  a_status 200
  a_json_has nodes
  a_json_has edges
  a_json_has stale
  a_json_has node_types
  end_case

  begin_case H48 "answerability returns a verdict, coverage and gaps"
  http_req POST /v1/graph/answerability '{"question":"graph b"}'
  a_status 200
  a_json_eq question 'graph b'
  a_json_has verdict
  a_json_has coverage
  a_json_has gaps
  end_case

  begin_case H49 "retrieve fuses the text and graph rankers"
  http_req POST /v1/graph/retrieve '{"query":"graph","top_k":5}'
  a_status 200
  a_json_eq query graph
  a_json_has hits
  a_json_has nodes
  end_case

  begin_case H50 "reindex rebuilds the projection from the bundle"
  http_req POST /v1/graph/index '{}'
  a_status 200
  a_json_has nodes
  a_json_has edges
  a_json_has pruned
  end_case

  begin_case H51 "MCP initialize negotiates the newest revision and a server identity"
  http_req POST /mcp '{"jsonrpc":"2.0","id":1,"method":"initialize"}'
  a_status 200
  a_json_eq jsonrpc 2.0
  a_json_eq result.serverInfo.name agentmem
  a_json_eq result.protocolVersion 2025-06-18
  a_json_eq result.capabilities.tools.listChanged false
  end_case

  begin_case H52 "MCP tools/list exposes the read tools and hides every write tool"
  http_req POST /mcp '{"jsonrpc":"2.0","id":2,"method":"tools/list"}'
  a_status 200
  a_json_eq result.tools.0.name search_terms
  a_json_len_min result.tools 13
  hasnt "$(cat "$BODY")" 'put_knowledge' 'read-only tool list'
  hasnt "$(cat "$BODY")" 'rebuild_graph_index' 'read-only tool list'
  end_case

  begin_case H53 "MCP notification without an id answers 202 with no body"
  http_req POST /mcp '{"jsonrpc":"2.0","method":"notifications/initialized"}'
  a_status 202
  eq "$(head -c 64 "$BODY")" '' 'notification body'
  end_case

  hc H54 "graph node read requires an id" GET /v1/graph/node '' 400
  a_json_eq error 'id is required'
  end_case

  hc H55 "graph depth is clamped to the configured range" GET '/v1/graph/neighbors?from=e2e%2Fgraph-a&depth=99' '' 400
  a_json_eq error 'numeric parameter is out of range'
  end_case

  hc H56 "unknown graph node and a private one share one 404" GET '/v1/graph/node?id=e2e%2Fmissing' '' 404
  a_json_eq error 'node not found or access denied'
  end_case

  begin_case H57 "MCP tools/call search_terms returns text plus structured content"
  http_req POST /mcp '{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"search_terms","arguments":{"query":"graph","limit":5}}}'
  a_status 200
  a_json_eq result.isError false
  a_json_eq result.content.0.type text
  a_json_has result.structuredContent
  a_json_has result.structuredContent.nodes.0.concept_id
  end_case

  begin_case H58 "MCP tools/call get_node reads one node through the same ACL"
  http_req POST /mcp '{"jsonrpc":"2.0","id":8,"method":"tools/call","params":{"name":"get_node","arguments":{"concept_id":"e2e/graph-a"}}}'
  a_status 200
  a_json_eq result.isError false
  a_json_eq result.structuredContent.concept_id e2e/graph-a
  a_json_eq result.structuredContent.node_type metric
  end_case

  begin_case H59 "MCP tools/call rejects an unknown tool without touching knowledge"
  http_req POST /mcp '{"jsonrpc":"2.0","id":9,"method":"tools/call","params":{"name":"graph_put","arguments":{}}}'
  a_status 200
  a_json_eq error.code -32602
  a_json_eq error.message 'unknown tool'
  a_json_absent result
  end_case

  begin_case H60 "MCP oversized request body is rejected with 413"
  http_req POST /mcp "@$WORK/big.json"
  a_status 413
  a_json_eq error 'request body is too large'
  end_case

  begin_case H61 "a stateful MCP revision without a session is refused"
  http_req POST /mcp '{"jsonrpc":"2.0","id":11,"method":"ping"}' 'MCP-Protocol-Version: 2025-06-18'
  a_status 404
  a_json_eq error.code -32004
  a_json_eq error.message 'Mcp-Session-Id header is required'
  http_req POST /mcp '{"jsonrpc":"2.0","id":12,"method":"ping"}' \
    'MCP-Protocol-Version: 2025-06-18' 'Mcp-Session-Id: unknown-session'
  a_status 404
  a_json_eq error.code -32004
  end_case

  begin_case H62 "initialize issues a session that carries calls and can be closed"
  http_req POST /mcp '{"jsonrpc":"2.0","id":13,"method":"initialize","params":{"protocolVersion":"2025-03-26"}}'
  a_status 200
  a_json_eq result.protocolVersion 2025-03-26
  MCP_SESSION="$(header_value Mcp-Session-Id)"
  eq "$([[ -n "$MCP_SESSION" ]] && echo issued || echo missing)" issued 'session header'
  http_req POST /mcp '{"jsonrpc":"2.0","id":14,"method":"tools/list"}' \
    "Mcp-Session-Id: $MCP_SESSION" 'MCP-Protocol-Version: 2025-03-26'
  a_status 200
  a_json_len_min result.tools 13
  http_req POST /mcp '{"jsonrpc":"2.0","id":15,"method":"ping"}' \
    "Mcp-Session-Id: $MCP_SESSION" 'MCP-Protocol-Version: 2025-03-26'
  a_status 200
  http_req DELETE /mcp '' "Mcp-Session-Id: $MCP_SESSION"
  a_status 204
  http_req POST /mcp '{"jsonrpc":"2.0","id":16,"method":"ping"}' "Mcp-Session-Id: $MCP_SESSION"
  a_status 404
  a_json_eq error.code -32004
  end_case

  begin_case H63 "an SSE-preferring client receives the response as an event"
  http_req POST /mcp '{"jsonrpc":"2.0","id":17,"method":"ping"}' 'Accept: application/json, text/event-stream'
  a_status 200
  a_header Content-Type text/event-stream
  a_body_has 'event: message'
  a_body_has 'data: '
  a_body_has '"jsonrpc":"2.0"'
  end_case

  begin_case H64 "GET /mcp opens an event stream for a live session"
  http_req POST /mcp '{"jsonrpc":"2.0","id":18,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}'
  a_status 200
  STREAM_SESSION="$(header_value Mcp-Session-Id)"
  curl -sS --max-time 2 -o /dev/null -D "$WORK/stream.headers" \
    -H "Mcp-Session-Id: $STREAM_SESSION" "http://127.0.0.1:${PORT}/mcp" >/dev/null 2>&1 || true
  if grep -qi 'text/event-stream' "$WORK/stream.headers"; then ck 0 ''; else ck 1 'the event stream did not open'; fi
  if grep -q '^HTTP/1.1 200' "$WORK/stream.headers"; then ck 0 ''; else ck 1 'the event stream must answer 200'; fi
  http_req DELETE /mcp '' "Mcp-Session-Id: $STREAM_SESSION"
  a_status 204
  end_case

  begin_case H65 "MCP read_knowledge returns the canonical body under the same ACL"
  http_req POST /mcp '{"jsonrpc":"2.0","id":19,"method":"tools/call","params":{"name":"read_knowledge","arguments":{"concept_id":"e2e/http-doc"}}}'
  a_status 200
  a_json_eq result.structuredContent.concept_id e2e/http-doc
  a_json_eq result.structuredContent.title 'E2E HTTP'
  a_json_eq result.structuredContent.visibility public
  http_req POST /mcp '{"jsonrpc":"2.0","id":20,"method":"tools/call","params":{"name":"read_knowledge","arguments":{"concept_id":"e2e/missing-doc"}}}'
  a_status 200
  a_json_eq error.code -32002
  a_json_eq error.message 'document not found or access denied'
  end_case

  begin_case H66 "MCP read tools cover concepts, query and audit surfaces"
  http_req POST /mcp '{"jsonrpc":"2.0","id":21,"method":"tools/call","params":{"name":"list_concepts","arguments":{}}}'
  a_status 200
  a_json_has result.structuredContent.concepts
  http_req POST /mcp '{"jsonrpc":"2.0","id":22,"method":"tools/call","params":{"name":"query","arguments":{"query":"e2e","top_k":3}}}'
  a_status 200
  a_json_has result.structuredContent.query
  a_json_has result.structuredContent.hits
  http_req POST /mcp '{"jsonrpc":"2.0","id":23,"method":"tools/call","params":{"name":"graph_schema","arguments":{}}}'
  a_status 200
  a_json_has result.structuredContent.node_types
  a_json_has result.structuredContent.edge_types
  end_case

  begin_case H67 "disabled write tools are indistinguishable from unknown tools"
  http_req POST /mcp '{"jsonrpc":"2.0","id":24,"method":"tools/call","params":{"name":"put_knowledge","arguments":{"concept_id":"e2e/mcp-write","document":{"type":"Concept"}}}}'
  a_status 200
  a_json_eq error.code -32602
  a_json_eq error.message 'unknown tool'
  http_req GET '/v1/knowledge?id=e2e%2Fmcp-write' ''
  a_status 404
  a_json_eq error 'document not found or access denied'
  end_case

  begin_case H69 "MCP malformed JSON answers a parse error"
  http_req POST /mcp '{'
  a_status 400
  a_json_eq jsonrpc 2.0
  a_json_eq error.code -32700
  a_json_eq error.message 'parse error'
  end_case

  begin_case H70 "MCP unknown method answers method not found"
  http_req POST /mcp '{"jsonrpc":"2.0","id":25,"method":"resources/list"}'
  a_status 200
  a_json_eq error.code -32601
  a_json_eq error.message 'method not found'
  a_json_absent result
  end_case

  begin_case H71 "MCP invalid request is rejected before any session lookup"
  http_req POST /mcp '{"jsonrpc":"1.0","id":26,"method":"ping"}'
  a_status 200
  a_json_eq error.code -32600
  a_json_eq error.message 'invalid request'
  http_req POST /mcp '{"jsonrpc":"2.0","id":27}'
  a_status 200
  a_json_eq error.code -32600
  a_json_eq error.message 'invalid request'
  end_case

  begin_case H72 "an unsupported MCP-Protocol-Version header is refused"
  http_req POST /mcp '{"jsonrpc":"2.0","id":28,"method":"ping"}' 'MCP-Protocol-Version: 1999-01-01'
  a_status 400
  a_json_eq error.code -32600
  a_json_eq error.message 'unsupported protocol version'
  end_case

  begin_case H73 "a MCP batch answers every request in order"
  http_req POST /mcp '[{"jsonrpc":"2.0","id":29,"method":"ping"},{"jsonrpc":"2.0","id":30,"method":"tools/list"}]'
  a_status 200
  a_json_len_min '' 2
  a_json_eq 0.id 29
  a_json_eq 0.result '{}'
  a_json_eq 1.id 30
  a_json_len_min 1.result.tools 13
  end_case

  python3 -c 'import json; print(json.dumps([{"jsonrpc": "2.0", "id": i, "method": "ping"} for i in range(33)]))' > "$WORK/batch.json"
  begin_case H74 "an oversized MCP batch is rejected as invalid"
  http_req POST /mcp "@$WORK/batch.json"
  a_status 400
  a_json_eq error.code -32600
  a_json_eq error.message 'invalid request'
  end_case

  begin_case H75 "an unsupported HTTP method on /mcp answers 405 with Allow"
  http_req PUT /mcp '{}'
  a_status 405
  a_header Allow 'GET, POST, DELETE'
  a_json_eq error 'method not allowed'
  end_case

  begin_case H76 "MCP stream and close without a session answer session not found"
  http_req GET /mcp ''
  a_status 404
  a_json_eq error.code -32004
  a_json_eq error.message 'session not found'
  http_req DELETE /mcp ''
  a_status 404
  a_json_eq error.code -32004
  a_json_eq error.message 'session not found'
  end_case

  begin_case H77 "MCP tools/call validates arguments before touching knowledge"
  http_req POST /mcp '{"jsonrpc":"2.0","id":31,"method":"tools/call","params":{"name":"read_knowledge","arguments":{}}}'
  a_status 200
  a_json_eq error.code -32602
  a_json_eq error.message 'concept_id is required'
  a_json_absent result
  end_case

  begin_case H78 "the decisions audit filters by concept_id"
  http_req GET '/v1/decisions?concept_id=e2e%2Fhttp-doc&limit=100' ''
  a_status 200
  a_json_len_min '' 1
  a_json_eq 0.concept_id "$HTTP_DOC"
  http_req GET '/v1/decisions?concept_id=e2e%2Fno-such-concept' ''
  a_status 200
  a_json_eq '' '[]'
  end_case

  begin_case H79 "the computation audit filters by concept_id"
  http_req GET '/v1/computations?concept_id=computations%2Frevenue-postgres&limit=100' ''
  a_status 200
  a_json_len_min '' 1
  a_json_eq 0.concept_id "$COMPUTE_ID"
  http_req GET '/v1/computations?concept_id=computations%2Fno-such-concept' ''
  a_status 200
  a_json_eq '' '[]'
  end_case

  begin_case H80 "graph reads reject missing or out-of-range inputs"
  http_req POST /v1/graph/answerability '{}'
  a_status 400
  a_json_eq error 'question is required'
  http_req POST /v1/graph/retrieve '{"query":"   "}'
  a_status 400
  a_json_eq error 'query is empty'
  http_req POST /v1/graph/retrieve '{"query":"x","top_k":101}'
  a_status 400
  a_json_eq error 'top_k is out of range'
  http_req GET '/v1/graph/neighbors?depth=1' ''
  a_status 400
  a_json_eq error 'from is required'
  end_case

  begin_case H81 "graph routing keeps unknown paths, methods and directions typed"
  http_req GET /v1/graph/nope ''
  a_status 404
  a_json_eq error 'not found'
  http_req POST /v1/graph/search '{}'
  a_status 405
  a_json_eq error 'method not allowed'
  http_req GET '/v1/graph/neighbors?from=e2e%2Fgraph-a&direction=sideways' ''
  a_status 400
  a_json_eq error 'direction must be out, in or both'
  end_case

else
  fail_case H00 "HTTP case group" "service failed to start: $(head -c 200 "$WORK/serve-base.log" | tr '\n' ' ')"
fi
stop_server

# ===========================================================================
section "3b. API-only deployment (AGENTMEM_CONSOLE_ENABLED=false)"
# ===========================================================================
if start_server console-off "$CONSOLE_ENV" "$PORT_CONSOLE"; then
  begin_case H68 "console disabled serves JSON 404 while the API keeps working"
  has "$(head -c 500 "$WORK/serve-console-off.log")" 'console=disabled' 'startup log'
  http_req GET / ''
  a_status 404
  a_json_eq error 'not found'
  a_header Content-Type application/json
  http_req GET /assets/missing.js ''
  a_status 404
  a_json_eq error 'not found'
  http_req GET /healthz ''
  a_status 200
  a_json_eq status ok
  http_req GET /v1/concepts ''
  a_status 200
  end_case
else
  fail_case H68 "API-only profile" "service failed to start: $(head -c 200 "$WORK/serve-console-off.log" | tr '\n' ' ')"
fi
stop_server

# ===========================================================================
section "3b2. MCP session capacity (AGENTMEM_MCP_MAX_SESSIONS=1)"
# ===========================================================================
if start_server mcp-cap "$MCP_CAP_ENV" "$PORT_MCP_CAP"; then
  begin_case H82 "a full session table refuses a new session and keeps the live one"
  http_req POST /mcp '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}'
  a_status 200
  CAP_SESSION="$(header_value Mcp-Session-Id)"
  eq "$([[ -n "$CAP_SESSION" ]] && echo issued || echo missing)" issued 'first session header'
  http_req POST /mcp '{"jsonrpc":"2.0","id":2,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}'
  a_status 200
  a_json_eq error.code -32004
  a_json_eq error.message 'session capacity reached'
  a_json_absent result
  eq "$(header_value Mcp-Session-Id)" '' 'the refused initialize must not issue a session'
  http_req POST /mcp '{"jsonrpc":"2.0","id":3,"method":"ping"}' "Mcp-Session-Id: $CAP_SESSION" 'MCP-Protocol-Version: 2025-06-18'
  a_status 200
  http_req DELETE /mcp '' "Mcp-Session-Id: $CAP_SESSION"
  a_status 204
  http_req POST /mcp '{"jsonrpc":"2.0","id":4,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}'
  a_status 200
  eq "$([[ -n "$(header_value Mcp-Session-Id)" ]] && echo issued || echo missing)" issued 'capacity released after close'
  end_case
else
  fail_case H82 "MCP session capacity profile" "service failed to start: $(head -c 200 "$WORK/serve-mcp-cap.log" | tr '\n' ' ')"
fi
stop_server

# ===========================================================================
section "3c. Typed ontology: declared relations are hard constraints"
# ===========================================================================
# The ontology is bundle content, so seeding the document is the supported way
# to declare it; no product endpoint can invent one.
cat > "$WORK/bundle/ontology.md" <<'MD'
---
type: ontology-schema
graph_schema:
  node_types:
    metric: {}
    table: {}
  edge_types:
    computed_from: { from: [metric], to: [table] }
    derived_from:  { from: [metric], to: [metric] }
---
# Typed ontology

Seeded by the e2e harness.
MD

if start_server ontology "$ONTOLOGY_ENV" "$PORT_ONTOLOGY"; then
  begin_case T01 "a seeded ontology document is served as a typed schema"
  http_req GET /v1/graph/schema ''
  a_status 200
  a_json_has node_types.metric
  a_json_has node_types.table
  a_json_eq edge_types.computed_from.from.0 metric
  a_json_eq edge_types.computed_from.to.0 table
  a_json_eq warnings null
  end_case

  begin_case T02 "a relation allowed by the ontology is committed"
  http_req PUT /v1/graph/node '{"concept_id":"e2e/typed-metric","node_type":"metric","label":"Typed metric"}'
  a_status 200
  a_json_eq concept_id e2e/typed-metric
  http_req PUT /v1/graph/node '{"concept_id":"e2e/typed-table","node_type":"table","label":"Typed table"}'
  a_status 200
  http_req PUT /v1/graph/node '{"concept_id":"e2e/typed-metric","replace_edges":true,"edges":[{"to":"e2e/typed-table","type":"computed_from"}]}'
  a_status 200
  a_json_eq unchanged false
  http_req GET '/v1/graph/node?id=e2e%2Ftyped-metric' ''
  a_status 200
  a_json_eq out.0.to_id e2e/typed-table
  a_json_eq out.0.edge_type computed_from
  end_case

  begin_case T03 "a relation that violates the ontology is rejected with 422"
  http_req PUT /v1/graph/node '{"concept_id":"e2e/typed-table","replace_edges":true,"edges":[{"to":"e2e/typed-metric","type":"computed_from"}]}'
  a_status 422
  a_json_eq error 'graph request rejected'
  a_json_absent content_hash
  a_body_hasnt 'not allowed from node type'
  http_req GET '/v1/graph/node?id=e2e%2Ftyped-table' ''
  a_status 200
  a_json_eq out '[]'
  end_case

  begin_case T04 "a rejected edge append leaves the stored declaration untouched"
  http_req POST /v1/graph/edges '{"concept_id":"e2e/typed-table","edges":[{"to":"e2e/typed-metric","type":"derived_from"}]}'
  a_status 422
  a_json_eq error 'graph request rejected'
  http_req GET '/v1/graph/node?id=e2e%2Ftyped-metric' ''
  a_status 200
  a_json_eq out.0.to_id e2e/typed-table
  end_case
else
  fail_case T00 "typed ontology group" "service failed to start: $(head -c 200 "$WORK/serve-ontology.log" | tr '\n' ' ')"
fi
stop_server

begin_case T05 "the CLI reports the same typed rejection"
run_cli graph edge --env "$ONTOLOGY_ENV" --subject system --id e2e/typed-table --to e2e/typed-metric --type derived_from
a_rc 1
a_cli_err 'not allowed from node type'
end_case

# ===========================================================================
section "3d. Graph switched off (AGENTMEM_GRAPH_ENABLED=false)"
# ===========================================================================
if start_server graph-off "$GRAPH_OFF_ENV" "$PORT_GRAPH_OFF"; then
  begin_case T06 "graph surfaces answer JSON 404 while the rest of the API works"
  http_req GET /v1/graph ''
  a_status 404
  a_json_eq error 'not found'
  http_req GET '/v1/graph/node?id=e2e%2Ftyped-metric' ''
  a_status 404
  a_json_eq error 'not found'
  http_req POST /mcp '{"jsonrpc":"2.0","id":1,"method":"initialize"}'
  a_status 404
  a_json_eq error 'not found'
  http_req GET /healthz ''
  a_status 200
  a_json_eq status ok
  http_req GET /v1/concepts ''
  a_status 200
  end_case
else
  fail_case T00 "graph-off profile" "service failed to start: $(head -c 200 "$WORK/serve-graph-off.log" | tr '\n' ' ')"
fi
stop_server

# ===========================================================================
section "3e. Remote read-only graph engine (AGENTMEM_GRAPH_BACKEND=remote)"
# ===========================================================================
cat > "$WORK/engine_stub.py" <<'PY'
#!/usr/bin/env python3
"""Loopback graph engine stub for the read-only contract in docs/graph.md §10.

Every request is logged as one line so the harness can prove what crossed the
boundary: method, path, id, whether a bearer was present (never its value), the
forwarded subject and whether the subject leaked into the query string.
"""
import json
import sys
from http.server import BaseHTTPRequestHandler, HTTPServer
from urllib.parse import parse_qs, urlparse

LOG = sys.argv[2]


class Handler(BaseHTTPRequestHandler):
    def _record(self, method):
        url = urlparse(self.path)
        query = parse_qs(url.query)
        auth = "ok" if (self.headers.get("Authorization") or "").startswith("Bearer ") else "missing"
        subject = self.headers.get("X-Agentmem-Subject") or "-"
        qsubject = "yes" if "subject" in query else "no"
        ident = (query.get("id") or query.get("from") or ["-"])[0]
        with open(LOG, "a", encoding="utf-8") as fh:
            fh.write("%s %s id=%s auth=%s subject=%s qsubject=%s\n"
                     % (method, url.path, ident, auth, subject, qsubject))

    def _reply(self, payload, status=200):
        body = json.dumps(payload).encode("utf-8")
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def do_GET(self):
        self._record("GET")
        url = urlparse(self.path)
        query = parse_qs(url.query)
        if url.path == "/v1/graph/node":
            if (query.get("id") or [""])[0] == "e2e/boom":
                self._reply({"error": "internal detail with DSN postgres://u:p@db/x"}, 500)
                return
            self._reply({
                "concept_id": (query.get("id") or [""])[0],
                "node_type": "remote_node", "label": "Remote Node",
                "out": [], "in": [], "warnings": [],
            })
        elif url.path == "/v1/graph/schema":
            self._reply({
                "node_types": {"remote_node": {"description": "Remote measure"}},
                "edge_types": {"remote_link": {"from": ["remote_node"], "to": ["remote_node"]}},
            })
        elif url.path == "/v1/graph/stats":
            self._reply({
                "nodes": 7, "edges": 3, "stale": 0,
                "node_types": {"remote_node": 7}, "edge_types": {"remote_link": 3},
            })
        elif url.path == "/v1/graph/search":
            self._reply([{"concept_id": "e2e/remote", "node_type": "remote_node", "label": "Remote Node"}])
        elif url.path == "/v1/graph/neighbors":
            self._reply({
                "root": (query.get("from") or [""])[0],
                "nodes": [], "edges": [], "depths": {}, "truncated": False,
            })
        elif url.path == "/v1/graph/path":
            self._reply({
                "from": (query.get("from") or [""])[0], "to": (query.get("to") or [""])[0],
                "found": False, "hops": 0, "nodes": [], "edges": [],
            })
        else:
            self._reply({"error": "not found"}, 404)

    def do_POST(self):
        self._record("POST")
        self._reply({"verdict": "insufficient", "coverage": 0.0, "matched": [], "gaps": []})

    def log_message(self, *args):
        pass


HTTPServer(("127.0.0.1", int(sys.argv[1])), Handler).serve_forever()
PY

start_engine() {
  python3 "$WORK/engine_stub.py" "$PORT_ENGINE" "$ENGINE_LOG" >"$WORK/engine-stub.log" 2>&1 &
  ENGINE_PID=$!
  local i code
  for i in $(seq 1 100); do
    code="$(curl -sS --max-time 1 -o /dev/null -w '%{http_code}' \
      -H 'Authorization: Bearer probe' \
      "http://127.0.0.1:$PORT_ENGINE/v1/graph/schema" 2>/dev/null || true)"
    if [[ "$code" == "200" ]]; then return 0; fi
    if ! kill -0 "$ENGINE_PID" 2>/dev/null; then ENGINE_PID=""; return 1; fi
    sleep 0.1
  done
  stop_engine
  return 1
}

# The token variable is unset here, so this proves the fail-closed check, not a
# missing scope: the process must exit before it ever listens.
begin_case E08 "remote backend without a token fails closed before listening"
timeout 15 "$BIN" serve --env "$REMOTE_NO_TOKEN_ENV" >"$CLI_OUT" 2>"$CLI_ERR"
CLI_RC=$?
a_rc 1
a_cli_err 'graph backend'
a_cli_err 'remote token is empty'
if grep -q 'agentmem listening' "$CLI_OUT"; then ck 1 'the server listened with an unusable engine config'; else ck 0 ''; fi
end_case

export GRAPH_ENGINE_TOKEN
begin_case E09 "remote endpoint outside the egress allowlist fails closed"
timeout 15 "$BIN" serve --env "$REMOTE_BAD_HOST_ENV" >"$CLI_OUT" 2>"$CLI_ERR"
CLI_RC=$?
a_rc 1
a_cli_err 'graph backend'
a_cli_err 'is not allowlisted'
if grep -q 'agentmem listening' "$CLI_OUT"; then ck 1 'the server listened with an unallowlisted engine'; else ck 0 ''; fi
end_case

ENGINE_AUTH=("Authorization: Bearer $TOK_ENGINE")
if start_engine; then
  if start_server remote "$REMOTE_ENV" "$PORT_REMOTE"; then
    hc E01 "a node read is proxied to the engine" GET '/v1/graph/node?id=e2e%2Fremote' '' 200 "${ENGINE_AUTH[@]}"
    a_json_eq concept_id e2e/remote
    a_json_eq node_type remote_node
    a_json_eq label 'Remote Node'
    end_case

    begin_case E02 "the remote schema is decoded into the same snake_case shape"
    http_req GET /v1/graph/schema '' "${ENGINE_AUTH[@]}"
    a_status 200
    a_json_has node_types.remote_node
    a_json_eq edge_types.remote_link.from.0 remote_node
    a_json_eq edge_types.remote_link.to.0 remote_node
    end_case

    begin_case E03 "stats are passthrough, not recomputed from the local index"
    http_req GET /v1/graph '' "${ENGINE_AUTH[@]}"
    a_status 200
    a_json_eq nodes 7
    a_json_eq edges 3
    end_case

    hc E10 "neighbourhood reads are proxied to the engine" GET '/v1/graph/neighbors?from=e2e%2Fremote&depth=2&direction=out' '' 200 "${ENGINE_AUTH[@]}"
    a_json_eq root e2e/remote
    a_json_len_min nodes 0
    a_json_eq truncated false
    end_case

    hc E11 "shortest-path reads are proxied to the engine" GET '/v1/graph/path?from=e2e%2Fremote&to=e2e%2Ftarget&max_depth=3' '' 200 "${ENGINE_AUTH[@]}"
    a_json_eq from e2e/remote
    a_json_eq to e2e/target
    a_json_eq found false
    end_case

    hc E12 "search hits come from the engine" GET '/v1/graph/search?q=remote' '' 200 "${ENGINE_AUTH[@]}"
    a_json_eq 0.concept_id e2e/remote
    a_json_eq 0.node_type remote_node
    end_case

    hc E13 "answerability is proxied as a POST" POST /v1/graph/answerability \
      '{"question":"how much"}' 200 "${ENGINE_AUTH[@]}"
    a_json_eq verdict insufficient
    a_json_len_min gaps 0
    end_case

    hc E14 "engine error text is redacted at the boundary" GET '/v1/graph/node?id=e2e%2Fboom' '' 500 "${ENGINE_AUTH[@]}"
    a_json_eq error 'internal error'
    a_body_hasnt DSN
    a_body_hasnt postgres
    a_body_hasnt 127.0.0.1
    end_case

    begin_case E04 "the engine sees the bearer and the token-derived subject only"
    engine_log="$(cat "$ENGINE_LOG")"
    has "$engine_log" 'auth=ok' 'bearer header'
    has "$engine_log" 'subject=alice' 'forwarded subject'
    has "$engine_log" 'qsubject=no' 'subject leaked into the query string'
    hasnt "$engine_log" 'auth=missing' 'unauthenticated engine call'
    end_case

    hc E05 "a node write is rejected before it reaches the engine" PUT /v1/graph/node \
      '{"concept_id":"e2e/remote-write","node_type":"metric","label":"Should not exist"}' 422 "${ENGINE_AUTH[@]}"
    a_json_eq error 'graph request rejected'
    a_json_absent content_hash
    a_body_hasnt 'builtin'
    end_case

    hc E06 "a reindex is rejected for the same reason" POST /v1/graph/index '{}' 422 "${ENGINE_AUTH[@]}"
    a_json_eq error 'graph request rejected'
    end_case

    begin_case E07 "rejected writes reach neither the engine nor the canonical store"
    engine_log="$(cat "$ENGINE_LOG")"
    hasnt "$engine_log" 'PUT ' 'engine received a write'
    hasnt "$engine_log" '/v1/graph/index' 'engine received a reindex'
    http_req GET '/v1/knowledge?id=e2e%2Fremote-write' '' "${ENGINE_AUTH[@]}"
    a_status 404
    a_json_eq error 'document not found or access denied'
    end_case
  else
    fail_case E00 "remote engine group" "service failed to start: $(head -c 200 "$WORK/serve-remote.log" | tr '\n' ' ')"
  fi
  stop_server
  stop_engine
else
  fail_case E00 "remote engine stub" "loopback engine stub failed to start: $(head -c 200 "$WORK/engine-stub.log" | tr '\n' ' ')"
fi
stop_server
unset GRAPH_ENGINE_TOKEN

# ===========================================================================
section "4. Authentication, permission matrix, and identity security (auth-enabled profile)"
# ===========================================================================
if start_server auth "$AUTH_ENV" "$PORT_AUTH"; then
  hc S01 "GET /healthz needs no token" GET /healthz '' 200
  a_json_eq status ok
  end_case

  hc S02 "missing token returns 401 with WWW-Authenticate" GET /v1/concepts '' 401
  a_json_eq error 'authentication required'
  a_header WWW-Authenticate 'Bearer realm="agentmem"'
  end_case

  hc S03 "unknown token returns 401" GET /v1/concepts '' 401 "Authorization: Bearer not-a-real-token"
  a_json_eq error 'authentication required'
  end_case

  hc S04 "knowledge:read can only read, not write" GET /v1/concepts '' 200 "Authorization: Bearer $TOK_READ"
  http_req POST /v1/knowledge \
    '{"concept_id":"e2e/scope-denied","document":{"type":"Concept"}}' \
    "Authorization: Bearer $TOK_READ"
  eq "$STATUS" 403 'knowledge:read write'
  end_case

  hc S05 "knowledge:write can only write, not read" POST /v1/knowledge \
    '{"concept_id":"e2e/scope-write","document":{"type":"Concept","visibility":"public"}}' 200 "Authorization: Bearer $TOK_WRITE"
  http_req GET /v1/concepts '' "Authorization: Bearer $TOK_WRITE"
  eq "$STATUS" 403 'knowledge:write read'
  end_case

  hc S06 "migrate:run can only migrate, not audit" POST /v1/migrate \
    '{"direction":"canonical_to_rag","target":"memory","dry_run":true,"limit":5}' 200 "Authorization: Bearer $TOK_MIGRATE"
  http_req GET /v1/decisions '' "Authorization: Bearer $TOK_MIGRATE"
  eq "$STATUS" 403 'migrate:run audit'
  end_case

  hc S07 "audit:read can only audit, not query" GET /v1/decisions '' 200 "Authorization: Bearer $TOK_AUDIT"
  http_req POST /v1/query '{"query":"x"}' "Authorization: Bearer $TOK_AUDIT"
  eq "$STATUS" 403 'audit:read query'
  end_case

  hc S08 "compute:run can only compute, not read audit" POST /v1/computations \
    '{"concept_id":"computations/revenue-postgres","parameters":{"year":2026}}' 200 "Authorization: Bearer $TOK_COMPUTE"
  a_json_eq display_allowed true
  http_req GET /v1/computations '' "Authorization: Bearer $TOK_COMPUTE"
  eq "$STATUS" 403 'compute:run audit'
  end_case

  hc S09 "health:read is insufficient for protected routes" GET /v1/concepts '' 403 "Authorization: Bearer $TOK_HEALTH"
  a_json_eq error 'permission denied'
  end_case

  hc S10 "full-scope token can access read, write, and audit" GET /v1/knowledge?id=e2e%2Fhttp-doc '' 200 "Authorization: Bearer $TOK_ALL"
  a_json_eq concept_id "$HTTP_DOC"
  http_req GET /v1/computations '' "Authorization: Bearer $TOK_ALL"
  eq "$STATUS" 200 'full-scope computation audit'
  end_case

  hc S11 "subject spoofing in request body is rejected" POST /v1/query \
    '{"query":"x","subject":"mallory"}' 400 "Authorization: Bearer $TOK_ALL"
  a_json_eq error 'invalid JSON'
  end_case

  hc S12 "write a private document" POST /v1/knowledge \
    '{"concept_id":"e2e/private-doc","document":{"type":"Concept","title":"Private","visibility":"private"}}' 200 "Authorization: Bearer $TOK_ALL"
  a_json_eq concept_id 'e2e/private-doc'
  end_case

  hc S13 "another user reading a private document gets 404" GET '/v1/knowledge?id=e2e%2Fprivate-doc' '' 404 "Authorization: Bearer $TOK_READ"
  a_json_eq error 'document not found or access denied'
  end_case

  hc S14 "owner can read their private document" GET '/v1/knowledge?id=e2e%2Fprivate-doc' '' 200 "Authorization: Bearer $TOK_ALL"
  a_json_eq visibility private
  end_case

  hc S15 "subject spoofing in the query string does not change identity" GET '/v1/knowledge?id=e2e%2Fprivate-doc&subject=alice' '' 404 "Authorization: Bearer $TOK_READ"
  a_json_eq error 'document not found or access denied'
  end_case

  hc S16 "Origin: null is rejected" GET /healthz '' 403 'Origin: null'
  a_json_eq error 'origin not allowed'
  end_case

  hc S17 "non-allowlisted cross-site Origin is rejected" GET /v1/concepts '' 403 \
    'Origin: https://evil.example.test' "Authorization: Bearer $TOK_ALL"
  a_json_eq error 'origin not allowed'
  end_case

  hc S18 "allowlisted cross-site Origin is allowed" GET /v1/concepts '' 200 \
    'Origin: https://console.example.test' "Authorization: Bearer $TOK_ALL"
  end_case

  hc S19 "405 determination happens after authentication" POST /healthz '' 401
  a_json_eq error 'authentication required'
  http_req POST /healthz '' "Authorization: Bearer $TOK_ALL"
  eq "$STATUS" 405 'wrong method with token'
  end_case

  hc S20 "error responses do not echo credentials or DSN" GET /v1/concepts '' 401
  a_body_hasnt 'postgres://'
  a_body_hasnt 'e2e-all-'
  a_body_hasnt 'SELECT'
  end_case

  hc S21 "console remains publicly readable with auth enabled" GET / '' 200
  a_header Content-Type text/html
  a_json_absent error
  end_case

  begin_case S22 "MCP write tools follow the switch and the per-tool scope"
  http_req POST /mcp '{"jsonrpc":"2.0","id":31,"method":"tools/list"}' "Authorization: Bearer $TOK_ALL"
  a_status 200
  has "$(cat "$BODY")" 'put_knowledge' 'write tool list'
  has "$(cat "$BODY")" 'rebuild_graph_index' 'write tool list'
  http_req POST /mcp \
    '{"jsonrpc":"2.0","id":32,"method":"tools/call","params":{"name":"put_knowledge","arguments":{"concept_id":"e2e/mcp-write","document":{"type":"Concept","title":"MCP","visibility":"public"}}}}' \
    "Authorization: Bearer $TOK_ALL"
  a_status 200
  a_json_eq result.isError false
  a_json_eq result.structuredContent.concept_id e2e/mcp-write
  a_json_has result.structuredContent.content_hash
  http_req GET '/v1/knowledge?id=e2e%2Fmcp-write' '' "Authorization: Bearer $TOK_ALL"
  a_status 200
  a_json_eq title MCP
  end_case

  begin_case S23 "MCP write tools reject a caller without the matching scope"
  http_req POST /mcp \
    '{"jsonrpc":"2.0","id":33,"method":"tools/call","params":{"name":"put_knowledge","arguments":{"concept_id":"e2e/mcp-forbidden","document":{"type":"Concept"}}}}' \
    "Authorization: Bearer $TOK_READ"
  a_status 200
  a_json_eq error.code -32003
  a_json_eq error.message forbidden
  a_json_absent result
  http_req GET '/v1/knowledge?id=e2e%2Fmcp-forbidden' '' "Authorization: Bearer $TOK_ALL"
  a_status 404
  end_case

  begin_case S24 "MCP write tools that need graph:write are refused for a knowledge writer"
  http_req POST /mcp \
    '{"jsonrpc":"2.0","id":34,"method":"tools/call","params":{"name":"put_graph_node","arguments":{"concept_id":"e2e/mcp-node"}}}' \
    "Authorization: Bearer $TOK_ALL"
  a_status 200
  a_json_eq error.code -32003
  a_json_eq error.message forbidden
  end_case

  begin_case S25 "the MCP transport authenticates before it dispatches"
  http_req POST /mcp '{"jsonrpc":"2.0","id":41,"method":"initialize"}'
  a_status 401
  a_json_eq error 'authentication required'
  a_header WWW-Authenticate 'Bearer realm="agentmem"'
  end_case

  begin_case S26 "graph:write writes a node but cannot read knowledge"
  http_req PUT /v1/graph/node \
    '{"concept_id":"e2e/graph-scoped","node_type":"metric","label":"Scoped"}' \
    "Authorization: Bearer $TOK_GRAPH"
  a_status 200
  a_json_has content_hash
  http_req GET /v1/concepts '' "Authorization: Bearer $TOK_GRAPH"
  a_status 403
  a_json_eq error 'permission denied'
  end_case

  begin_case S27 "reindex follows migrate:run rather than graph:write"
  http_req POST /v1/graph/index '{}' "Authorization: Bearer $TOK_GRAPH"
  a_status 403
  a_json_eq error 'permission denied'
  http_req POST /v1/graph/index '{}' "Authorization: Bearer $TOK_MIGRATE"
  a_status 200
  a_json_has nodes
  end_case
else
  fail_case S00 "auth case group" "service failed to start: $(head -c 200 "$WORK/serve-auth.log" | tr '\n' ' ')"
fi
stop_server

# ===========================================================================
section "5. Rate limiting (separate low-limit profile)"
# ===========================================================================
if start_server rate "$RATE_ENV" "$PORT_RATE"; then
  begin_case R01 "exceeding the token-bucket rate returns 429"
  rate_found=0
  for _i in $(seq 1 30); do
    http_req GET /healthz ''
    if [[ "$STATUS" == "429" ]]; then
      rate_found=1
      cp "$BODY" "$WORK/rate429.body"
      break
    fi
  done
  if [[ "$rate_found" -eq 1 ]]; then ck 0 ""; else ck 1 "no 429 in 30 consecutive requests"; fi
  has "$(head -c 500 "$WORK/rate429.body")" 'rate limit exceeded' '429 response body'
  end_case
else
  fail_case R00 "rate-limit case group" "service failed to start: $(head -c 200 "$WORK/serve-rate.log" | tr '\n' ' ')"
fi
stop_server

# ===========================================================================
section "6. Cross-process state consistency (all servers stopped)"
# ===========================================================================
begin_case X01 "CLI concepts sees the concept written over HTTP"
run_cli concepts "${ENVARG[@]}"
a_rc 0
a_cli_json_contains '' "$HTTP_DOC"
end_case

begin_case X02 "CLI decisions retains the full audit trail"
run_cli decisions "${ENVARG[@]}" --limit 100
a_rc 0
a_cli_json_len_min '' 1
a_cli_json_has 0.concept_id
end_case

begin_case X03 "CLI computations retains the HTTP computation record"
run_cli computations "${ENVARG[@]}" --limit 100
a_rc 0
a_cli_json_len_min '' 1
a_cli_json_has 0.verdict
end_case

begin_case X04 "offline snapshot is written and is valid JSON"
if [[ -f "$WORK/meta.fake.json" ]]; then ck 0 ""; else ck 1 'missing meta.fake.json'; fi
python3 -c 'import json,sys; json.load(open(sys.argv[1], encoding="utf-8"))' \
  "$WORK/meta.fake.json" >/dev/null 2>&1
ck $? 'snapshot is not valid JSON'
end_case

begin_case X05 "OKF+Git bodies are written to the temporary bundle"
e2e_md=0
if [[ -d "$WORK/bundle/e2e" ]]; then
  e2e_md="$(find "$WORK/bundle/e2e" -type f -name '*.md' | wc -l)"
fi
if [[ "$e2e_md" -ge 1 ]]; then ck 0 ""; else ck 1 'no e2e concept body found in bundle'; fi
end_case

# ===========================================================================
section "7. Web console (binary-hosted + static self-check)"
# ===========================================================================
begin_case W01 "index.html carries a strict CSP"
web_html="$(cat "$REPO/web/index.html")"
has "$web_html" 'http-equiv="Content-Security-Policy"' 'CSP meta'
has "$web_html" "default-src 'none'" 'default-src'
end_case

begin_case W02 "no external CDN assets"
if grep -Eq '(src|href)="https?://' "$REPO/web/index.html"; then
  ck 1 'index.html references an external resource'
else
  ck 0 ''
fi
end_case

begin_case W03 "API docs mirror 23 method+path entries"
web_routes="$(grep -c "    method: '" "$REPO/web/src/lib/api-content.js")"
eq "$web_routes" 23 'endpoint count'
end_case

begin_case W04 "all 13 language directories present"
web_langs="$(cat "$REPO/web/src/lib/languages.js")"
has "$web_langs" 'zh-TW' 'Traditional Chinese'
has "$web_langs" 'hi' 'Hindi'
web_tests="$(ls "$REPO"/web/tests/*.test.mjs 2>/dev/null | wc -l)"
ge "$web_tests" 10 'offline test file count'
end_case

begin_case W06 "docs page provides interactive try-it"
web_content="$(cat "$REPO/web/src/lib/api-content.js")"
has "$web_content" 'export const tryMeta' 'tryMeta metadata'
web_try="$(cat "$REPO/web/src/lib/copy/try-copy.js")"
has "$web_try" "'trySend'" 'send button copy'
has "$web_try" 'tryErrAuth' 'auth failure hint'
web_view="$(cat "$REPO/web/src/views/try-it.js")"
has "$web_view" 'buildTryIt' 'try-it panel'
web_trylib="$(cat "$REPO/web/src/lib/api-try.js")"
has "$web_trylib" "TOKEN_KEY = 'agentmem.token'" 'token key name'
has "$web_trylib" 'sessionStorage' 'session-level storage'
end_case

if command -v bun >/dev/null 2>&1; then
  begin_case W05 "bun test (Web offline node:test suite)"
  (cd "$REPO/web" && bun test) >"$WORK/web-test.log" 2>&1
  web_test_rc=$?
  ck "$web_test_rc" "bun test exit code $web_test_rc: $(tail -n 3 "$WORK/web-test.log" | tr '\n' ' ' | head -c 200)"
  end_case
else
  skip_case W05 "bun test (Web offline node:test suite)" "bun not installed"
fi

# ===========================================================================
section "8. Existing quality gates"
# ===========================================================================
run_gate() { # ID DESC COMMAND...
  local id="$1" desc="$2"
  shift 2
  begin_case "$id" "$desc"
  "$@" >"$WORK/gate-$id.log" 2>&1
  local rc=$?
  ck "$rc" "exit code $rc: $(tail -n 3 "$WORK/gate-$id.log" | tr '\n' ' ' | head -c 200)"
  end_case
}

if [[ "$NO_GATES" -eq 1 ]]; then
  skip_case G00 "existing quality gates" "--no-gates specified"
else
  run_gate G02 "go vet ./..." go vet ./...
  run_gate G03 "go test ./... -count=1 (offline)" go test ./... -count=1
  run_gate G04 "go build ./..." go build ./...
  run_gate G05 "make validate (strict .env.example validation)" make validate
  run_gate G06 "make check-boundaries" make check-boundaries
  run_gate G07 "make eval" make eval
  if command -v openssl >/dev/null 2>&1; then
    run_gate G08 "make check-trust-store" make check-trust-store
  else
    skip_case G08 "make check-trust-store" "openssl not installed"
  fi
fi

# G09/G10 prove the coverage gate itself: that it rejects a bad profile and that
# it is still wired into make and CI. They must never be skipped with
# --no-gates, otherwise removing `make cover` would leave CI green (the only
# check able to notice would be the one that no longer runs). Both are
# second-scale checks, so they run on every invocation.
begin_case G09 "the coverage gate rejects a profile below 100%"
printf 'mode: atomic\nagentmem/internal/mcp/protocol.go:1.1,2.2 1 1\nagentmem/internal/mcp/protocol.go:3.3,4.4 1 0\n' >"$WORK/incomplete.out"
gate_rc=0
go run ./scripts/coverage -profile "$WORK/incomplete.out" >"$WORK/gate-G09.log" 2>&1 || gate_rc=$?
ck $(( gate_rc == 1 ? 0 : 1 )) "exit code $gate_rc (want 1)"
has "$(cat "$WORK/gate-G09.log")" '50.0% < 100.0%' 'below-threshold report'
end_case

begin_case G10 "the coverage gate is wired into make and CI"
has "$(cat "$REPO/Makefile")" 'cover:' 'make cover target'
has "$(cat "$REPO/.github/workflows/ci.yml")" 'make cover' 'CI step'
end_case

# ===========================================================================
section "Summary"
# ===========================================================================
printf 'PASS=%d  FAIL=%d  SKIP=%d  TOTAL=%d\n' "$PASS" "$FAIL" "$SKIP" "$((PASS + FAIL + SKIP))"
if (( FAIL > 0 )); then
  printf 'Result: FAIL\n'
  exit 1
fi
printf 'Result: PASS\n'
exit 0
