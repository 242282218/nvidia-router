#!/usr/bin/env python3
"""Run a redacted vibe-scenario model matrix on the domestic test host."""

import argparse
import json
import re
import socket
import sys
import time
import urllib.error
import urllib.request
from collections import Counter
from concurrent.futures import ThreadPoolExecutor


BASE = "http://127.0.0.1:3756"
RUN_TAG = "vibe-eval"
GATE_TIMEOUT = 35
REQ_TIMEOUT = 100
DEFAULT_REPEAT = 20
DEFAULT_CONTEXT_TARGETS = [8192, 32768]
DEFAULT_MAX_MODELS = 4
DEFAULT_MATRIX_BUDGET_SECONDS = 600.0
MAX_CONCURRENCY = 4
RETRYABLE = {"upstream_unavailable", "upstream_throttled", "upstream_error"}
NEEDLE = "ZEBRA-42-VECTOR"

_DROP_FIELDS = {
    "args",
    "arguments",
    "authorization",
    "content",
    "content_text",
    "message",
    "messages",
    "password",
    "prompt",
    "raw_tool_calls",
    "tool_calls_raw",
    "tool_args",
    "_content_text",
    "_tool_calls_raw",
}
_TOOL_FIELDS = {"tool_calls", "tool_calls_raw", "_tool_calls_raw", "raw_tool_calls"}
_SAFE_STRING_FIELDS = {
    "case",
    "error_category",
    "error_code",
    "finish_reason",
    "model",
    "models",
    "provider",
    "public_id",
    "reasoning_wire_format",
    "skipped",
    "step",
    "stream_error_code",
    "tools_status",
    "wire",
}

admin_opener = None
v1_opener = None
access_key = {"id": None, "key": None}
# remote_exec.py replaces this placeholder for a bounded single-model run.
REMOTE_MODEL_FILTER = "__REMOTE_MODEL_FILTER__"


def _safe_token(value, fallback="redacted"):
    value = str(value or "")
    if re.fullmatch(r"[A-Za-z0-9_.:/@+-]{1,120}", value):
        return value
    return fallback


def _is_dropped_field(key):
    normalized = str(key).lower()
    return normalized in _DROP_FIELDS or normalized.startswith("_")


def _sanitize_value(key, value):
    if isinstance(value, dict):
        return sanitize_record(value)
    if isinstance(value, list):
        return [_sanitize_value(key, item) for item in value]
    if isinstance(value, str):
        if key not in _SAFE_STRING_FIELDS:
            return None
        return _safe_token(value)
    if value is None or isinstance(value, (bool, int, float)):
        return value
    return None


def _tool_call_metrics(calls):
    calls = list(calls or [])
    if not calls:
        return {"tool_call_count": 0, "tool_names_present": None, "tool_args_valid": None}
    names_present = all(bool(call.get("name") or call.get("name_present")) for call in calls)
    args_valid = all(
        bool(call["args_valid"]) if "args_valid" in call else is_json_args(call.get("args"))
        for call in calls
    )
    return {
        "tool_call_count": len(calls),
        "tool_names_present": names_present,
        "tool_args_valid": args_valid,
    }


def sanitize_record(record):
    """Keep only safe scalar metrics; raw prompts, messages and tool args never pass through."""
    if not isinstance(record, dict):
        return {}
    safe = {}
    tool_calls = None
    for key, value in record.items():
        key = str(key)
        if key in _TOOL_FIELDS:
            tool_calls = value
            continue
        if _is_dropped_field(key):
            continue
        cleaned = _sanitize_value(key, value)
        if cleaned is not None:
            safe[key] = cleaned
    if tool_calls is not None:
        safe.update(_tool_call_metrics(tool_calls))
    return safe


def log(kind, payload):
    print(
        "%s|%s" % (kind, json.dumps(sanitize_record(payload), sort_keys=True, separators=(",", ":"))),
        flush=True,
    )


def _get_opener(kind):
    global admin_opener, v1_opener
    if kind == "admin":
        if admin_opener is None:
            admin_opener = urllib.request.build_opener(urllib.request.HTTPCookieProcessor())
        return admin_opener
    if v1_opener is None:
        v1_opener = urllib.request.build_opener()
    return v1_opener


