"""Multi-dimension model whitelist audit — runs ON the domestic test host.

Executed through ``scripts/test/remote_exec.py``; the admin password arrives on
stdin and is never written to disk, argv or output. Emits one JSON object per
line prefixed with ``R|`` so the caller can aggregate without parsing prose.

Placeholders substituted by the caller: __PHASE__ (one phase or a comma-separated
phase list), __MODELS__, __WORKERS__, __TIMEOUT__, __REPEATS__.
"""

import json
import socket
import sys
import threading
import time
import urllib.error
import urllib.request
from concurrent.futures import ThreadPoolExecutor

BASE = "http://127.0.0.1:3756"
PHASE = "__PHASE__"
PHASES = [item.strip() for item in PHASE.split(",") if item.strip()]
MODEL_FILTER = set(json.loads("""__MODELS__"""))
WORKERS = int("__WORKERS__")
TIMEOUT = float("__TIMEOUT__")
REPEATS = int("__REPEATS__")
RUN_NAME = "whitelist-audit-" + PHASE

password = sys.stdin.readline().rstrip("\r\n")
jar = urllib.request.HTTPCookieProcessor()
opener = urllib.request.build_opener(jar)
admin_lock = threading.Lock()
print_lock = threading.Lock()


def emit(kind, **fields):
    fields["kind"] = kind
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


def unwrap(body):
    value = json.loads(body)
    if isinstance(value, dict) and "data" in value:
        return value["data"]
    return value


# ---------------------------------------------------------------- case builders

LONG_TEXT = ("The quick brown fox jumps over the lazy dog near the riverbank while "
             "seventeen engineers debate cache invalidation strategies. ")
NUMBER_TASK = ("Write exactly 120 numbered lines, one per line, formatted as 'N. item N'. "
               "Start at 1 and end at 120. No preamble, no summary.")


def base_payload(model, prompt="Reply with exactly OK.", tokens=64, stream=False):
    return {
        "model": model,
        "messages": [{"role": "user", "content": prompt}],
        "max_tokens": tokens,
        "stream": stream,
    }


def tools_spec():
    return [
        {
            "type": "function",
            "function": {
                "name": "get_weather",
                "description": "Get the current weather for a city.",
                "parameters": {
                    "type": "object",
                    "properties": {
                        "city": {"type": "string"},
                        "unit": {"type": "string", "enum": ["c", "f"]},
                    },
                    "required": ["city"],
                },
            },
        },
        {
            "type": "function",
            "function": {
                "name": "search_docs",
                "description": "Search internal documentation.",
                "parameters": {
                    "type": "object",
                    "properties": {"query": {"type": "string"}},
                    "required": ["query"],
                },
            },
        },
    ]


