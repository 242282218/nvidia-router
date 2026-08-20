"""Post-deploy verification — runs ON the domestic test host.

Checks the fixes that just shipped: /metrics now requires the management gate,
the reasoning capability backfill (migration 038) reaches the two NVIDIA models
that emit reasoning_content, and the thinking budget is reconciled against
max_tokens. The admin password arrives on stdin and is never written to disk,
argv or output.
"""

import json
import sys
import urllib.error
import urllib.request

BASE = "http://127.0.0.1:3756"
RUN_NAME = "post-deploy-verify"

password = sys.stdin.readline().rstrip("\r\n")
authed = urllib.request.build_opener(urllib.request.HTTPCookieProcessor())


def emit(kind, **fields):
    fields["kind"] = kind
    print("R|" + json.dumps(fields, sort_keys=True, separators=(",", ":")), flush=True)


def call_admin(method, path, payload=None, opener=None):
    data = None if payload is None else json.dumps(payload, separators=(",", ":")).encode()
    headers = {"Origin": BASE}
    if data is not None:
        headers["Content-Type"] = "application/json"
    request = urllib.request.Request(BASE + path, data=data, headers=headers, method=method)
    try:
        with (opener or authed).open(request, timeout=60) as response:
            return response.status, response.read()
    except urllib.error.HTTPError as error:
        return error.code, error.read()


def chat(access_key, payload, timeout=240):
    request = urllib.request.Request(
        BASE + "/v1/chat/completions",
        data=json.dumps(payload, separators=(",", ":")).encode(),
        headers={"Authorization": "Bearer " + access_key, "Content-Type": "application/json"},
        method="POST",
    )
    record = {}
    try:
        with urllib.request.urlopen(request, timeout=timeout) as response:
            record["status"] = response.status
            body = json.loads(response.read(1 << 22))
            choice = (body.get("choices") or [{}])[0] or {}
            message = choice.get("message") or {}
            record["content_chars"] = len(str(message.get("content") or ""))
            record["reasoning_chars"] = len(str(message.get("reasoning_content") or ""))
            record["finish_reason"] = choice.get("finish_reason")
            record["completion_tokens"] = (body.get("usage") or {}).get("completion_tokens")
    except urllib.error.HTTPError as error:
        record["status"] = error.code
        try:
            payload = json.loads(error.read(1 << 20))
            err = payload.get("error", {}) if isinstance(payload, dict) else {}
            record["error_code"] = err.get("code")
            record["error_message"] = str(err.get("message") or "")[:160]
        except Exception:  # noqa: BLE001
            record["error_code"] = "unparseable"
    except Exception as error:  # noqa: BLE001
        record["status"] = 0
        record["error_code"] = type(error).__name__
    return record


def main():
    # 1. /metrics must reject an unauthenticated caller.
    anonymous = urllib.request.build_opener()
    status, _ = call_admin("GET", "/metrics", opener=anonymous)
    emit("metrics_anonymous", status=status)

    status, _ = call_admin("POST", "/admin/api/auth/login", {"username": "admin", "password": password})
    emit("meta", step="login", status=status)
    if status != 200:
        return 1

    status, body = call_admin("GET", "/metrics")
    emit("metrics_authenticated", status=status, has_pool_gauge=(b"proxy_pool" in body))

    access_id = None
    try:
        status, body = call_admin("GET", "/admin/api/models")
        catalog = json.loads(body)
        catalog = catalog.get("data", catalog)
        emit("reasoning_flags", rows=[{
            "public_id": item.get("public_id"),
            "supports_reasoning": item.get("supports_reasoning"),
            "wire": item.get("reasoning_wire_format"),
        } for item in catalog if item.get("public_id") in (
            "nvidia/nemotron-3-ultra-550b-a55b", "stepfun-ai/step-3.7-flash",
            "minimaxai/minimax-m3", "z-ai/glm-5.2")])

        status, body = call_admin("POST", "/admin/api/access-keys", {"name": RUN_NAME})
        if status != 201:
            emit("meta", step="create_key", status=status)
            return 1
        created = json.loads(body)
        access_id, access_key = created["id"], created["key"]

        # 2. The two backfilled models must now accept reasoning_effort (was 501).
        for model in ("nvidia/nemotron-3-ultra-550b-a55b", "stepfun-ai/step-3.7-flash"):
            emit("reasoning_accepted", model=model, **chat(access_key, {
                "model": model,
                "messages": [{"role": "user", "content": "Reply with exactly OK."}],
                "max_tokens": 64,
                "reasoning_effort": "none",
            }))

        # 3. Budget reconciliation: a small allowance with high effort must still
        #    leave room for an answer instead of spending everything on thinking.
        emit("budget_reconciled", model="nvidia/nemotron-3-ultra-550b-a55b", **chat(access_key, {
            "model": "nvidia/nemotron-3-ultra-550b-a55b",
            "messages": [{"role": "user", "content": "Name one HTTP status code for rate limiting. Answer in one line."}],
            "max_tokens": 512,
            "reasoning_effort": "high",
        }))
    finally:
        if access_id is not None:
            call_admin("DELETE", "/admin/api/access-keys/%s" % access_id)
        call_admin("POST", "/admin/api/auth/logout")
    return 0


raise SystemExit(main())
