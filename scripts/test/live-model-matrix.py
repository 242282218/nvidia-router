#!/usr/bin/env python3
"""Run low-volume model compatibility probes on the domestic test host.

The script keeps the administrator password and temporary access key in
memory. The password is read from NVIDIA_ROUTER_ADMIN_PASSWORD and is sent to
the remote probe over SSH stdin; neither value is printed or written to disk.
"""

from __future__ import annotations

import argparse
import base64
import inspect
import json
import os
import shlex
import sys
from pathlib import Path

import paramiko


def payload_for(model, metadata, case):
    prompt = "Reply with exactly OK."
    payload = {
        "model": model,
        "messages": [{"role": "user", "content": prompt}],
        "max_tokens": 128,
        "stream": False,
    }
    if case == "baseline":
        payload["max_tokens"] = 16
    elif case.startswith("reasoning_low"):
        if metadata.get("supports_reasoning"):
            payload["reasoning_effort"] = "low"
    elif case == "reasoning_medium":
        if metadata.get("supports_reasoning"):
            payload["reasoning_effort"] = "medium"
        payload["max_tokens"] = 512
    elif case == "reasoning_high":
        if metadata.get("supports_reasoning"):
            payload["reasoning_effort"] = "high"
        payload["max_tokens"] = 1024
    elif case == "reasoning_none":
        if metadata.get("supports_reasoning"):
            payload["reasoning_effort"] = "none"
        payload["max_tokens"] = 32
    elif case == "thinking_enabled":
        payload["thinking"] = {"type": "enabled", "budget_tokens": 1024}
    elif case == "thinking_disabled":
        payload["thinking"] = {"type": "disabled"}
    elif case == "stream_low":
        if metadata.get("supports_reasoning"):
            payload["reasoning_effort"] = "low"
        payload["stream"] = True
    elif case == "stream":
        payload["stream"] = True
    elif case == "tools":
        payload["tools"] = [{
            "type": "function",
            "function": {
                "name": "lookup",
                "description": "Look up a value.",
                "parameters": {"type": "object", "properties": {"value": {"type": "string"}}, "required": ["value"]},
            },
        }]
        payload["tool_choice"] = {"type": "function", "function": {"name": "lookup"}}
        payload["messages"] = [{"role": "user", "content": "Call the lookup tool with value demo."}]
    elif case == "long_input":
        payload["max_tokens"] = 256
        payload["messages"] = [{"role": "user", "content": "Summarize this marker exactly once: " + ("long-input-marker " * 512)}]
    elif case in {"output_64", "output_512", "output_2048"}:
        if metadata.get("supports_reasoning"):
            payload["reasoning_effort"] = "none"
        payload["max_tokens"] = int(case.split("_", 1)[1])
        payload["messages"] = [{"role": "user", "content": "Write 100 short numbered lines from 1 to 100. Put one line per number and no preamble."}]
    elif case.startswith("repeat_"):
        if metadata.get("supports_reasoning"):
            payload["reasoning_effort"] = "none"
        payload["max_tokens"] = 64
        payload["messages"] = [{"role": "user", "content": "Reply with exactly the word OK."}]
    return payload


def cases_for(profile, metadata):
    reasoning = bool(metadata.get("supports_reasoning"))
    tools = bool(metadata.get("supports_tools"))
    if profile == "baseline":
        return ["baseline"]
    if profile == "reasoning":
        cases = ["reasoning_none"]
        if reasoning:
            cases.insert(0, "reasoning_low")
        return cases
    if profile == "strength":
        return ["reasoning_low", "reasoning_medium", "reasoning_high"] if reasoning else []
    if profile == "low_repeat":
        return ["reasoning_low_%d" % index for index in range(1, 6)] if reasoning else []
    if profile == "thinking":
        return ["thinking_enabled", "thinking_disabled"] if reasoning else []
    if profile == "stream":
        return ["stream_low"] if reasoning else ["stream"]
    if profile == "tools":
        return ["tools"] if tools else []
    if profile == "long":
        return ["long_input"]
    if profile == "output":
        return ["output_64", "output_512", "output_2048"]
    if profile == "repeat":
        return ["repeat_%d" % index for index in range(1, 6)]
    return []


