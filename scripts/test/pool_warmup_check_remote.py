"""Check proxy-pool health and re-probe the NVIDIA channel — runs ON the host.

Used after a redeploy: the container restart rebuilds the XApi egress pool from
scratch, so NVIDIA-channel calls can return 502 upstream_proxy_unavailable until
collection and validation finish. This separates "pool still warming" from a real
regression by reading the pool gauge before retrying.

The admin password arrives on stdin and is never written to disk, argv or output.
"""

import json
import re
import sys
import time
import urllib.error
import urllib.request

BASE = "http://127.0.0.1:3756"
MODEL = "stepfun-ai/step-3.7-flash"

password = sys.stdin.readline().rstrip("\r\n")
authed = urllib.request.build_opener(urllib.request.HTTPCookieProcessor())


def call_admin(method, path, payload=None):
    data = None if payload is None else json.dumps(payload, separators=(",", ":")).encode()
    headers = {"Origin": BASE}
    if data is not None:
        headers["Content-Type"] = "application/json"
    request = urllib.request.Request(BASE + path, data=data, headers=headers, method=method)
    try:
        with authed.open(request, timeout=60) as response:
            return response.status, response.read()
    except urllib.error.HTTPError as error:
        return error.code, error.read()


def pool_gauges(body):
    text = body.decode("utf-8", "replace")
    found = {}
    for name in ("proxy_pool_healthy", "proxy_pool_total"):
        match = re.search(r"^\S*%s\S*\s+([0-9.]+)$" % name, text, re.M)
        if match:
            found[name] = match.group(1)
    return found


status, _ = call_admin("POST", "/admin/api/auth/login", {"username": "admin", "password": password})
print("login:", status, flush=True)
if status != 200:
    raise SystemExit(1)

access_id = None
try:
    status, body = call_admin("GET", "/metrics")
    print("pool gauges:", pool_gauges(body), flush=True)

    status, body = call_admin("POST", "/admin/api/access-keys", {"name": "pool-warmup-check"})
    if status != 201:
        raise SystemExit("create key: %s" % status)
    created = json.loads(body)
    access_id, access_key = created["id"], created["key"]

    for attempt in range(1, 6):
        request = urllib.request.Request(
            BASE + "/v1/chat/completions",
            data=json.dumps({"model": MODEL, "max_tokens": 32,
                             "messages": [{"role": "user", "content": "Reply with exactly OK."}]}).encode(),
            headers={"Authorization": "Bearer " + access_key, "Content-Type": "application/json"},
            method="POST",
        )
        try:
            with urllib.request.urlopen(request, timeout=180) as response:
                json.loads(response.read(1 << 22))
                print("attempt %d: 200" % attempt, flush=True)
        except urllib.error.HTTPError as error:
            payload = json.loads(error.read(1 << 20) or b"{}")
            code = (payload.get("error") or {}).get("code")
            print("attempt %d: %d %s" % (attempt, error.code, code), flush=True)
        except Exception as error:  # noqa: BLE001
            print("attempt %d: %s" % (attempt, type(error).__name__), flush=True)
        if attempt < 5:
            time.sleep(20)

    status, body = call_admin("GET", "/metrics")
    print("pool gauges after:", pool_gauges(body), flush=True)
finally:
    if access_id is not None:
        call_admin("DELETE", "/admin/api/access-keys/%s" % access_id)
    call_admin("POST", "/admin/api/auth/logout")
