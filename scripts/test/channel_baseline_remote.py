"""Channel-side baseline snapshot — runs ON the domestic test host.

Executed through ``scripts/test/remote_exec.py``; the admin password arrives on
stdin and is never written to disk, argv or output. Collects proxy-pool health,
monitoring summary and model-health latest so audit failures can be attributed
to the channel (proxy pool / upstream gateway) rather than to a model.
"""

import json
import re
import sys
import urllib.error
import urllib.request

BASE = "http://127.0.0.1:3756"

password = sys.stdin.readline().rstrip("\r\n")
opener = urllib.request.build_opener(urllib.request.HTTPCookieProcessor())


def emit(kind, **fields):
    fields["kind"] = kind
    print("R|" + json.dumps(fields, sort_keys=True, separators=(",", ":")), flush=True)


def call(method, path, payload=None):
    data = None if payload is None else json.dumps(payload, separators=(",", ":")).encode()
    headers = {"Origin": BASE}
    if data is not None:
        headers["Content-Type"] = "application/json"
    request = urllib.request.Request(BASE + path, data=data, headers=headers, method=method)
    try:
        with opener.open(request, timeout=60) as response:
            return response.status, response.read()
    except urllib.error.HTTPError as error:
        return error.code, error.read()


def unwrap(body):
    value = json.loads(body)
    return value["data"] if isinstance(value, dict) and "data" in value else value


def main():
    status, _ = call("POST", "/admin/api/auth/login", {"username": "admin", "password": password})
    emit("meta", step="login", status=status)
    if status != 200:
        return 1
    try:
        status, body = call("GET", "/metrics")
        if status == 200:
            text = body.decode("utf-8", "replace")
            wanted = {}
            for line in text.splitlines():
                if line.startswith("#") or "nvidia_router" not in line:
                    continue
                match = re.match(r"^(\S+)\s+(\S+)$", line)
                if match and ("proxy_pool" in match.group(1) or "upstream" in match.group(1)):
                    wanted[match.group(1)] = match.group(2)
            emit("metrics", values=wanted)

        status, body = call("GET", "/admin/api/proxy-pool")
        if status == 200:
            pool = unwrap(body)
            emit("proxy_pool", summary={
                key: pool.get(key) for key in
                ("healthy", "total", "expected_qty", "enabled", "last_collect_at",
                 "last_error", "latency_samples", "state")
                if key in pool
            })

        status, body = call("GET", "/admin/api/monitoring/summary?range=24h")
        if status == 200:
            data = unwrap(body)
            emit("monitoring", summary={
                key: value for key, value in data.items()
                if isinstance(value, (int, float, str))
            })

        status, body = call("GET", "/admin/api/model-health/summary")
        if status == 200:
            data = unwrap(body)
            latest = data.get("latest") if isinstance(data, dict) else None
            if isinstance(latest, list):
                emit("model_health", rows=[{
                    "model": row.get("public_id") or row.get("model_id"),
                    "status": row.get("status"),
                    "latency_ms": row.get("latency_ms"),
                    "error_category": row.get("error_category"),
                    "checked_at": row.get("checked_at"),
                } for row in latest])
    finally:
        call("POST", "/admin/api/auth/logout")
    return 0


raise SystemExit(main())