def build_cases(model, meta, phase):
    """Return [(case_name, endpoint, payload, expectation)]."""
    reasoning = bool(meta.get("supports_reasoning"))
    tools = bool(meta.get("supports_tools"))
    levels = meta.get("reasoning_levels") or []
    cases = []

    def add(name, payload, endpoint="/v1/chat/completions", expect="ok"):
        cases.append((name, endpoint, payload, expect))

    if phase == "probe":
        for index in range(1, 3):
            add("probe_%d" % index, base_payload(model, tokens=16))
        return cases

    if phase == "compat":
        add("compat_system_role", {
            "model": model,
            "messages": [
                {"role": "system", "content": "You are terse. Always answer with one word."},
                {"role": "user", "content": "Say OK."},
            ],
            "max_tokens": 32,
        })
        add("compat_multi_turn", {
            "model": model,
            "messages": [
                {"role": "system", "content": "You are a calculator."},
                {"role": "user", "content": "What is 2+2?"},
                {"role": "assistant", "content": "4"},
                {"role": "user", "content": "Add 3 to your previous answer. Reply with the number only."},
            ],
            "max_tokens": 32,
        })
        add("compat_content_parts", {
            "model": model,
            "messages": [{"role": "user", "content": [{"type": "text", "text": "Reply with exactly OK."}]}],
            "max_tokens": 32,
        })
        add("compat_sampling_params", dict(
            base_payload(model, tokens=48),
            temperature=0.2, top_p=0.9, frequency_penalty=0.1, presence_penalty=0.1, seed=42,
        ))
        add("compat_stop_sequence", dict(
            base_payload(model, prompt="Count: one two three four five", tokens=64), stop=["three"],
        ))
        add("compat_json_object", {
            "model": model,
            "messages": [{"role": "user", "content": 'Return JSON only: {"status":"ok"}'}],
            "max_tokens": 128,
            "response_format": {"type": "json_object"},
        })
        add("compat_user_field", dict(base_payload(model, tokens=32), user="audit-client"))
        add("compat_stream_usage", dict(
            base_payload(model, tokens=48, stream=True), stream_options={"include_usage": True},
        ))
        if tools:
            add("tools_auto", dict(
                base_payload(model, prompt="What is the weather in Hangzhou? Use a tool.", tokens=256),
                tools=tools_spec(), tool_choice="auto",
            ))
            add("tools_required", dict(
                base_payload(model, prompt="Weather in Beijing please.", tokens=256),
                tools=tools_spec(), tool_choice="required",
            ))
            add("tools_none", dict(
                base_payload(model, prompt="Say OK.", tokens=64),
                tools=tools_spec(), tool_choice="none",
            ))
            add("tools_named", dict(
                base_payload(model, prompt="Look up caching docs.", tokens=256),
                tools=tools_spec(),
                tool_choice={"type": "function", "function": {"name": "search_docs"}},
            ))
            add("tools_stream", dict(
                base_payload(model, prompt="Weather in Shanghai? Use a tool.", tokens=256, stream=True),
                tools=tools_spec(), tool_choice="auto",
            ))
            add("tools_result_round_trip", {
                "model": model,
                "max_tokens": 128,
                "messages": [
                    {"role": "user", "content": "What is the weather in Hangzhou?"},
                    {"role": "assistant", "content": None, "tool_calls": [{
                        "id": "call_1", "type": "function",
                        "function": {"name": "get_weather", "arguments": '{"city":"Hangzhou","unit":"c"}'},
                    }]},
                    {"role": "tool", "tool_call_id": "call_1", "content": '{"temp_c":21,"sky":"clear"}'},
                ],
                "tools": tools_spec(),
            })
        return cases

    if phase == "thinking":
        if not reasoning:
            add("thinking_unsupported_passthrough", dict(
                base_payload(model, tokens=64), reasoning_effort="high",
            ))
            return cases
        for level in ("none", "low", "medium", "high"):
            add("reasoning_%s" % level, dict(
                base_payload(model, prompt="A farmer has 17 sheep; all but 9 run away. How many remain? Explain briefly.", tokens=1024),
                reasoning_effort=level,
            ))
        for level in ("minimal", "xhigh", "max"):
            if level in levels:
                add("reasoning_%s" % level, dict(
                    base_payload(model, prompt="A farmer has 17 sheep; all but 9 run away. How many remain? Explain briefly.", tokens=1024),
                    reasoning_effort=level,
                ))
        add("thinking_enabled_budget", dict(
            base_payload(model, prompt="A farmer has 17 sheep; all but 9 run away. How many remain?", tokens=1024),
            thinking={"type": "enabled", "budget_tokens": 512},
        ))
        add("thinking_disabled", dict(
            base_payload(model, prompt="A farmer has 17 sheep; all but 9 run away. How many remain?", tokens=512),
            thinking={"type": "disabled"},
        ))
        add("reasoning_stream_high", dict(
            base_payload(model, prompt="Explain briefly why 2+2=4.", tokens=1024, stream=True),
            reasoning_effort="high",
        ))
        return cases

    if phase == "output":
        for tokens in (64, 512, 2048):
            payload = base_payload(model, prompt=NUMBER_TASK, tokens=tokens)
            if reasoning:
                payload["reasoning_effort"] = "none"
            add("output_%d" % tokens, payload)
        payload = base_payload(model, prompt=NUMBER_TASK, tokens=2048, stream=True)
        if reasoning:
            payload["reasoning_effort"] = "none"
        add("output_stream_2048", payload)
        return cases

    if phase == "long":
        payload = base_payload(
            model,
            prompt="Below is a document. Reply with the single word DOCUMENT-OK.\n\n" + LONG_TEXT * 380,
            tokens=64,
        )
        if reasoning:
            payload["reasoning_effort"] = "none"
        add("long_input_8k", payload)
        payload = base_payload(
            model,
            prompt=("Write a detailed technical design document about an HTTP reverse proxy with "
                    "connection pooling, retries, and observability. At least 2500 words, use headings."),
            tokens=4096,
            stream=True,
        )
        if reasoning:
            payload["reasoning_effort"] = "low"
        add("long_output_stream_4k", payload)
        payload = {
            "model": model,
            "max_tokens": 1024,
            "stream": True,
            "messages": [
                {"role": "user", "content": "Step 1: list three cache eviction policies."},
                {"role": "assistant", "content": "LRU, LFU, FIFO."},
                {"role": "user", "content": "Step 2: for each, give one failure mode."},
                {"role": "assistant", "content": "LRU: scan resistance. LFU: aging. FIFO: hot-item eviction."},
                {"role": "user", "content": "Step 3: recommend one for a proxy token cache and justify in 200 words."},
            ],
        }
        add("long_multi_turn_chain", payload)
        return cases

    if phase == "stability":
        for index in range(1, REPEATS + 1):
            payload = base_payload(model, prompt="Reply with exactly OK.", tokens=32)
            if reasoning:
                payload["reasoning_effort"] = "none"
            add("stability_%d" % index, payload)
        return cases

    if phase == "concurrency":
        for index in range(1, 4):
            payload = base_payload(model, prompt="Reply with exactly OK.", tokens=32)
            if reasoning:
                payload["reasoning_effort"] = "none"
            add("concurrent_%d" % index, payload)
        return cases

    if phase == "errors":
        add("err_max_tokens_zero", dict(base_payload(model, tokens=0)), expect="reject")
        add("err_max_tokens_negative", dict(base_payload(model, tokens=-5)), expect="reject")
        add("err_bad_reasoning_level", dict(base_payload(model, tokens=32), reasoning_effort="turbo"), expect="any")
        add("err_bad_thinking_type", dict(base_payload(model, tokens=32), thinking={"type": "sideways"}), expect="reject")
        add("err_empty_messages", {"model": model, "messages": [], "max_tokens": 32}, expect="reject")
        add("err_bad_role", {"model": model, "messages": [{"role": "wizard", "content": "hi"}], "max_tokens": 32}, expect="any")
        add("err_temperature_out_of_range", dict(base_payload(model, tokens=32), temperature=9.5), expect="any")
        add("err_responses_store", {"model": model, "input": "hi", "store": True},
            endpoint="/v1/responses", expect="reject")
        return cases

    if phase == "responses":
        add("responses_nonstream", {"model": model, "input": "Reply with exactly OK.", "max_output_tokens": 64},
            endpoint="/v1/responses")
        add("responses_stream", {"model": model, "input": "Reply with exactly OK.", "max_output_tokens": 64, "stream": True},
            endpoint="/v1/responses")
        return cases

    return cases


