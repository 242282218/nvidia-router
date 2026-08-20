"""Reproduce the fast 502 upstream_proxy_unavailable window — runs ON the host.

Fires short chat requests in rounds while sampling the proxy-pool gauges, so a
failure burst can be correlated with what the pool looked like at that instant.
The admin password arrives on stdin and is never written to disk, argv or output.

Placeholders: __MODELS__, __ROUNDS__, __WORKERS__, __TIMEOUT__.
"""

import json
import re
import sys
import threading
import time
import urllib.error
import urllib.request
from concurrent.futures import ThreadPoolExecutor

BASE = "http://127.0.0.1:3756"
MODELS = json.loads("""__MODELS__""")
ROUNDS = int("__ROUNDS__")
WORKERS = int("__WORKERS__")
TIMEOUT = float("__TIMEOUT__")
RUN_NAME = "proxy-502-repro"

password = sys.stdin.readline().rstrip("\r\n")
opener = urllib.request.build_opener(urllib.request.HTTPCookieProcessor())
admin_lock = threading.Lock()
print_lock = threading.Lock()
stop = threading.Event()
started = time.monotonic()


def stamp():
    return round(time.monotonic() - started, 3)


def emit(kind, **fields):
    fields["kind"] = kind
    fields["t"] = stamp()
    with print_lock:
        print("R|" + json.dumps(fields, sort_keys=True, separators=(",", ":")), flush=True)


def call_admin(method, path, payload=None):
    data = None if payload is None else json.dumps(payload, separators=(",", ":")).encode()
    headers = {"Origin": BASE}
    if data is not None:
        headers["Content-Type"] = "application/json"
    request = urllib.request.Request(BASE + path, data=data, headers=headers, method=method)
    with admin_lock:
        try:
            with opener.open(request, timeout=60) as response:
                return response.status, response.read()
        except urllib.error.HTTPError as error:
            return error.code, error.read()


def sample_pool():
    """Poll the pool gauges once per second for the whole run."""
    while not stop.is_set():
        status, body = call_admin("GET", "/metrics")
        if status == 200:
            values = {}
            for line in body.decode("utf-8", "replace").splitlines():
                if line.startswith("#") or "proxy_pool" not in line:
                    continue
                match = re.match(r"^(\S+)\s+(\S+)$", line)
                if match:
                    values[match.group(1).replace("nvidia_router_proxy_pool_", "")] = match.group(2)
            if values:
                emit("pool", values=values)
        stop.wait(1.0)


def fire(access_key, model, index):
    begin = time.monotonic()
    payload = {
        "model": model,
        "messages": [{"role": "user", "content": "Reply with exactly OK."}],
        "max_tokens": 8,
    }
    request = urllib.request.Request(
        BASE + "/v1/chat/completions",
        data=json.dumps(payload, separators=(",", ":")).encode(),
        headers={
            "Authorization": "Bearer " + access_key,
            "Content-Type": "application/json",
            "Connection": "close",
        },
        method="POST",
    )
    record = {"model": model, "index": index}
    try:
        with urllib.request.urlopen(request, timeout=TIMEOUT) as response:
            record["status"] = response.status
            response.read()
    except urllib.error.HTTPError as error:
        body = error.read(64 * 1024)
        record["status"] = error.code
        try:
            payload = json.loads(body)
            err = payload.get("error", {}) if isinstance(payload, dict) else {}
            record["error_code"] = err.get("code")
            record["error_message"] = str(err.get("message") or "")[:120]
        except Exception:  # noqa: BLE001
            record["error_code"] = "unparseable"
    except Exception as error:  # noqa: BLE001
        record["status"] = 0
        record["error_code"] = type(error).__name__
    record["ms"] = int((time.monotonic() - begin) * 1000)
    emit("call", **record)


def main():
    status, _ = call_admin("POST", "/admin/api/auth/login", {"username": "admin", "password": password})
    emit("meta", step="login", status=status)
    if status != 200:
        return 1
    access_id = None
    try:
        status, body = call_admin("GET", "/admin/api/access-keys")
        if status == 200:
            payload = json.loads(body)
            for item in payload.get("data", payload):
                if item.get("name") == RUN_NAME:
                    call_admin("DELETE", "/admin/api/access-keys/%s" % item["id"])
        status, body = call_admin("POST", "/admin/api/access-keys", {"name": RUN_NAME})
        if status != 201:
            emit("meta", step="create_key", status=status)
            return 1
        created = json.loads(body)
        access_id, access_key = created["id"], created["key"]

        sampler = threading.Thread(target=sample_pool, daemon=True)
        sampler.start()
        with ThreadPoolExecutor(max_workers=WORKERS) as pool:
            for round_index in range(ROUNDS):
                jobs = [(model, round_index) for model in MODELS]
                list(pool.map(lambda item: fire(access_key, *item), jobs))
        stop.set()
        sampler.join(timeout=3)
    finally:
        stop.set()
        if access_id is not None:
            call_admin("DELETE", "/admin/api/access-keys/%s" % access_id)
        call_admin("POST", "/admin/api/auth/logout")
    return 0


raise SystemExit(main())