REMOTE_PROGRAM = r'''
import json
import socket
import sys
import time
import urllib.error
import urllib.request

BASE = "http://127.0.0.1:3756"
PROFILE = __PROFILE__
MODEL_FILTER = set(__MODELS__)
DELAY = __DELAY__
TIMEOUT = __TIMEOUT__
CATALOG_ONLY = __CATALOG_ONLY__
RUN_NAME = "loop123-matrix-" + PROFILE
password = sys.stdin.readline().rstrip("\r\n")
jar = urllib.request.HTTPCookieProcessor()
opener = urllib.request.build_opener(jar)


def call_admin(method, path, payload=None):
    data = None if payload is None else json.dumps(payload, separators=(",", ":")).encode()
    headers = {"Origin": BASE}
    if data is not None:
        headers["Content-Type"] = "application/json"
    request = urllib.request.Request(BASE + path, data=data, headers=headers, method=method)
    try:
        with opener.open(request, timeout=30) as response:
            return response.status, response.read()
    except urllib.error.HTTPError as error:
        return error.code, error.read()


def unwrap(body):
    value = json.loads(body)
    if isinstance(value, dict) and "data" in value:
        return value["data"]
    return value


def error_code(body):
    try:
        value = json.loads(body)
        error = value.get("error", value) if isinstance(value, dict) else {}
        if isinstance(error, dict):
            return str(error.get("code") or error.get("type") or "error")
    except Exception:
        pass
    return "unparseable_error"


def error_message(body):
    try:
        value = json.loads(body)
        error = value.get("error", value) if isinstance(value, dict) else {}
        message = error.get("message") if isinstance(error, dict) else None
        if message:
            return str(message)[:2048]
    except Exception:
        pass
    return ""


def text_length(value):
    if isinstance(value, str):
        return len(value)
    if isinstance(value, list):
        return sum(text_length(item.get("text", "") if isinstance(item, dict) else item) for item in value)
    return 0


def json_result(body):
    try:
        value = json.loads(body)
        choices = value.get("choices", []) if isinstance(value, dict) else []
        first = choices[0] if choices else {}
        message = first.get("message", {}) if isinstance(first, dict) else {}
        return {
            "content_chars": text_length(message.get("content", "")),
            "reasoning_chars": text_length(message.get("reasoning_content", message.get("reasoning", ""))),
            "tool_calls": len(message.get("tool_calls", [])) if isinstance(message.get("tool_calls", []), list) else 0,
            "finish_reason": first.get("finish_reason") if isinstance(first, dict) else None,
            "protocol_ok": bool(choices),
        }
    except Exception:
        return {"protocol_ok": False}


def stream_result(response, started):
    first_byte_ms = None
    content_chars = 0
    reasoning_chars = 0
    tool_calls = 0
    finish_reason = None
    done = False
    malformed = 0
    events = 0
    while True:
        line = response.readline()
        if not line:
            break
        if first_byte_ms is None:
            first_byte_ms = int((time.monotonic() - started) * 1000)
        data = line.strip()
        if not data.startswith(b"data:"):
            continue
        data = data[5:].strip()
        if data == b"[DONE]":
            done = True
            continue
        try:
            value = json.loads(data)
            events += 1
            choices = value.get("choices", []) if isinstance(value, dict) else []
            first = choices[0] if choices else {}
            delta = first.get("delta", {}) if isinstance(first, dict) else {}
            content_chars += text_length(delta.get("content", ""))
            reasoning_chars += text_length(delta.get("reasoning_content", delta.get("reasoning", "")))
            calls = delta.get("tool_calls", [])
            if isinstance(calls, list):
                tool_calls += len(calls)
            if isinstance(first, dict) and first.get("finish_reason") is not None:
                finish_reason = first.get("finish_reason")
        except Exception:
            malformed += 1
    return {
        "ttft_ms": first_byte_ms,
        "content_chars": content_chars,
        "reasoning_chars": reasoning_chars,
        "tool_calls": tool_calls,
        "finish_reason": finish_reason,
        "stream_done": done,
        "sse_events": events,
        "sse_malformed": malformed,
        "protocol_ok": done and events > 0,
    }


def run_case(model, case, payload):
    started = time.monotonic()
    result = {"case": case, "model": model}
    request = urllib.request.Request(
        BASE + "/v1/chat/completions",
        data=json.dumps(payload, separators=(",", ":")).encode(),
        headers={
            "Authorization": "Bearer " + access_key,
            "Content-Type": "application/json",
            "Accept": "text/event-stream" if payload.get("stream") else "application/json",
            "Connection": "close",
        },
        method="POST",
    )
    try:
        with urllib.request.urlopen(request, timeout=TIMEOUT) as response:
            result["status"] = response.status
            if payload.get("stream"):
                result.update(stream_result(response, started))
            else:
                result.update(json_result(response.read(8 * 1024 * 1024)))
    except urllib.error.HTTPError as error:
        body = error.read(256 * 1024)
        result["status"] = error.code
        result["error_code"] = error_code(body)
        message = error_message(body)
        if message:
            result["error_message"] = message.replace(access_key, "[redacted]")
        result["protocol_ok"] = False
    except (socket.timeout, TimeoutError):
        result["status"] = 0
        result["error_code"] = "client_timeout"
        result["protocol_ok"] = False
    except urllib.error.URLError as error:
        result["status"] = 0
        result["error_code"] = "url_error"
        result["error_detail"] = type(error.reason).__name__
        result["protocol_ok"] = False
    except Exception as error:
        result["status"] = 0
        result["error_code"] = type(error).__name__
        result["protocol_ok"] = False
    result["elapsed_ms"] = int((time.monotonic() - started) * 1000)
    print(json.dumps(result, sort_keys=True), flush=True)


__PAYLOAD_HELPERS__


access_id = None
access_key = None
try:
    status, _ = call_admin("POST", "/admin/api/auth/login", {"username": "admin", "password": password})
    print("meta|login_status=%d" % status, flush=True)
    if status != 200:
        raise SystemExit("admin_login_failed")

    status, body = call_admin("GET", "/admin/api/access-keys")
    if status == 200:
        for item in unwrap(body):
            if item.get("name") == RUN_NAME:
                call_admin("DELETE", "/admin/api/access-keys/%s" % item["id"])

    status, body = call_admin("POST", "/admin/api/access-keys", {"name": RUN_NAME})
    print("meta|create_access_status=%d" % status, flush=True)
    if status != 201:
        raise SystemExit("access_key_create_failed")
    created = json.loads(body)
    access_id = created["id"]
    access_key = created["key"]

    status, body = call_admin("GET", "/admin/api/models")
    all_models = unwrap(body) if status == 200 else []
    provider_counts = {}
    for item in all_models:
        provider = str(item.get("provider") or "unknown")
        provider_counts[provider] = provider_counts.get(provider, 0) + 1
    print("meta|catalog_status=%d|catalog_count=%d|provider_counts=%s" % (
        status,
        len(all_models),
        json.dumps(provider_counts, sort_keys=True, separators=(",", ":")),
    ), flush=True)
    for item in all_models:
        if str(item.get("provider") or "").lower() != "opencodefree":
            continue
        print("meta|catalog_model=" + json.dumps({
            "public_id": item.get("public_id"),
            "upstream_id": item.get("upstream_id"),
            "provider": item.get("provider"),
            "kind": item.get("kind"),
            "enabled": item.get("enabled"),
            "supports_vision": item.get("supports_vision"),
            "supports_tools": item.get("supports_tools"),
            "supports_reasoning": item.get("supports_reasoning"),
            "reasoning_wire_format": item.get("reasoning_wire_format"),
            "reasoning_levels": item.get("reasoning_levels"),
        }, sort_keys=True, separators=(",", ":")), flush=True)
    if CATALOG_ONLY:
        raise SystemExit(0)
    selected = [item for item in all_models if item.get("enabled") and item.get("kind") == "chat"]
    if MODEL_FILTER:
        selected = [item for item in selected if item.get("public_id") in MODEL_FILTER]
    print("meta|selected_models=%d|profile=%s" % (len(selected), PROFILE), flush=True)
    for item in selected:
        model = item["public_id"]
        cases = cases_for(PROFILE, item)
        print("meta|model=%s|cases=%s" % (model, ",".join(cases)), flush=True)
        for case in cases:
            run_case(model, case, payload_for(model, item, case))
            time.sleep(DELAY)
finally:
    if access_id is not None:
        status, _ = call_admin("DELETE", "/admin/api/access-keys/%s" % access_id)
        print("meta|cleanup_access_status=%d" % status, flush=True)
    call_admin("POST", "/admin/api/auth/logout")
'''


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--profile", choices=("baseline", "reasoning", "strength", "low_repeat", "thinking", "stream", "tools", "long", "output", "repeat"))
    parser.add_argument("--models", default="", help="comma-separated public model IDs; default is all enabled chat models")
    parser.add_argument("--delay", type=float, default=3.0)
    parser.add_argument("--timeout", type=float, default=180.0)
    parser.add_argument("--catalog-only", action="store_true", help="print the current model catalog and exit")
    parser.add_argument("--ssh-config", type=Path, default=None)
    parser.add_argument("--self-test", action="store_true", help="run local payload/template checks without SSH or secrets")
    args = parser.parse_args()
    if not args.self_test and args.profile is None:
        parser.error("--profile is required unless --self-test is used")
    return args


