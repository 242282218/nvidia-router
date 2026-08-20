"""OpenCodeFree channel diagnosis — runs ON the domestic test host.

Lists what the gateway currently advertises (admin candidate discovery) and
probes every OpenCodeFree model in the whitelist, capturing the exact status and
error the router produces. The admin password arrives on stdin and is never
written to disk, argv or output.
"""

import json
import sys
import urllib.error
import urllib.request

BASE = "http://127.0.0.1:3756"
RUN_NAME = "ocf-diagnosis"

password = sys.stdin.readline().rstrip("\r\n")
opener = urllib.request.build_opener(urllib.request.HTTPCookieProcessor())


def emit(kind, **fields):
    fields["kind"] = kind
    print("R|" + json.dumps(fields, sort_keys=True, separators=(",", ":")), flush=True)


def call_admin(method, path, payload=None):
    data = None if payload is None else json.dumps(payload, separators=(",", ":")).encode()
    headers = {"Origin": BASE}
    if data is not None:
        headers["Content-Type"] = "application/json"
    request = urllib.request.Request(BASE + path, data=data, headers=headers, method=method)
    try:
        with opener.open(request, timeout=120) as response:
            return response.status, response.read()
    except urllib.error.HTTPError as error:
        return error.code, error.read()


def unwrap(body):
    value = json.loads(body)
    return value["data"] if isinstance(value, dict) and "data" in value else value


def probe(access_key, model):
    payload = {
        "model": model,
        "messages": [{"role": "user", "content": "Reply with exactly OK."}],
        "max_tokens": 16,
    }
    request = urllib.request.Request(
        BASE + "/v1/chat/completions",
        data=json.dumps(payload, separators=(",", ":")).encode(),
        headers={"Authorization": "Bearer " + access_key, "Content-Type": "application/json"},
        method="POST",
    )
    record = {"model": model}
    try:
        with urllib.request.urlopen(request, timeout=180) as response:
            record["status"] = response.status
            body = json.loads(response.read(1 << 20))
            choices = body.get("choices") or [{}]
            message = (choices[0] or {}).get("message") or {}
            record["content"] = str(message.get("content") or "")[:80]
            record["finish_reason"] = (choices[0] or {}).get("finish_reason")
    except urllib.error.HTTPError as error:
        record["status"] = error.code
        try:
            payload = json.loads(error.read(1 << 20))
            err = payload.get("error", {}) if isinstance(payload, dict) else {}
            record["error_code"] = err.get("code")
            record["error_message"] = str(err.get("message") or "")[:200]
        except Exception:  # noqa: BLE001
            record["error_code"] = "unparseable"
    except Exception as error:  # noqa: BLE001
        record["status"] = 0
        record["error_code"] = type(error).__name__
    emit("probe", **record)


def main():
    status, _ = call_admin("POST", "/admin/api/auth/login", {"username": "admin", "password": password})
    emit("meta", step="login", status=status)
    if status != 200:
        return 1
    access_id = None
    try:
        status, body = call_admin("GET", "/admin/api/models/candidates")
        emit("meta", step="candidates", status=status)
        if status == 200:
            items = unwrap(body)
            emit("candidates", count=len(items), items=[{
                "upstream_id": item.get("upstream_id"),
                "display_name": item.get("display_name"),
                "kind": item.get("kind"),
                "provider": item.get("provider"),
                "known": item.get("known"),
            } for item in items])

        status, body = call_admin("GET", "/admin/api/models")
        catalog = unwrap(body) if status == 200 else []
        emit("catalog", rows=[{
            "public_id": item.get("public_id"),
            "upstream_id": item.get("upstream_id"),
            "provider": item.get("provider"),
            "enabled": item.get("enabled"),
            "kind": item.get("kind"),
        } for item in catalog if item.get("provider") == "opencodefree"])

        status, body = call_admin("POST", "/admin/api/access-keys", {"name": RUN_NAME})
        if status != 201:
            emit("meta", step="create_key", status=status)
            return 1
        created = json.loads(body)
        access_id, access_key = created["id"], created["key"]
        for item in catalog:
            if item.get("provider") == "opencodefree" and item.get("enabled") and item.get("kind") == "chat":
                probe(access_key, item["public_id"])
    finally:
        if access_id is not None:
            call_admin("DELETE", "/admin/api/access-keys/%s" % access_id)
        call_admin("POST", "/admin/api/auth/logout")
    return 0


raise SystemExit(main())
