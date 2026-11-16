#!/usr/bin/env python3
"""Reference agentmem compute sidecar (wire protocol v1).

agentmem never links a warehouse SDK. ``bigquery``, ``dbt``, ``looker`` and
``python`` computations are executed by a sidecar it can reach over HTTP
(``POST {endpoint}/run``) or over stdio (``{command} run``). This file is a
complete, dependency-free implementation of that contract:

    request  -> {"runtime", "concept_id", "contract_hash", "body_kind",
                 "body", "parameters"}
    response <- {"columns", "rows", "body_digest"}      (nothing else)

Rules the Go side enforces, so a sidecar must obey:

* ``body_digest`` is the SHA-256 of the body this process actually executed.
  Go recomputes it and refuses to display any value on a mismatch.
* ``job_id``, the result and the display value are recomputed in Go. Returning
  those fields is a protocol violation (strict decoding rejects the reply).
* The body is data, never a command. A runtime that has to execute code (here:
  ``python``) is expected to run it in its own sandbox, with the credentials and
  the filesystem view it needs — and nothing more.
* Cells must be scalars (string / number / bool / null). Nested objects are
  rejected by Go, because a receipt has to stay recomputable.

Usage
-----
    # bridge: {"runtime":"bigquery","bridge":"cli","command":"agentmem-example"}
    agentmem-example run < request.json > response.json

    # bridge: {"runtime":"bigquery","bridge":"http","endpoint":"http://127.0.0.1:8791"}
    agentmem-example --addr 127.0.0.1:8791

    # Deterministic tables, no credentials, no network (use for demos and e2e):
    SIDECAR_OFFLINE=1 agentmem-example run < request.json

Environment (real mode only; offline mode ignores all of it):

    GOOGLE_APPLICATION_CREDENTIALS  bigquery: service-account JSON path
    BQ_PROJECT                      bigquery: project id passed to `bq`
    DBT_PROJECT_DIR                 dbt: directory holding dbt_project.yml
    DBT_PROFILES_DIR                dbt: profiles directory (optional)
    LOOKER_BASE_URL                 looker: https://looker.example.com
    LOOKER_CLIENT_ID / SECRET       looker: API3 credentials
    SIDECAR_OFFLINE=1               deterministic tables, no credentials

The switches deliberately avoid the AGENTMEM_ prefix: agentmem's own closed
configuration whitelist rejects unknown AGENTMEM_* keys in its process
environment, and a CLI bridge child inherits that environment. A sidecar-local
variable must therefore live in its own namespace.
"""

from __future__ import annotations

import hashlib
import json
import os
import subprocess
import sys
from typing import Any

PROTOCOL_FIELDS = {
    "runtime",
    "concept_id",
    "contract_hash",
    "body_kind",
    "body",
    "parameters",
}

# Deterministic tables for SIDECAR_OFFLINE=1. The values differ per
# runtime on purpose: an e2e failure then points at the dispatch, not at the
# plumbing.
OFFLINE_TABLES: dict[str, tuple[list[str], list[list[Any]]]] = {
    "bigquery": (["revenue"], [[1250]]),
    "dbt": (["revenue"], [[1310]]),
    "looker": (["revenue"], [[1420]]),
    "python": (["revenue"], [[1530]]),
}


class SidecarError(RuntimeError):
    """A request this sidecar refuses to execute."""


def digest(body: str) -> str:
    return hashlib.sha256(body.encode("utf-8")).hexdigest()


def table_from_rows(rows: list[dict[str, Any]]) -> tuple[list[str], list[list[Any]]]:
    """Normalise a list of row objects into the columnar wire shape."""
    if not rows:
        return [], []
    columns = list(rows[0].keys())
    values = [[row.get(column) for column in columns] for row in rows]
    return columns, values


