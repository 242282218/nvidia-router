#!/usr/bin/env python3
"""Vibe-scenario model evaluation probe — runs ON the domestic test host.

Gates every enabled chat model with one cheap request, selects the currently
stable ones, then probes five dimensions that matter for vibe-coding clients:
reasoning strength, tool calling, context handling, stability, and strict
format following. Output is line-oriented JSON ("M|", "G|", "R|", "SUMMARY|")
for local aggregation. The admin password arrives on stdin and is never
written to disk, argv, or output.
"""

import json
import re
import sys
import time
import urllib.error
import urllib.request
from concurrent.futures import ThreadPoolExecutor

BASE = "http://127.0.0.1:3756"
RUN_TAG = "vibe-eval"
GATE_TIMEOUT = 35
REQ_TIMEOUT = 100
MATRIX_BUDGET = 330.0
MAX_MODELS = 4
MAX_CONCURRENCY = 4
RETRYABLE = {"upstream_unavailable", "upstream_throttled", "upstream_error"}

password = sys.stdin.readline().rstrip("\r\n")
admin_opener = urllib.request.build_opener(urllib.request.HTTPCookieProcessor())
v1_opener = urllib.request.build_opener()
access_key = {"id": None, "key": None}


def log(kind, payload):
    print("%s|%s" % (kind, json.dumps(payload, sort_keys=True, separators=(",", ":"))), flush=True)


def call_admin(method, path, payload=None, timeout=30):
    data = None if payload is None else json.dumps(payload, separators=(",", ":")).encode()
    headers = {"Origin": BASE}
    if data is not None:
        headers["Content-Type"] = "application/json"
    request = urllib.request.Request(BASE + path, data=data, headers=headers, method=method)
    try:
        with admin_opener.open(request, timeout=timeout) as response:
            return response.status, response.read()
    except urllib.error.HTTPError as error:
        return error.code, error.read()


def unwrap(body):
    value = json.loads(body)
    if isinstance(value, dict) and "data" in value:
        return value["data"]
    return value


def parse_error_body(body):
    try:
        value = json.loads(body)
        error = value.get("error", value) if isinstance(value, dict) else {}
        if isinstance(error, dict):
            return str(error.get("code") or error.get("type") or "error"), str(error.get("message") or "")[:300]
        return "error", str(error)[:300]
    except Exception:
        return "unparseable_error", body.decode("utf-8", "replace")[:200]


def text_len(value):
    if value is None:
        return 0
    if isinstance(value, str):
        return len(value)
    if isinstance(value, list):
        return sum(len(item.get("text", "")) if isinstance(item, dict) else len(str(item)) for item in value)
    if isinstance(value, dict):
        return len(json.dumps(value))
    return len(str(value))


def is_json_args(value):
    if not isinstance(value, str) or not value.strip():
        return False
    try:
        json.loads(value)
        return True
    except Exception:
        return False


def user(content):
    return [{"role": "user", "content": content}]


