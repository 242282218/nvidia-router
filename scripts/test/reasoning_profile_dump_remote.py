"""Dump the live reasoning profile of every enabled model — runs ON the domestic host.

Read-only. Needed to tell apart two explanations for reasoning_content still
appearing when a client asks for reasoning_effort:"none":
  - the router forwarded thinking:{"type":"disabled"} and the upstream ignored it, or
  - the catalog row has reasoning_zero_allowed=false, so availableLevels() drops
    `none` and nearestLevel() upgrades the request to the cheapest thinking level.

The admin password arrives on stdin and is never written to disk, argv or output.
"""

import json
import sys
import urllib.error
import urllib.request

BASE = "http://127.0.0.1:3756"

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


status, _ = call_admin("POST", "/admin/api/auth/login", {"username": "admin", "password": password})
print("login:", status, flush=True)
if status != 200:
    raise SystemExit(1)
try:
    status, body = call_admin("GET", "/admin/api/models")
    catalog = json.loads(body)
    catalog = catalog.get("data", catalog)
    for model in catalog:
        if not model.get("enabled"):
            continue
        print("R|" + json.dumps({
            "public_id": model.get("public_id"),
            "provider": model.get("provider"),
            "wire": model.get("reasoning_wire_format"),
            "levels": model.get("reasoning_levels"),
            "zero_allowed": model.get("reasoning_zero_allowed", False),
            "dynamic_allowed": model.get("reasoning_dynamic_allowed", False),
            "min_budget": model.get("reasoning_min_budget", 0),
            "max_budget": model.get("reasoning_max_budget", 0),
        }, sort_keys=True, separators=(",", ":")), flush=True)
finally:
    call_admin("POST", "/admin/api/auth/logout")