def call_admin(method, path, payload=None, timeout=30, opener=None):
    data = None if payload is None else json.dumps(payload, separators=(",", ":")).encode()
    headers = {"Origin": BASE}
    if data is not None:
        headers["Content-Type"] = "application/json"
    request = urllib.request.Request(BASE + path, data=data, headers=headers, method=method)
    try:
        with (opener or _get_opener("admin")).open(request, timeout=timeout) as response:
            return response.status, response.read()
    except urllib.error.HTTPError as error:
        return error.code, error.read()
    except (urllib.error.URLError, OSError, TimeoutError):
        return 0, b""


def unwrap(body):
    value = json.loads(body)
    if isinstance(value, dict) and "data" in value:
        return value["data"]
    return value


def _safe_error_code(value, fallback="error"):
    value = str(value or "")
    if re.fullmatch(r"[A-Za-z0-9_.-]{1,80}", value):
        return value
    return fallback


def parse_error_body(body):
    """Return an error code and message length without returning upstream text."""
    if isinstance(body, bytes):
        text = body.decode("utf-8", "replace")
    else:
        text = str(body or "")
    try:
        value = json.loads(text)
        error = value.get("error", value) if isinstance(value, dict) else {}
        if isinstance(error, dict):
            code = _safe_error_code(error.get("code") or error.get("type"))
            message = str(error.get("message") or "")
        else:
            code = "error"
            message = str(error)
    except Exception:
        code = "unparseable_error"
        message = text
    return {
        "error_code": code,
        "error_message_length": min(len(message), 1000),
        "error_message_present": bool(message),
    }


def classify_error(status, error_code=None):
    if status == 200:
        return "ok"
    if status == 501:
        return "unsupported"
    if status == 502:
        return "bad_gateway"
    if status == 503:
        return "service_unavailable"
    if status == 429:
        return "throttled"
    if status == 0:
        if error_code in {"timeout", "timed_out", "read_timeout"}:
            return "timeout"
        return "transport"
    if isinstance(status, int) and 400 <= status < 500:
        return "request_error"
    return "unknown"


def text_len(value):
    if value is None:
        return 0
    if isinstance(value, str):
        return len(value)
    if isinstance(value, list):
        return sum(text_len(item.get("text", "")) if isinstance(item, dict) else text_len(item) for item in value)
    if isinstance(value, dict):
        return sum(text_len(item) for item in value.values())
    return len(str(value))


def has_user_visible_output(content_chars, tool_call_count=0):
    return bool(content_chars or tool_call_count)


def _text_value(value):
    if value is None:
        return ""
    if isinstance(value, str):
        return value
    if isinstance(value, list):
        return "".join(_text_value(item.get("text", "")) if isinstance(item, dict) else _text_value(item) for item in value)
    if isinstance(value, dict):
        return "".join(_text_value(item) for item in value.values())
    return str(value)


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


def _event_error_code(event):
    error = event.get("error") if isinstance(event, dict) else None
    if not isinstance(error, dict):
        response = event.get("response") if isinstance(event, dict) else None
        error = response.get("error") if isinstance(response, dict) else None
    if not isinstance(error, dict):
        return None
    return _safe_error_code(error.get("code") or error.get("type"), "stream_error")