# ------------------------------------------------------------------ execution

def text_length(value):
    if isinstance(value, str):
        return len(value)
    if isinstance(value, list):
        total = 0
        for item in value:
            if isinstance(item, dict):
                total += text_length(item.get("text", ""))
            else:
                total += text_length(item)
        return total
    return 0


def parse_json_body(body):
    try:
        value = json.loads(body)
    except Exception:
        return {"protocol_ok": False, "parse_error": True}
    if not isinstance(value, dict):
        return {"protocol_ok": False, "parse_error": True}
    result = {}
    usage = value.get("usage") or {}
    if isinstance(usage, dict):
        result["prompt_tokens"] = usage.get("prompt_tokens") or usage.get("input_tokens")
        result["completion_tokens"] = usage.get("completion_tokens") or usage.get("output_tokens")
        details = usage.get("completion_tokens_details") or {}
        if isinstance(details, dict):
            result["reasoning_tokens"] = details.get("reasoning_tokens")
    choices = value.get("choices")
    if isinstance(choices, list) and choices:
        first = choices[0] if isinstance(choices[0], dict) else {}
        message = first.get("message") or {}
        calls = message.get("tool_calls")
        args_ok = None
        names = []
        if isinstance(calls, list) and calls:
            args_ok = True
            for call in calls:
                function = (call or {}).get("function") or {}
                names.append(function.get("name"))
                try:
                    json.loads(function.get("arguments") or "{}")
                except Exception:
                    args_ok = False
        result.update({
            "content_chars": text_length(message.get("content") or ""),
            "reasoning_chars": text_length(message.get("reasoning_content") or message.get("reasoning") or ""),
            "tool_calls": len(calls) if isinstance(calls, list) else 0,
            "tool_names": names,
            "tool_args_valid": args_ok,
            "finish_reason": first.get("finish_reason"),
            "protocol_ok": True,
            "model_echo": value.get("model"),
        })
        return result
    if "output" in value or "output_text" in value:  # responses API
        text = value.get("output_text")
        chars = text_length(text) if text else 0
        if not chars:
            for item in value.get("output") or []:
                for part in (item or {}).get("content") or []:
                    chars += text_length((part or {}).get("text", ""))
        result.update({"content_chars": chars, "protocol_ok": chars >= 0, "response_status": value.get("status")})
        return result
    result["protocol_ok"] = False
    return result