def run_bigquery(request: dict[str, Any]) -> tuple[list[str], list[list[Any]]]:
    """Execute the sanctioned SQL with the bq CLI.

    `bq query` is used instead of a client library so this file keeps working
    with only the gcloud SDK installed. Parameters travel as typed flags, never
    as string interpolation into the SQL.
    """
    project = os.environ.get("BQ_PROJECT", "")
    if not project:
        raise SidecarError("bigquery: BQ_PROJECT is required")
    command = [
        "bq",
        f"--project_id={project}",
        "query",
        "--format=json",
        "--use_legacy_sql=false",
        "--quiet",
    ]
    for name, value in sorted(request.get("parameters", {}).items()):
        command.append(f"--parameter={name}:{bq_type(value)}:{value}")
    command.append(request["body"])
    output = subprocess.run(  # noqa: S603 - fixed argv, no shell, no user command
        command, check=True, capture_output=True, text=True, timeout=120
    ).stdout
    return table_from_rows(json.loads(output or "[]"))


def bq_type(value: Any) -> str:
    if isinstance(value, bool):
        return "BOOL"
    if isinstance(value, int):
        return "INT64"
    if isinstance(value, float):
        return "FLOAT64"
    return "STRING"


def run_dbt(request: dict[str, Any]) -> tuple[list[str], list[list[Any]]]:
    """Run the sanctioned model selector with `dbt show`.

    The body is a model selector document (``model`` / ``selector``), not SQL:
    the dbt project owns compilation and warehouse access.
    """
    project_dir = os.environ.get("DBT_PROJECT_DIR")
    if not project_dir:
        raise SidecarError("dbt: DBT_PROJECT_DIR is required")
    selector = json.loads(request["body"]) if request["body"].strip() else {}
    model = selector.get("model")
    if not model:
        raise SidecarError("dbt: the sanctioned body must name a model")
    environment = dict(os.environ, DBT_PROJECT_DIR=project_dir)
    if profile := os.environ.get("DBT_PROFILES_DIR"):
        environment["DBT_PROFILES_DIR"] = profile
    for name, value in sorted(request.get("parameters", {}).items()):
        # dbt consumes parameters as CLI vars so the model stays untouched.
        environment[f"AGENTMEM_VAR_{name.upper()}"] = str(value)
    output = subprocess.run(  # noqa: S603 - fixed argv, no shell
        ["dbt", "show", "--select", str(model), "--output", "json"],
        cwd=project_dir,
        env=environment,
        check=True,
        capture_output=True,
        text=True,
        timeout=600,
    ).stdout
    return table_from_rows(json.loads(output or "[]"))


def run_looker(request: dict[str, Any]) -> tuple[list[str], list[list[Any]]]:
    """Run the sanctioned Looker query reference through the REST API."""
    base = os.environ.get("LOOKER_BASE_URL", "").rstrip("/")
    client_id = os.environ.get("LOOKER_CLIENT_ID", "")
    secret = os.environ.get("LOOKER_CLIENT_SECRET", "")
    if not base or not client_id or not secret:
        raise SidecarError("looker: LOOKER_BASE_URL, LOOKER_CLIENT_ID and LOOKER_CLIENT_SECRET are required")
    reference = json.loads(request["body"]) if request["body"].strip() else {}
    query: dict[str, Any] = {"model": reference.get("model", "agentmem"), "view": reference.get("explore")}
    if query_id := reference.get("query_id"):
        query["query_id"] = query_id
    filters = {name: str(value) for name, value in request.get("parameters", {}).items()}
    if filters:
        query["filters"] = filters
    token = looker_token(base, client_id, secret)
    body = json.dumps(query).encode("utf-8")
    reply = looker_request(base, "/api/4.0/queries/run/json", token, body)
    rows = json.loads(reply or "[]")
    if rows and isinstance(rows[0], dict):
        return table_from_rows(rows)
    return [], rows


def looker_token(base: str, client_id: str, secret: str) -> str:
    payload = f"client_id={client_id}&client_secret={secret}".encode("utf-8")
    reply = looker_request(base, "/api/4.0/login", "", payload, form=True)
    token = json.loads(reply).get("access_token", "")
    if not token:
        raise SidecarError("looker: login did not return an access token")
    return token


def looker_request(base: str, path: str, token: str, body: bytes, form: bool = False) -> str:
    import urllib.request

    headers = {"Accept": "application/json"}
    if form:
        headers["Content-Type"] = "application/x-www-form-urlencoded"
    else:
        headers["Content-Type"] = "application/json"
        if token:
            headers["Authorization"] = f"token {token}"
    request = urllib.request.Request(base + path, data=body, headers=headers, method="POST")
    with urllib.request.urlopen(request, timeout=300) as reply:  # noqa: S310 - configured host only
        return reply.read().decode("utf-8")