def parse_sse_events(lines, started_at=None, clock=None, include_raw=False):
    """Parse Chat SSE into safe metrics; raw tool calls are opt-in for in-process follow-up only."""
    clock = clock or time.monotonic
    stats = {
        "ttft": None,
        "content_chars": 0,
        "reasoning_chars": 0,
        "max_gap": 0.0,
        "event_count": 0,
        "semantic_events": 0,
        "malformed_events": 0,
        "done": False,
        "stream_error_code": None,
        "finish_reason": None,
    }
    slots = {}
    content_parts = []
    first_event_at = None
    last_event_at = None
    for raw_line in lines:
        line = raw_line.decode("utf-8", "replace") if isinstance(raw_line, bytes) else str(raw_line)
        line = line.strip()
        if not line.startswith("data:"):
            continue
        payload_text = line[5:].strip()
        if payload_text == "[DONE]":
            stats["done"] = True
            break
        now = clock()
        if first_event_at is None:
            first_event_at = now
            if started_at is not None:
                stats["ttft"] = round(now - started_at, 3)
        elif last_event_at is not None:
            stats["max_gap"] = round(max(stats["max_gap"], now - last_event_at), 3)
        last_event_at = now
        try:
            event = json.loads(payload_text)
        except Exception:
            stats["malformed_events"] += 1
            stats["stream_error_code"] = stats["stream_error_code"] or "malformed_sse"
            continue
        stats["event_count"] += 1
        explicit_error = _event_error_code(event)
        if explicit_error:
            stats["stream_error_code"] = explicit_error
            continue
        choices = event.get("choices") or []
        if choices:
            stats["semantic_events"] += 1
        for choice in choices:
            delta = choice.get("delta") or {}
            content = _text_value(delta.get("content"))
            stats["content_chars"] += len(content)
            if content:
                content_parts.append(content)
            stats["reasoning_chars"] += text_len(delta.get("reasoning_content"))
            for call in delta.get("tool_calls") or []:
                index = call.get("index", len(slots))
                slot = slots.setdefault(index, {"id": "", "name": "", "args": ""})
                if call.get("id"):
                    slot["id"] = str(call["id"])
                function = call.get("function") or {}
                if function.get("name"):
                    slot["name"] += str(function["name"])
                if function.get("arguments"):
                    slot["args"] += str(function["arguments"])
            if choice.get("finish_reason"):
                stats["finish_reason"] = choice["finish_reason"]
    if not stats["done"] and stats["stream_error_code"] is None:
        stats["stream_error_code"] = "upstream_stream_truncated"
    elif stats["done"] and stats["semantic_events"] == 0 and stats["stream_error_code"] is None:
        stats["stream_error_code"] = "empty_stream"
    stats.update(_tool_call_metrics(slots.values()))
    if include_raw:
        stats["_content_text"] = "".join(content_parts)
        stats["_tool_calls_raw"] = list(slots.values())
    return stats


def stream_record_ok(status, stats):
    return bool(
        status == 200
        and stats.get("done")
        and stats.get("semantic_events", 0) > 0
        and stats.get("malformed_events", 0) == 0
        and not stats.get("stream_error_code")
        and has_user_visible_output(stats.get("content_chars", 0), stats.get("tool_call_count", 0))
    )


def stability_record_ok(record):
    return bool(
        record.get("ok")
        and _text_value(record.get("_content_text", "")).strip() == "OK"
    )


def gate_payload():
    return {
        "stream": False,
        "max_tokens": 512,
        "messages": user("Reply with exactly OK."),
    }


def chat(public_id, payload, timeout=REQ_TIMEOUT):
    body = dict(payload)
    body["model"] = public_id
    data = json.dumps(body, separators=(",", ":")).encode()
    request = urllib.request.Request(
        BASE + "/v1/chat/completions",
        data=data,
        headers={"Content-Type": "application/json", "Authorization": "Bearer " + str(access_key["key"] or "")},
        method="POST",
    )
    record = {"ttft": None, "total": None, "status": None, "ok": False, "done": False}
    started = time.monotonic()
    try:
        with _get_opener("v1").open(request, timeout=timeout) as response:
            record["status"] = response.status
            if body.get("stream"):
                stats = parse_sse_events(response, started_at=started, include_raw=True)
                record.update({key: value for key, value in stats.items() if not key.startswith("_")})
                record["_content_text"] = stats.get("_content_text", "")
                record["_tool_calls_raw"] = stats.get("_tool_calls_raw", [])
                record["total"] = round(time.monotonic() - started, 3)
                record["ok"] = stream_record_ok(record["status"], stats)
            else:
                raw = response.read()
                record["total"] = round(time.monotonic() - started, 3)
                value = json.loads(raw)
                choice = (value.get("choices") or [{}])[0]
                message = choice.get("message") or {}
                raw_calls = []
                for call in message.get("tool_calls") or []:
                    function = call.get("function") or {}
                    raw_calls.append(
                        {
                            "id": str(call.get("id") or ""),
                            "name": str(function.get("name") or ""),
                            "args": str(function.get("arguments") or ""),
                        }
                    )
                record["content_chars"] = text_len(message.get("content"))
                record["reasoning_chars"] = text_len(message.get("reasoning_content"))
                record["finish_reason"] = choice.get("finish_reason")
                record.update(_tool_call_metrics(raw_calls))
                record["_tool_calls_raw"] = raw_calls
                record["_content_text"] = _text_value(message.get("content"))
                record["empty_response"] = not has_user_visible_output(
                    record["content_chars"], record["tool_call_count"]
                )
                record["completion_tokens"] = (value.get("usage") or {}).get("completion_tokens")
                record["done"] = True
                record["ok"] = record["status"] == 200 and not record["empty_response"]
    except urllib.error.HTTPError as error:
        record["status"] = error.code
        record["total"] = round(time.monotonic() - started, 3)
        record.update(parse_error_body(error.read()))
        record["error_category"] = classify_error(error.code, record["error_code"])
    except (socket.timeout, TimeoutError):
        record["status"] = 0
        record["total"] = round(time.monotonic() - started, 3)
        record["error_code"] = "timeout"
        record["error_category"] = "timeout"
    except urllib.error.URLError as error:
        record["status"] = 0
        record["total"] = round(time.monotonic() - started, 3)
        reason = error.reason
        is_timeout = isinstance(reason, (socket.timeout, TimeoutError)) or "timed out" in str(reason).lower()
        record["error_code"] = "timeout" if is_timeout else "transport"
        record["error_category"] = classify_error(0, record["error_code"])
    except Exception:
        record["status"] = 0
        record["total"] = round(time.monotonic() - started, 3)
        record["error_code"] = "transport"
        record["error_category"] = "transport"
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