def parse_stream(response, started):
    ttft = None
    content = 0
    reasoning = 0
    calls = 0
    finish = None
    done = False
    malformed = 0
    events = 0
    usage_seen = False
    last = started
    max_gap = 0.0
    first_role = None
    tool_arg_fragments = {}
    while True:
        line = response.readline()
        if not line:
            break
        now = time.monotonic()
        if ttft is None:
            ttft = int((now - started) * 1000)
        else:
            max_gap = max(max_gap, now - last)
        last = now
        data = line.strip()
        if not data.startswith(b"data:"):
            continue
        data = data[5:].strip()
        if data == b"[DONE]":
            done = True
            continue
        try:
            value = json.loads(data)
        except Exception:
            malformed += 1
            continue
        events += 1
        if isinstance(value.get("usage"), dict):
            usage_seen = True
        choices = value.get("choices")
        if not isinstance(choices, list) or not choices:
            continue
        first = choices[0] if isinstance(choices[0], dict) else {}
        delta = first.get("delta") or {}
        if first_role is None and delta.get("role"):
            first_role = delta.get("role")
        content += text_length(delta.get("content") or "")
        reasoning += text_length(delta.get("reasoning_content") or delta.get("reasoning") or "")
        chunk_calls = delta.get("tool_calls")
        if isinstance(chunk_calls, list):
            calls += len(chunk_calls)
            for item in chunk_calls:
                index = (item or {}).get("index", 0)
                function = (item or {}).get("function") or {}
                tool_arg_fragments.setdefault(index, "")
                tool_arg_fragments[index] += function.get("arguments") or ""
        if first.get("finish_reason") is not None:
            finish = first.get("finish_reason")
    args_ok = None
    if tool_arg_fragments:
        args_ok = True
        for value in tool_arg_fragments.values():
            try:
                json.loads(value or "{}")
            except Exception:
                args_ok = False
    return {
        "ttft_ms": ttft,
        "content_chars": content,
        "reasoning_chars": reasoning,
        "tool_calls": calls,
        "tool_args_valid": args_ok,
        "finish_reason": finish,
        "stream_done": done,
        "sse_events": events,
        "sse_malformed": malformed,
        "usage_in_stream": usage_seen,
        "max_idle_gap_ms": int(max_gap * 1000),
        "first_delta_role": first_role,
        "protocol_ok": bool(done and events > 0 and malformed == 0),
    }


def error_fields(body, key):
    try:
        value = json.loads(body)
    except Exception:
        return {"error_code": "unparseable_error", "error_message": body[:200].decode("utf-8", "replace")}
    error = value.get("error", value) if isinstance(value, dict) else {}
    if not isinstance(error, dict):
        error = {}
    message = str(error.get("message") or "")[:600]
    if key:
        message = message.replace(key, "[redacted]")
    return {"error_code": str(error.get("code") or error.get("type") or "error"), "error_message": message}


