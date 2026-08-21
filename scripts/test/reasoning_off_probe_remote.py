"""Verify the reasoning-off fix on the live deployment — runs ON the domestic host.

Regression under test: `reasoning_effort:"none"` / `thinking:false` used to be
classified as a reasoning *capability requirement*, so every model whose catalog
row has supports_reasoning=false answered 501 not_implemented to any client that
sends a reasoning parameter as a global default.

The fix touches `RequiresReasoning()`, which is evaluated on **every** request, so
the live check that matters here is that reasoning-off still round-trips on the
enabled models — both wire formats — and that switching reasoning on still works.
The 501 half is unreachable on this deployment (every non-reasoning model in the
catalog is disabled) and is covered by the protocol unit tests instead; this probe
reports the catalog state so that stays visible.

The admin password arrives on stdin and is never written to disk, argv or output.
"""

import json
import sys
import urllib.error
import urllib.request

BASE = "http://127.0.0.1:3756"
RUN_NAME = "reasoning-off-probe"
# One model per reasoning wire format: NVIDIA speaks `thinking`, the OpenCodeFree
# gateway speaks bare `reasoning_effort`.
TARGETS = ("stepfun-ai/step-3.7-flash", "opencode-free/nemotron-3-ultra-free")

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


def chat(access_key, payload, timeout=180):
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
            record["completion_tokens"] = (body.get("usage") or {}).get("completion_tokens")
    except urllib.error.HTTPError as error:
        record["status"] = error.code
        try:
            body = json.loads(error.read(1 << 20))
            err = body.get("error", {}) if isinstance(body, dict) else {}
            record["error_code"] = err.get("code")
            record["error_message"] = str(err.get("message") or "")[:200]
        except Exception:  # noqa: BLE001
            record["error_code"] = "unparseable"
    except Exception as error:  # noqa: BLE001
        record["status"] = 0
        record["error_code"] = type(error).__name__
    return record


def main():
    anonymous = urllib.request.build_opener()
    status, _ = call_admin("GET", "/metrics", opener=anonymous)
    emit("metrics_anonymous", status=status)

    status, _ = call_admin("POST", "/admin/api/auth/login", {"username": "admin", "password": password})
    emit("meta", step="login", status=status)
    if status != 200:
        return 1

    access_id = None
    try:
        status, body = call_admin("GET", "/admin/api/models")
        if status != 200:
            emit("meta", step="list_models", status=status)
            return 1
        catalog = json.loads(body)
        catalog = catalog.get("data", catalog)
        emit("catalog_non_reasoning", rows=[{
            "public_id": m.get("public_id"), "enabled": m.get("enabled"),
        } for m in catalog if not m.get("supports_reasoning")])

        status, body = call_admin("POST", "/admin/api/access-keys", {"name": RUN_NAME})
        if status != 201:
            emit("meta", step="create_key", status=status)
            return 1
        created = json.loads(body)
        access_id, access_key = created["id"], created["key"]

        for target in TARGETS:
            base = {"model": target, "messages": [{"role": "user", "content": "Reply with exactly OK."}],
                    "max_tokens": 64}
            for label, extra in (
                ("no_reasoning_field", {}),
                ("reasoning_effort_none", {"reasoning_effort": "none"}),
                ("reasoning_effort_off", {"reasoning_effort": "off"}),
                ("thinking_false", {"thinking": False}),
                ("thinking_disabled", {"thinking": {"type": "disabled"}}),
                ("reasoning_effort_low", {"reasoning_effort": "low"}),
            ):
                emit("reasoning_variant", model=target, variant=label,
                     **chat(access_key, dict(base, **extra)))
    finally:
        if access_id is not None:
            call_admin("DELETE", "/admin/api/access-keys/%s" % access_id)
        call_admin("POST", "/admin/api/auth/logout")
    return 0


raise SystemExit(main())