def chat(public_id, payload, timeout=REQ_TIMEOUT):
    body = dict(payload)
    body["model"] = public_id
    data = json.dumps(body, separators=(",", ":")).encode()
    request = urllib.request.Request(
        BASE + "/v1/chat/completions",
        data=data,
        headers={"Content-Type": "application/json", "Authorization": "Bearer " + access_key["key"]},
        method="POST",
    )
    record = {"ttft": None, "total": None, "status": None, "ok": False, "done": False}
    started = time.monotonic()
    try:
        with v1_opener.open(request, timeout=timeout) as response:
            record["status"] = response.status
            if body.get("stream"):
                content_chars = reasoning_chars = 0
                slots = {}
                max_gap = 0.0
                last_event = first_event = None
                finish_reason = None
                for raw_line in response:
                    line = raw_line.decode("utf-8", "replace").strip()
                    if not line.startswith("data:"):
                        continue
                    now = time.monotonic()
                    if first_event is None:
                        first_event = now
                        record["ttft"] = round(now - started, 3)
                    else:
                        max_gap = max(max_gap, now - last_event)
                    last_event = now
                    payload_text = line[5:].strip()
                    if payload_text == "[DONE]":
                        record["done"] = True
                        break
                    try:
                        event = json.loads(payload_text)
                    except Exception:
                        continue
                    choice = (event.get("choices") or [{}])[0]
                    delta = choice.get("delta") or {}
                    content_chars += text_len(delta.get("content"))
                    reasoning_chars += text_len(delta.get("reasoning_content"))
                    for call in delta.get("tool_calls") or []:
                        slot = slots.setdefault(call.get("index", 0), {"id": "", "name": "", "args": ""})
                        if call.get("id"):
                            slot["id"] = call["id"]
                        function = call.get("function") or {}
                        if function.get("name"):
                            slot["name"] += function["name"]
                        if function.get("arguments"):
                            slot["args"] += function["arguments"]
                    if choice.get("finish_reason"):
                        finish_reason = choice["finish_reason"]
                record["total"] = round(time.monotonic() - started, 3)
                record["content_chars"] = content_chars
                record["reasoning_chars"] = reasoning_chars
                record["max_gap"] = round(max_gap, 3)
                record["finish_reason"] = finish_reason
                record["tool_calls_raw"] = list(slots.values())
                record["tool_calls"] = [
                    {"id": bool(slot["id"]), "name": slot["name"], "args_valid": is_json_args(slot["args"])}
                    for slot in slots.values()
                ]
                record["ok"] = record["status"] == 200 and record["done"]
            else:
                raw = response.read()
                record["total"] = round(time.monotonic() - started, 3)
                value = json.loads(raw)
                choice = (value.get("choices") or [{}])[0]
                message = choice.get("message") or {}
                raw_calls = message.get("tool_calls") or []
                record["content_chars"] = text_len(message.get("content"))
                record["reasoning_chars"] = text_len(message.get("reasoning_content"))
                record["content_text"] = str(message.get("content") or "")[:400]
                record["finish_reason"] = choice.get("finish_reason")
                record["tool_calls_raw"] = [
                    {
                        "id": str(call.get("id") or ""),
                        "name": str((call.get("function") or {}).get("name") or ""),
                        "args": str((call.get("function") or {}).get("arguments") or ""),
                    }
                    for call in raw_calls
                ]
                record["tool_calls"] = [
                    {"id": bool(raw["id"]), "name": raw["name"], "args_valid": is_json_args(raw["args"])}
                    for raw in record["tool_calls_raw"]
                ]
                usage = value.get("usage") or {}
                record["completion_tokens"] = usage.get("completion_tokens")
                record["ok"] = record["status"] == 200
    except urllib.error.HTTPError as error:
        record["status"] = error.code
        record["total"] = round(time.monotonic() - started, 3)
        record["error_code"], record["error_message"] = parse_error_body(error.read())
    except Exception as error:
        record["status"] = 0
        record["total"] = round(time.monotonic() - started, 3)
        record["error_code"] = "transport"
        record["error_message"] = type(error).__name__
    return record


WEATHER_TOOL = {
    "type": "function",
    "function": {
        "name": "get_weather",
        "description": "Get current weather for a city.",
        "parameters": {"type": "object", "properties": {"city": {"type": "string"}}, "required": ["city"]},
    },
}
TIME_TOOL = {
    "type": "function",
    "function": {
        "name": "get_time",
        "description": "Get current local time in a timezone.",
        "parameters": {"type": "object", "properties": {"timezone": {"type": "string"}}, "required": ["timezone"]},
    },
}
TOOL_RESULTS = {
    "get_weather": {"temperature_c": 21, "condition": "sunny"},
    "get_time": {"iso": "2026-08-22T12:00:00+09:00", "zone": "Asia/Tokyo"},
}