def run_case(access_key, model, case, endpoint, payload, expect, phase):
    started = time.monotonic()
    record = {"model": model, "case": case, "endpoint": endpoint, "expect": expect, "phase": phase}
    stream = bool(payload.get("stream"))
    request = urllib.request.Request(
        BASE + endpoint,
        data=json.dumps(payload, separators=(",", ":")).encode(),
        headers={
            "Authorization": "Bearer " + access_key,
            "Content-Type": "application/json",
            "Accept": "text/event-stream" if stream else "application/json",
            "Connection": "close",
        },
        method="POST",
    )
    try:
        with urllib.request.urlopen(request, timeout=TIMEOUT) as response:
            record["status"] = response.status
            if stream:
                record.update(parse_stream(response, started))
            else:
                record["ttft_ms"] = None
                record.update(parse_json_body(response.read(16 * 1024 * 1024)))
    except urllib.error.HTTPError as error:
        body = error.read(256 * 1024)
        record["status"] = error.code
        record.update(error_fields(body, access_key))
        record["protocol_ok"] = False
    except (socket.timeout, TimeoutError):
        record["status"] = 0
        record["error_code"] = "client_timeout"
        record["protocol_ok"] = False
    except urllib.error.URLError as error:
        record["status"] = 0
        record["error_code"] = "url_error"
        record["error_message"] = type(error.reason).__name__
        record["protocol_ok"] = False
    except Exception as error:  # noqa: BLE001 - probe must never die on one case
        record["status"] = 0
        record["error_code"] = type(error).__name__
        record["protocol_ok"] = False
    record["elapsed_ms"] = int((time.monotonic() - started) * 1000)
    emit("case", **record)
    return record


def run_model(access_key, model, meta, phase):
    cases = build_cases(model, meta, phase)
    emit("plan", model=model, phase=phase, cases=[item[0] for item in cases])
    if phase == "concurrency":
        with ThreadPoolExecutor(max_workers=len(cases) or 1) as pool:
            list(pool.map(lambda item: run_case(access_key, model, *item, phase), cases))
        return
    for item in cases:
        run_case(access_key, model, *item, phase)
        time.sleep(0.4)


def main():
    status, _ = call_admin("POST", "/admin/api/auth/login", {"username": "admin", "password": password})
    emit("meta", step="login", status=status)
    if status != 200:
        return 1
    access_id = None
    access_key = None
    try:
        status, body = call_admin("GET", "/admin/api/access-keys")
        if status == 200:
            for item in unwrap(body):
                if item.get("name") == RUN_NAME:
                    call_admin("DELETE", "/admin/api/access-keys/%s" % item["id"])
        status, body = call_admin("POST", "/admin/api/access-keys", {"name": RUN_NAME})
        emit("meta", step="create_key", status=status)
        if status != 201:
            return 1
        created = json.loads(body)
        access_id = created["id"]
        access_key = created["key"]

        status, body = call_admin("GET", "/admin/api/models")
        catalog = unwrap(body) if status == 200 else []
        selected = [item for item in catalog if item.get("enabled") and item.get("kind") == "chat"]
        if MODEL_FILTER:
            selected = [item for item in selected if item.get("public_id") in MODEL_FILTER]
        emit("meta", step="selected", count=len(selected), models=[item["public_id"] for item in selected])
        for phase in PHASES:
            emit("meta", step="phase_start", phase=phase)
            with ThreadPoolExecutor(max_workers=max(1, min(WORKERS, len(selected) or 1))) as pool:
                futures = [pool.submit(run_model, access_key, item["public_id"], item, phase) for item in selected]
                for future in futures:
                    try:
                        future.result()
                    except Exception as error:  # noqa: BLE001
                        emit("meta", step="model_error", phase=phase, error=type(error).__name__)
    finally:
        if access_id is not None:
            status, _ = call_admin("DELETE", "/admin/api/access-keys/%s" % access_id)
            emit("meta", step="cleanup_key", status=status)
        call_admin("POST", "/admin/api/auth/logout")
    return 0


raise SystemExit(main())
