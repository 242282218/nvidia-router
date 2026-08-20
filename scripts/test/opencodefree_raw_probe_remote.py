"""Raw OpenCodeFree gateway probe — runs ON the domestic test host.

Reads the gateway base URL and auth key out of the running container's
environment and calls the gateway directly, so the non-standard status can be
seen with its body instead of the router's mapped 502. Neither the key nor the
full URL is ever printed: only the host is shown, and the key is redacted out of
any body before it is emitted.
"""

import json
import re
import subprocess
import urllib.error
import urllib.parse
import urllib.request

CONTAINER = "nvidia-router-app-1"


def emit(kind, **fields):
    fields["kind"] = kind
    print("R|" + json.dumps(fields, sort_keys=True, separators=(",", ":")), flush=True)


def container_name():
    out = subprocess.run(
        ["docker", "ps", "--format", "{{.Names}}"], capture_output=True, text=True, timeout=60
    ).stdout.split()
    for name in out:
        if "app" in name and "nvidia" in name:
            return name
    return CONTAINER


def read_env(name, container):
    result = subprocess.run(
        ["docker", "exec", container, "printenv", name],
        capture_output=True, text=True, timeout=60,
    )
    return result.stdout.strip()


def redact(text, secrets):
    for secret in secrets:
        if secret:
            text = text.replace(secret, "[redacted]")
    return text


def main():
    container = container_name()
    base = read_env("NVIDIA_ROUTER_OPENCODEFREE_BASE_URL", container)
    key = read_env("NVIDIA_ROUTER_OPENCODEFREE_AUTH_KEY", container)
    if not base:
        emit("meta", step="config", configured=False)
        return 1
    parsed = urllib.parse.urlparse(base)
    emit("meta", step="config", host=parsed.hostname, scheme=parsed.scheme,
         port=parsed.port, has_key=bool(key))

    secrets = [key, base]
    for path, payload in (("/v1/models", None),
                          ("/v1/chat/completions", {
                              "model": "deepseek-v4-flash-free",
                              "messages": [{"role": "user", "content": "Reply with exactly OK."}],
                              "max_tokens": 16,
                          })):
        url = base.rstrip("/") + path
        data = None if payload is None else json.dumps(payload).encode()
        headers = {"Accept": "application/json"}
        if key:
            headers["Authorization"] = "Bearer " + key
        if data is not None:
            headers["Content-Type"] = "application/json"
        request = urllib.request.Request(url, data=data, headers=headers,
                                         method="GET" if data is None else "POST")
        record = {"path": path}
        try:
            with urllib.request.urlopen(request, timeout=120) as response:
                record["status"] = response.status
                body = response.read(1 << 20).decode("utf-8", "replace")
                record["body"] = redact(body, secrets)[:1200]
        except urllib.error.HTTPError as error:
            record["status"] = error.code
            record["reason"] = str(error.reason)[:120]
            body = error.read(1 << 20).decode("utf-8", "replace")
            record["body"] = redact(body, secrets)[:1200]
            record["headers"] = {k: v for k, v in error.headers.items()
                                 if k.lower() in ("content-type", "retry-after", "x-request-id", "server", "date")}
        except Exception as error:  # noqa: BLE001
            record["status"] = 0
            record["error"] = type(error).__name__
            record["detail"] = redact(str(error), secrets)[:200]
        emit("gateway", **record)
    return 0


raise SystemExit(main())