def needle_payload(context_target=32768):
    target = max(1, int(context_target))
    target_chars = max(12000, target * 4)
    filler_unit = "Background note: routine operational text without any codes. "
    filler = (filler_unit * ((target_chars // len(filler_unit)) + 2))[:target_chars]
    midpoint = len(filler) // 2
    document = filler[:midpoint] + " IMPORTANT: the secret access code is " + NEEDLE + ". " + filler[midpoint:]
    return {
        "stream": False,
        "max_tokens": 512,
        "messages": user(document + "\n\nReply with only the secret access code mentioned above."),
    }


def needle_hit(value):
    return NEEDLE in _text_value(value)


def _declared_context_length(meta):
    for key in ("context_length_api", "context_length", "context_length_db"):
        try:
            value = int(meta.get(key) or 0)
        except (TypeError, ValueError):
            value = 0
        if value > 0:
            return value
    return 0


def context_targets_for_model(meta, targets):
    declared = _declared_context_length(meta)
    if not declared:
        return []
    return [target for target in targets if int(target) <= declared]


def _tools_are_supported(meta):
    if "tools_status" in meta:
        return meta.get("tools_status") in {"supported", "inferred"}
    return bool(meta.get("supports_tools"))


def _reasoning_efforts(meta):
    if not meta.get("supports_reasoning"):
        return []
    declared = meta.get("reasoning_efforts")
    if declared is None:
        declared = meta.get("reasoning_levels")
    allowed = {"none", "low", "medium", "high"}
    if isinstance(declared, list):
        values = [str(value).lower() for value in declared if str(value).lower() in allowed]
        if meta.get("reasoning_zero_allowed") is False:
            values = [value for value in values if value != "none"]
        return list(dict.fromkeys(values))
    fallback = ["none", "low", "high"]
    if meta.get("reasoning_zero_allowed") is False:
        fallback.remove("none")
    return fallback


def parse_model_ids(value):
    values = [item.strip() for item in str(value).split(",") if item.strip()]
    if not values:
        raise argparse.ArgumentTypeError("models must contain at least one model id")
    return list(dict.fromkeys(values))


def select_models(catalog, requested=None):
    models = [item for item in catalog if isinstance(item, dict) and item.get("enabled")]
    if not requested:
        return models
    wanted = set(requested)
    return [item for item in models if item.get("public_id") in wanted]


def matrix_plan(meta, repeat=DEFAULT_REPEAT, context_targets=None):
    context_targets = DEFAULT_CONTEXT_TARGETS if context_targets is None else list(context_targets)
    plan = []
    reasoning_efforts = _reasoning_efforts(meta)
    if reasoning_efforts:
        prompts = {
            "none": (64, "What is 2+2? Answer with the number only."),
            "low": (512, "A train travels 60 km in 45 minutes. What is its speed in km/h? Think briefly."),
            "medium": (768, "Solve 12 * 17 mentally and answer with the number only."),
            "high": (1024, "A bat and a ball cost 1.10 in total. The bat costs 1.00 more than the ball. How much is the ball? Reason carefully step by step."),
        }
        for effort in reasoning_efforts:
            max_tokens, prompt = prompts[effort]
            plan.append(
                (
                    "reasoning_" + effort,
                    {"stream": True, "max_tokens": max_tokens, "reasoning_effort": effort, "messages": user(prompt)},
                )
            )
    else:
        plan.append(
            (
                "reasoning_off_passthrough",
                {"stream": True, "max_tokens": 32, "messages": user("What is 2+2? Answer with the number only.")},
            )
        )
    plan.append(
        (
            "tools_parallel",
            {
                "stream": False,
                "max_tokens": 512,
                "tools": [WEATHER_TOOL, TIME_TOOL],
                "tool_choice": "auto",
                "messages": user("What is the weather in Beijing and the current time in Tokyo? You must call both tools before answering."),
            },
        )
    )
    plan.append(
        (
            "json_mode",
            {
                "stream": False,
                "max_tokens": 256,
                "response_format": {"type": "json_object"},
                "messages": user('Return a JSON object exactly like {"ok": true, "items": 3, "label": "demo"}. No other text.'),
            },
        )
    )
    safe_targets = context_targets_for_model(meta, context_targets)
    if safe_targets:
        for target in safe_targets:
            plan.append(("context_needle_%d" % target, needle_payload(target)))
    elif context_targets:
        observed_target = min(int(target) for target in context_targets)
        plan.append(
            (
                "context_needle_observed_%d" % observed_target,
                needle_payload(observed_target),
            )
        )
    else:
        plan.append(("context_needle_skipped", {"skipped": "context_window_unverified"}))
    for index in range(1, max(0, int(repeat)) + 1):
        entry = {"stream": True, "max_tokens": 32, "messages": user("Reply with exactly the word OK.")}
        # A positive reasoning level with a 32-token window creates an empty
        # answer on models that cannot express "none". Leave the field out and
        # measure the model's default path instead of manufacturing starvation.
        if "none" in reasoning_efforts:
            entry["reasoning_effort"] = "none"
        plan.append(("stability_%d" % index, entry))
    plan.append(
        (
            "long_output",
            {
                "stream": False,
                "max_tokens": 2048,
                "messages": user("Write a thorough step-by-step explanation of the quicksort algorithm: pivot choice, partitioning, recursion, and complexity."),
            },
        )
    )
    if _tools_are_supported(meta):
        plan.append(
            (
                "tools_stream",
                {
                    "stream": True,
                    "max_tokens": 512,
                    "tools": [WEATHER_TOOL, TIME_TOOL],
                    "tool_choice": "auto",
                    "messages": user("What is the weather in Beijing and the current time in Tokyo? You must call both tools before answering."),
                },
            )
        )
    return plan


def _tool_result_reference_count(text, calls):
    text = _text_value(text)
    expected = 0
    for call in calls:
        result = TOOL_RESULTS.get(call.get("name"), {})
        if result and any(str(value) in text for value in result.values()):
            expected += 1
    return expected


def run_matrix(meta, deadline, repeat=DEFAULT_REPEAT, context_targets=None):
    public_id = meta["public_id"]
    results = []

    def emit(case, record):
        entry = sanitize_record(record)
        entry.update({"model": public_id, "provider": meta.get("provider"), "case": case})
        log("R", entry)

    def run(case, payload, annotate=None):
        if payload.get("skipped"):
            record = {"skipped": payload["skipped"], "ok": False}
            record["case"] = case
            results.append(record)
            emit(case, record)
            return record
        record = chat(public_id, payload)
        if not record["ok"] and record.get("error_code") in RETRYABLE and deadline - time.monotonic() > 90:
            time.sleep(15)
            retry = chat(public_id, payload)
            retry["retried"] = True
            record = retry
        if annotate:
            annotate(record)
        record["case"] = case
        results.append(record)
        emit(case, record)
        return record

    for case, payload in matrix_plan(meta, repeat=repeat, context_targets=context_targets):
        if deadline - time.monotonic() < 20:
            record = {"skipped": "budget", "ok": False}
            record["case"] = case
            results.append(record)
            emit(case, record)
            continue
        if case.startswith("context_needle_") and not payload.get("skipped"):
            run(case, payload, lambda record: record.update({"needle_hit": needle_hit(record.get("_content_text", ""))}))
            continue
        annotate = None
        if case.startswith("stability_"):
            def annotate_stability(value):
                value["exact_ok"] = stability_record_ok(value)
                value["ok"] = value["exact_ok"]

            annotate = annotate_stability
        record = run(case, payload, annotate)
        raw_calls = record.get("_tool_calls_raw") or []
        if case == "tools_parallel" and raw_calls:
            followup = {
                "stream": False,
                "max_tokens": 256,
                "messages": payload["messages"]
                + [
                    {
                        "role": "assistant",
                        "content": "",
                        "tool_calls": [
                            {
                                "id": call["id"] or "call_%d" % index,
                                "type": "function",
                                "function": {"name": call["name"], "arguments": call["args"]},
                            }
                            for index, call in enumerate(raw_calls)
                        ],
                    }
                ]
                + [
                    {
                        "role": "tool",
                        "tool_call_id": call["id"] or "call_%d" % index,
                        "content": json.dumps(TOOL_RESULTS.get(call["name"], {"value": "n/a"})),
                    }
                    for index, call in enumerate(raw_calls)
                ],
            }
            if deadline - time.monotonic() >= 20:
                run(
                    "tools_followup",
                    followup,
                    lambda follow_record: follow_record.update(
                        {
                            "tool_results_referenced": _tool_result_reference_count(
                                follow_record.get("_content_text", ""), raw_calls
                            ),
                            "tool_results_expected": len(raw_calls),
                        }
                    ),
                )
    return results


def summarize_stability(records, required_samples=DEFAULT_REPEAT):
    samples = [record for record in records if str(record.get("case", "")).startswith("stability_")]
    successes = sum(1 for record in samples if record.get("ok"))
    ttfts = [record.get("ttft") for record in samples if isinstance(record.get("ttft"), (int, float))]
    return {
        "samples": len(samples),
        "successes": successes,
        "success_rate": round(successes / len(samples), 3) if samples else 0.0,
        "ttft_min": min(ttfts) if ttfts else None,
        "ttft_max": max(ttfts) if ttfts else None,
        "passed": len(samples) >= required_samples and (successes / len(samples) >= 0.95 if samples else False),
    }


def summarize_records(records, models=None, matrix_seconds=None):
    safe_records = [sanitize_record(record) for record in records]
    status_counts = Counter()
    error_categories = Counter()
    for record in safe_records:
        if record.get("status") is not None:
            status_counts[str(record["status"])] += 1
        if record.get("error_category"):
            error_categories[record["error_category"]] += 1
    summary = {
        "matrix_seconds": matrix_seconds,
        "models": list(models or []),
        "record_count": len(safe_records),
        "status_counts": dict(sorted(status_counts.items())),
        "error_categories": dict(sorted(error_categories.items())),
        "stability": summarize_stability(records),
    }
    summary["redaction_passed"] = all(
        not any(field in record for field in _DROP_FIELDS) for record in safe_records
    )
    return sanitize_record(summary)


def _positive_int(value):
    try:
        number = int(value)
    except ValueError as error:
        raise argparse.ArgumentTypeError("must be an integer") from error
    if number <= 0:
        raise argparse.ArgumentTypeError("must be positive")
    return number


def _positive_float(value):
    try:
        number = float(value)
    except ValueError as error:
        raise argparse.ArgumentTypeError("must be a number") from error
    if number <= 0:
        raise argparse.ArgumentTypeError("must be positive")
    return number


def parse_context_targets(value):
    if isinstance(value, list):
        values = value
    else:
        values = str(value).split(",")
    targets = []
    for item in values:
        try:
            target = int(item)
        except (TypeError, ValueError) as error:
            raise argparse.ArgumentTypeError("context targets must be comma-separated integers") from error
        if target <= 0:
            raise argparse.ArgumentTypeError("context targets must be positive")
        if target not in targets:
            targets.append(target)
    if not targets:
        raise argparse.ArgumentTypeError("at least one context target is required")
    return targets


def parse_args(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--repeat", type=_positive_int, default=DEFAULT_REPEAT)
    parser.add_argument("--context-targets", type=parse_context_targets, default=list(DEFAULT_CONTEXT_TARGETS))
    parser.add_argument("--max-models", type=_positive_int, default=DEFAULT_MAX_MODELS)
    parser.add_argument("--matrix-budget-seconds", type=_positive_float, default=DEFAULT_MATRIX_BUDGET_SECONDS)
    parser.add_argument("--models", type=parse_model_ids, default=None, help="comma-separated model allowlist")
    return parser.parse_args(argv)


def main(argv=None):
    args = parse_args(argv)
    password = sys.stdin.readline().rstrip("\r\n")
    global admin_opener, v1_opener, access_key
    admin_opener = urllib.request.build_opener(urllib.request.HTTPCookieProcessor())
    v1_opener = urllib.request.build_opener()
    access_key = {"id": None, "key": None}
    status, _ = call_admin("POST", "/admin/api/auth/login", {"username": "admin", "password": password})
    log("L", {"step": "login", "status": status})
    if status != 200:
        raise SystemExit(2)
    try:
        status, body = call_admin("GET", "/admin/api/models")
        catalog = unwrap(body) if status == 200 else []
        if isinstance(catalog, list):
            for item in catalog:
                item["public_id"] = item.get("public_id") or item.get("model_id") or item.get("id")
            requested_models = args.models
            if requested_models is None and not REMOTE_MODEL_FILTER.startswith("__"):
                requested_models = [REMOTE_MODEL_FILTER]
            models = select_models(catalog, requested_models)
        else:
            models = []
        status, metrics = call_admin("GET", "/metrics", timeout=15)
        pool_healthy = None
        if status == 200:
            match = re.search(r"^nvidia_router_proxy_pool_healthy\s+(\d+)", metrics.decode("utf-8", "replace"), re.M)
            if match:
                pool_healthy = int(match.group(1))
        log("L", {"step": "catalog", "count": len(models), "pool_healthy": pool_healthy})
        status, body = call_admin("POST", "/admin/api/access-keys", {"name": RUN_TAG})
        created = unwrap(body) if status == 201 else {}
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
        except Exception:
            log("L", {"step": "v1_models", "error_category": "transport"})
        for meta in models:
            meta["public_id"] = meta.get("public_id") or meta.get("model_id") or meta.get("id")
            log(
                "M",
                {
                    "public_id": meta.get("public_id"),
                    "provider": meta.get("provider"),
                    "supports_reasoning": meta.get("supports_reasoning"),
                    "supports_tools": meta.get("supports_tools"),
                    "tools_status": meta.get("tools_status"),
                    "wire": meta.get("reasoning_wire_format"),
                    "context_length_db": meta.get("context_length"),
                    "context_length_api": exposed.get(meta.get("public_id")),
                },
            )

        def gate(meta):
            attempts = [
                chat(meta["public_id"], gate_payload(), timeout=GATE_TIMEOUT)
                for _ in range(2)
            ]
            statuses = [attempt.get("status") for attempt in attempts]
            categories = sorted({attempt["error_category"] for attempt in attempts if attempt.get("error_category")})
            return {
                "model": meta["public_id"],
                "provider": meta.get("provider"),
                "attempts": len(attempts),
                "statuses": statuses,
                "status": statuses[-1] if statuses else None,
                "total": round(sum(attempt.get("total") or 0 for attempt in attempts), 3),
                "consecutive_200": all(attempt.get("status") == 200 for attempt in attempts),
                "error_categories": categories,
                "ok": all(attempt.get("ok") for attempt in attempts),
            }

        with ThreadPoolExecutor(max_workers=MAX_CONCURRENCY) as pool:
            gate_results = list(pool.map(gate, models))
        for record in gate_results:
            log("G", record)
        passing = sorted([record for record in gate_results if record["ok"]], key=lambda record: record["total"])
        nvidia = [record for record in passing if record["provider"] == "nvidia"]
        others = [record for record in passing if record["provider"] != "nvidia"]
        chosen = [record["model"] for record in (nvidia[:2] + others)[: args.max_models]]
        chosen_metas = [meta for meta in models if meta["public_id"] in chosen]
        log("L", {"step": "selected", "models": chosen})

        deadline = time.monotonic() + args.matrix_budget_seconds
        started = time.monotonic()
        with ThreadPoolExecutor(max_workers=min(MAX_CONCURRENCY, len(chosen_metas) or 1)) as pool:
            matrix_results = list(
                pool.map(
                    lambda meta: run_matrix(
                        meta,
                        deadline,
                        repeat=args.repeat,
                        context_targets=args.context_targets,
                    ),
                    chosen_metas,
                )
            )
        elapsed = round(time.monotonic() - started, 1)
        records = [record for model_records in matrix_results for record in model_records]
        log("SUMMARY", summarize_records(records, models=chosen, matrix_seconds=elapsed))
    finally:
        if access_key["id"] is not None:
            call_admin("DELETE", "/admin/api/access-keys/%s" % access_key["id"])
        call_admin("POST", "/admin/api/auth/logout")
        log("L", {"step": "cleanup", "done": True})


if __name__ == "__main__":
    main()