def main() -> int:
    args = parse_args()
    if args.self_test:
        self_test()
        return 0
    password = os.environ.get("NVIDIA_ROUTER_ADMIN_PASSWORD")
    if not password:
        raise SystemExit("NVIDIA_ROUTER_ADMIN_PASSWORD is required")
    # The repository and the server-management directory are sibling folders
    # under the workspace root, rather than a child of this repository.
    config_path = args.ssh_config or Path(__file__).resolve().parents[3] / "服务器管理" / "hangzhou2-2" / "ssh_config_local"
    config = paramiko.SSHConfig()
    with config_path.open(encoding="utf-8") as handle:
        config.parse(handle)
    host = config.lookup("hangzhou2-2")
    program = build_remote_program(
        args.profile,
        [value for value in (item.strip() for item in args.models.split(",")) if value],
        args.delay,
        args.timeout,
        args.catalog_only,
    )
    encoded = base64.b64encode(program.encode()).decode()
    command = "python3 -c " + shlex.quote(
        "import base64;exec(compile(base64.b64decode(%r),'<live-model-matrix>','exec'))" % encoded
    )
    client = paramiko.SSHClient()
    client.set_missing_host_key_policy(paramiko.AutoAddPolicy())
    client.connect(
        host["hostname"],
        port=int(host.get("port", 22)),
        username=host.get("user", "root"),
        key_filename=host["identityfile"][0],
        timeout=20,
    )
    stdin, stdout, stderr = client.exec_command(command, timeout=max(120, args.timeout * 20))
    stdin.write(password + "\n")
    stdin.channel.shutdown_write()
    output = stdout.read().decode("utf-8", "replace")
    error = stderr.read().decode("utf-8", "replace")
    status = stdout.channel.recv_exit_status()
    client.close()
    sys.stdout.write(output)
    if error:
        sys.stderr.write(error)
    return status