def run_python(request: dict[str, Any]) -> tuple[list[str], list[list[Any]]]:
    """Execute the sanctioned script in a child interpreter.

    The body is a script the operator approved and stored in OKF+Git; agentmem
    only supplied parameter values. The child gets a clean environment plus the
    declared parameters as AGENTMEM_PARAM_* variables, and must print a JSON
    object of scalar cells on stdout.
    """
    environment = {"PATH": os.environ.get("PATH", "/usr/bin:/bin")}
    for name, value in sorted(request.get("parameters", {}).items()):
        environment[f"AGENTMEM_PARAM_{name.upper()}"] = json.dumps(value)
    result = subprocess.run(  # noqa: S603 - sanctioned body, no shell
        [sys.executable, "-I", "-"],
        input=request["body"],
        env=environment,
        check=True,
        capture_output=True,
        text=True,
        timeout=120,
    )
    payload = json.loads(result.stdout or "{}")
    if isinstance(payload, dict):
        return list(payload.keys()), [list(payload.values())]
    return table_from_rows(payload)


RUNNERS = {
    "bigquery": run_bigquery,
    "dbt": run_dbt,
    "looker": run_looker,
    "python": run_python,
}


def execute(request: dict[str, Any]) -> dict[str, Any]:
    """Validate the request and return the only reply shape Go will accept."""
    unknown = set(request) - PROTOCOL_FIELDS
    if unknown:
        raise SidecarError(f"unknown request fields: {', '.join(sorted(unknown))}")
    runtime = str(request.get("runtime", "")).lower()
    body = request.get("body", "")
    if not isinstance(body, str) or not body:
        raise SidecarError("body must be a non-empty string")
    if runtime not in RUNNERS:
        raise SidecarError(f"unsupported runtime {runtime!r}")
    if os.environ.get("SIDECAR_OFFLINE") == "1":
        columns, rows = OFFLINE_TABLES[runtime]
    else:
        columns, rows = RUNNERS[runtime](request)
    return {
        "columns": columns,
        "rows": rows,
        # Echo the digest of what this process was asked to run. Go recomputes
        # it from the sanctioned body; a mismatch suppresses the display value.
        "body_digest": digest(body),
    }


def serve_stdio() -> int:
    try:
        request = json.load(sys.stdin)
        json.dump(execute(request), sys.stdout)
        return 0
    except (SidecarError, json.JSONDecodeError, subprocess.SubprocessError) as exc:
        print(f"sidecar: {exc}", file=sys.stderr)
        return 1


def serve_http(addr: str) -> int:
    from http.server import BaseHTTPRequestHandler, HTTPServer

    class Handler(BaseHTTPRequestHandler):
        def do_POST(self) -> None:  # noqa: N802 - stdlib naming
            if self.path != "/run":
                self.send_error(404)
                return
            length = int(self.headers.get("Content-Length") or 0)
            try:
                request = json.loads(self.rfile.read(length) or b"{}")
                reply = execute(request)
            except (SidecarError, json.JSONDecodeError, subprocess.SubprocessError) as exc:
                self.send_error(400, str(exc))
                return
            payload = json.dumps(reply).encode("utf-8")
            self.send_response(200)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(payload)))
            self.end_headers()
            self.wfile.write(payload)

        def log_message(self, fmt: str, *args: Any) -> None:
            # Never log bodies or parameters: only the request line.
            sys.stderr.write("sidecar: %s\n" % (fmt % args))

    host, _, port = addr.rpartition(":")
    server = HTTPServer((host or "127.0.0.1", int(port)), Handler)
    server.serve_forever()
    return 0


def main(argv: list[str]) -> int:
    if "--addr" in argv:
        return serve_http(argv[argv.index("--addr") + 1])
    if len(argv) == 2 and argv[1] == "run":
        return serve_stdio()
    print(
        "usage: sidecar.py run < request.json | sidecar.py --addr HOST:PORT",
        file=sys.stderr,
    )
    return 2


if __name__ == "__main__":
    sys.exit(main(sys.argv))