def needle_payload():
    filler = " ".join("Background note %d: routine operational text without any codes." % index for index in range(520))
    document = filler[:12000] + " IMPORTANT: the secret access code is ZEBRA-42-VECTOR. " + filler[12000:24000]
    return {
        "stream": False,
        "max_tokens": 64,
        "messages": user(document + "\n\nReply with only the secret access code mentioned above."),
    }


def matrix_plan(meta):
    reasoning = bool(meta.get("supports_reasoning"))
    tools = bool(meta.get("supports_tools"))
    plan = []
    if reasoning:
        plan.append(("reasoning_none", {"stream": True, "max_tokens": 64, "reasoning_effort": "none",
                                        "messages": user("What is 2+2? Answer with the number only.")}))
        plan.append(("reasoning_low", {"stream": True, "max_tokens": 512, "reasoning_effort": "low",
                                       "messages": user("A train travels 60 km in 45 minutes. What is its speed in km/h? Think briefly.")}))
        plan.append(("reasoning_high", {"stream": True, "max_tokens": 1024, "reasoning_effort": "high",
                                        "messages": user("A bat and a ball cost 1.10 in total. The bat costs 1.00 more than the ball. How much is the ball? Reason carefully step by step.")}))
    else:
        plan.append(("reasoning_off_passthrough", {"stream": True, "max_tokens": 32, "reasoning_effort": "none",
                                                   "messages": user("What is 2+2? Answer with the number only.")}))
    plan.append(("tools_parallel", {
        "stream": False, "max_tokens": 512,
        "tools": [WEATHER_TOOL, TIME_TOOL], "tool_choice": "auto",
        "messages": user("What is the weather in Beijing and the current time in Tokyo? You must call both tools before answering."),
    }))
    plan.append(("json_mode", {
        "stream": False, "max_tokens": 256,
        "response_format": {"type": "json_object"},
        "messages": user('Return a JSON object exactly like {"ok": true, "items": 3, "label": "demo"}. No other text.'),
    }))
    plan.append(("context_needle", needle_payload()))
    for index in (1, 2, 3):
        entry = {"stream": True, "max_tokens": 32, "messages": user("Reply with exactly the word OK.")}
        if reasoning:
            entry["reasoning_effort"] = "none"
        plan.append(("stability_%d" % index, entry))
    plan.append(("long_output", {
        "stream": False, "max_tokens": 2048,
        "messages": user("Write a thorough step-by-step explanation of the quicksort algorithm: pivot choice, partitioning, recursion, and complexity."),
    }))
    if tools:
        plan.append(("tools_stream", {
            "stream": True, "max_tokens": 512,
            "tools": [WEATHER_TOOL, TIME_TOOL], "tool_choice": "auto",
            "messages": user("What is the weather in Beijing and the current time in Tokyo? You must call both tools before answering."),
        }))
    return plan


def run_matrix(meta, deadline):
    public_id = meta["public_id"]

    def emit(case, record):
        entry = {key: value for key, value in record.items() if key != "tool_calls_raw"}
        entry.update({"model": public_id, "provider": meta.get("provider"), "case": case})
        log("R", entry)

    def run(case, payload):
        record = chat(public_id, payload)
        if not record["ok"] and record.get("error_code") in RETRYABLE and deadline - time.monotonic() > 90:
            time.sleep(15)
            retry = chat(public_id, payload)
            retry["retried"] = True
            record = retry
        emit(case, record)
        return record

    for case, payload in matrix_plan(meta):
        if deadline - time.monotonic() < 20:
            emit(case, {"skipped": "budget"})
            continue
        record = run(case, payload)
        if case == "tools_parallel" and record.get("tool_calls_raw"):
            followup = {
                "stream": False,
                "max_tokens": 256,
                "messages": payload["messages"] + [
                    {"role": "assistant", "content": "", "tool_calls": [
                        {"id": call["id"] or "call_%d" % index, "type": "function",
                         "function": {"name": call["name"], "arguments": call["args"]}}
                        for index, call in enumerate(record["tool_calls_raw"])]},
                ] + [
                    {"role": "tool", "tool_call_id": call["id"] or "call_%d" % index,
                     "content": json.dumps(TOOL_RESULTS.get(call["name"], {"value": "n/a"}))}
                    for index, call in enumerate(record["tool_calls_raw"])
                ],
            }
            if deadline - time.monotonic() >= 20:
                run("tools_followup", followup)