def build_remote_program(profile, models, delay, timeout, catalog_only):
    program = REMOTE_PROGRAM.replace("__PROFILE__", repr(profile)).replace("__MODELS__", repr(models))
    program = program.replace("__DELAY__", repr(delay)).replace("__TIMEOUT__", repr(timeout))
    program = program.replace("__CATALOG_ONLY__", repr(catalog_only))
    helpers = inspect.getsource(payload_for) + "\n\n" + inspect.getsource(cases_for)
    program = program.replace("__PAYLOAD_HELPERS__", helpers)
    if "__PAYLOAD_HELPERS__" in program:
        raise ValueError("remote payload helper template was not expanded")
    return program


def self_test():
    unsupported = {"supports_reasoning": False}
    supported = {"supports_reasoning": True}
    if "reasoning_effort" in payload_for("model", unsupported, "output_64"):
        raise AssertionError("output payload must omit reasoning_effort for unsupported models")
    if payload_for("model", supported, "output_64").get("reasoning_effort") != "none":
        raise AssertionError("output payload must preserve reasoning_effort=none for reasoning models")
    compile(
        build_remote_program("output", ["model"], 0, 30, False),
        "<live-model-matrix-self-test>",
        "exec",
    )
    print("self-test|payload_contract=PASS|remote_program=PASS")


if __name__ == "__main__":
    raise SystemExit(main())