def main():
    status, _ = call_admin("POST", "/admin/api/auth/login", {"username": "admin", "password": password})
    log("L", {"step": "login", "status": status})
    if status != 200:
        raise SystemExit(2)
    try:
        status, body = call_admin("GET", "/admin/api/models")
        models = [item for item in unwrap(body) if item.get("enabled")]
        status, metrics = call_admin("GET", "/metrics", timeout=15)
        pool_healthy = None
        if status == 200:
            match = re.search(r"^nvidia_router_proxy_pool_healthy\s+(\d+)", metrics.decode("utf-8", "replace"), re.M)
            if match:
                pool_healthy = int(match.group(1))
        log("L", {"step": "catalog", "count": len(models), "pool_healthy": pool_healthy})
        status, body = call_admin("POST", "/admin/api/access-keys", {"name": RUN_TAG})
        created = unwrap(body)
        access_key["id"] = created.get("id")
        access_key["key"] = created.get("key")
        if status != 201 or not access_key["key"]:
            log("L", {"step": "access_key", "status": status})
            raise SystemExit(3)
        request = urllib.request.Request(BASE + "/v1/models", headers={"Authorization": "Bearer " + access_key["key"]})
        exposed = {}
        try:
            with v1_opener.open(request, timeout=30) as response:
                for entry in json.loads(response.read()).get("data", []):
                    exposed[str(entry.get("id"))] = entry.get("context_length")
        except Exception as error:
            log("L", {"step": "v1_models", "error": type(error).__name__})
        for meta in models:
            meta["public_id"] = meta.get("public_id") or meta.get("model_id") or meta.get("id")
            log("M", {
                "public_id": meta.get("public_id"),
                "provider": meta.get("provider"),
                "supports_reasoning": meta.get("supports_reasoning"),
                "supports_tools": meta.get("supports_tools"),
                "wire": meta.get("reasoning_wire_format"),
                "context_length_db": meta.get("context_length"),
                "context_length_api": exposed.get(meta.get("public_id")),
            })

        def gate(meta):
            record = chat(meta["public_id"], {"stream": False, "max_tokens": 16, "messages": user("Reply with exactly OK.")}, timeout=GATE_TIMEOUT)
            record.update({"model": meta["public_id"], "provider": meta.get("provider")})
            return record

        with ThreadPoolExecutor(max_workers=MAX_CONCURRENCY) as pool:
            gate_results = list(pool.map(gate, models))
        for record in gate_results:
            log("G", record)
        passing = sorted([record for record in gate_results if record["ok"]], key=lambda record: record["total"])
        nvidia = [record for record in passing if record["provider"] == "nvidia"]
        others = [record for record in passing if record["provider"] != "nvidia"]
        chosen = [record["model"] for record in (nvidia[:2] + others)[:MAX_MODELS]]
        chosen_metas = [meta for meta in models if meta["public_id"] in chosen]
        log("L", {"step": "selected", "models": chosen})

        deadline = time.monotonic() + MATRIX_BUDGET
        started = time.monotonic()
        with ThreadPoolExecutor(max_workers=min(MAX_CONCURRENCY, len(chosen_metas) or 1)) as pool:
            list(pool.map(lambda meta: run_matrix(meta, deadline), chosen_metas))
        log("SUMMARY", {"matrix_seconds": round(time.monotonic() - started, 1), "models": chosen})
    finally:
        if access_key["id"] is not None:
            call_admin("DELETE", "/admin/api/access-keys/%s" % access_key["id"])
        call_admin("POST", "/admin/api/auth/logout")
        log("L", {"step": "cleanup", "done": True})


main()
