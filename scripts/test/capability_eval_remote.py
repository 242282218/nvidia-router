#!/usr/bin/env python3
"""Capability-oriented model matrix for the vibe-coding evaluation.

Runs on the domestic test host (hangzhou2-2) against the in-host router at
127.0.0.1:3756. The admin password arrives on stdin (one line); a temporary
access key is created for /v1 calls and always deleted in ``finally``.

Every case has a programmatic verifier (no LLM judging):
reasoning puzzles with unique numeric answers, generated Python executed in a
sandbox subprocess with timeouts, constraint-checked prose, JSON schema checks,
tool-call loops, a long-context needle and repeat-stability samples.

All log records pass through :func:`sanitize_record`; prompts, responses and
tool arguments never reach stdout.
"""

from __future__ import annotations

import ast
import json
import multiprocessing
import re
import sys
import time
import urllib.error
import urllib.request
from collections import Counter
from concurrent.futures import ThreadPoolExecutor

BASE = "http://127.0.0.1:3756"
RUN_TAG = "cap-eval-20260825"
GATE_TIMEOUT = 35
CHAT_TIMEOUT = 110
GATE_ATTEMPTS = 2
MAX_MODELS = 8
MATRIX_BUDGET_SECONDS = 600.0
REPEAT_SAMPLES = 3
NEEDLE_TARGETS = [8192]
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
_TOOL_FIELDS = {"tool_calls", "tool_calls_raw", "_tool_calls_raw"}
_SAFE_STRING_FIELDS = {
    "case",
    "error_category",
    "error_code",
    "finish_reason",
    "model",
    "provider",
    "public_id",
    "self_id_snippet",
    "skipped",
    "step",
    "tools_status",
    "wire",
}

admin_opener = None
v1_opener = None
access_key = {"id": None, "key": None}


def sanitize_record(record):
    """Keep only safe scalar metrics; raw prompts/responses/tool args never pass through."""
    if not isinstance(record, dict):
        return {}
    safe = {}
    tool_calls = None
    for key, value in record.items():
        key = str(key)
        if key in _TOOL_FIELDS:
            tool_calls = value
            continue
        if key in _DROP_FIELDS or key.startswith("_"):
            continue
        if isinstance(value, dict):
            cleaned = sanitize_record(value)
        elif isinstance(value, list):
            cleaned = [sanitize_record(item) if isinstance(item, dict) else item for item in value]
        elif isinstance(value, str):
            if key not in _SAFE_STRING_FIELDS:
                continue
            if key == "self_id_snippet":
                # already stripped to [A-Za-z0-9 .,:/-] by the verifier; allow spaces
                cleaned = value if re.fullmatch(r"[A-Za-z0-9 .,:/+-]{1,200}", value) else "redacted"
            else:
                cleaned = value if re.fullmatch(r"[A-Za-z0-9_.:/@+-]{1,200}", value) else "redacted"
        elif value is None or isinstance(value, (bool, int, float)):
            cleaned = value
        else:
            continue
        if cleaned is not None:
            safe[key] = cleaned
    if tool_calls is not None:
        calls = list(tool_calls or [])
        safe["tool_call_count"] = len(calls)
        if calls:
            safe["tool_names_present"] = all(bool(call.get("name")) for call in calls)
            safe["tool_args_valid"] = all(_is_json(call.get("args")) for call in calls)
    return safe


def _is_json(value):
    if not isinstance(value, str) or not value.strip():
        return False
    try:
        json.loads(value)
        return True
    except Exception:
        return False


def log(kind, payload):
    print("%s|%s" % (kind, json.dumps(sanitize_record(payload), sort_keys=True, separators=(",", ":"))), flush=True)


def _get_opener(kind):
    global admin_opener, v1_opener
    if kind == "admin":
        if admin_opener is None:
            admin_opener = urllib.request.build_opener(urllib.request.HTTPCookieProcessor())
        return admin_opener
    if v1_opener is None:
        v1_opener = urllib.request.build_opener()
    return v1_opener


def call_admin(method, path, payload=None, timeout=30):
    data = None if payload is None else json.dumps(payload, separators=(",", ":")).encode()
    headers = {"Origin": BASE}
    if data is not None:
        headers["Content-Type"] = "application/json"
    request = urllib.request.Request(BASE + path, data=data, headers=headers, method=method)
    try:
        with _get_opener("admin").open(request, timeout=timeout) as response:
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
        return "timeout" if error_code in {"timeout", "timed_out"} else "transport"
    if isinstance(status, int) and 400 <= status < 500:
        return "request_error"
    return "unknown"


def parse_error_body(body):
    text = body.decode("utf-8", "replace") if isinstance(body, bytes) else str(body or "")
    try:
        value = json.loads(text)
        error = value.get("error", value) if isinstance(value, dict) else {}
        code = str(error.get("code") or error.get("type") or "error") if isinstance(error, dict) else "error"
        message = str(error.get("message") or "") if isinstance(error, dict) else ""
    except Exception:
        code, message = "unparseable_error", text
    return {
        "error_code": re.sub(r"[^A-Za-z0-9_.-]", "", code)[:80] or "error",
        "error_message_present": bool(message),
    }


def _text_value(value):
    if value is None:
        return ""
    if isinstance(value, str):
        return value
    if isinstance(value, list):
        return "".join(_text_value(i.get("text", "")) if isinstance(i, dict) else _text_value(i) for i in value)
    if isinstance(value, dict):
        return "".join(_text_value(v) for v in value.values())
    return str(value)


def has_user_visible_output(content_chars, tool_call_count=0):
    return bool(content_chars or tool_call_count)


def user(content):
    return [{"role": "user", "content": content}]


def chat(model, payload, timeout=CHAT_TIMEOUT):
    started = time.monotonic()
    record = {"model": model, "case": payload.pop("_case", None)}
    body_payload = {k: v for k, v in payload.items() if k != "_case"}
    body_payload["model"] = model
    request = urllib.request.Request(
        BASE + "/v1/chat/completions",
        data=json.dumps(body_payload, separators=(",", ":")).encode(),
        headers={
            "Authorization": "Bearer " + (access_key["key"] or ""),
            "Content-Type": "application/json",
            "Accept": "text/event-stream" if payload.get("stream") else "application/json",
            "Connection": "close",
        },
        method="POST",
    )
    try:
        with _get_opener("v1").open(request, timeout=timeout) as response:
            record["status"] = response.status
            if payload.get("stream"):
                _consume_stream(response, record, started)
            else:
                body = json.loads(response.read(16 * 1024 * 1024))
                choices = body.get("choices") or []
                first = choices[0] if choices else {}
                message = first.get("message") or {}
                record["_content_text"] = message.get("content")
                record["content_chars"] = len(_text_value(message.get("content")))
                record["reasoning_chars"] = len(_text_value(message.get("reasoning_content")))
                raw_calls = []
                for call in message.get("tool_calls") or []:
                    fn = call.get("function") or {}
                    raw_calls.append({"id": call.get("id"), "name": fn.get("name"), "args": fn.get("arguments")})
                record["_tool_calls_raw"] = raw_calls
                record["finish_reason"] = first.get("finish_reason")
                record["completion_tokens"] = (body.get("usage") or {}).get("completion_tokens")
                record["empty_response"] = not has_user_visible_output(
                    record["content_chars"], len(raw_calls)
                )
                record["ok"] = record["status"] == 200 and not record["empty_response"]
    except urllib.error.HTTPError as error:
        record["status"] = error.code
        record.update(parse_error_body(error.read()))
        record["error_category"] = classify_error(error.code, record.get("error_code"))
        record["ok"] = False
    except (socket_timeout_bases()):
        record["status"] = 0
        record["error_code"] = "timeout"
        record["error_category"] = "timeout"
        record["ok"] = False
    except urllib.error.URLError as error:
        reason = error.reason
        is_timeout = isinstance(reason, (TimeoutError,)) or "timed out" in str(reason).lower()
        record["status"] = 0
        record["error_code"] = "timeout" if is_timeout else "transport"
        record["error_category"] = classify_error(0, record["error_code"])
        record["ok"] = False
    except Exception:
        record["status"] = 0
        record["error_code"] = "transport"
        record["error_category"] = "transport"
        record["ok"] = False
    record["total"] = round(time.monotonic() - started, 3)
    return record


def socket_timeout_bases():
    import socket

    return (socket.timeout, TimeoutError)


def _consume_stream(response, record, started):
    first_at = None
    last_at = None
    content_chars = 0
    reasoning_chars = 0
    events = 0
    malformed = 0
    done = False
    finish_reason = None
    chunks = []
    slots = {}
    while True:
        line = response.readline()
        if not line:
            break
        now = time.monotonic()
        if first_at is None:
            first_at = now
        last_at = now
        data = line.strip()
        if not data.startswith(b"data:"):
            continue
        data = data[5:].strip()
        if data == b"[DONE]":
            done = True
            break
        try:
            value = json.loads(data)
        except Exception:
            malformed += 1
            continue
        events += 1
        choices = value.get("choices") or []
        first = choices[0] if choices else {}
        delta = first.get("delta") or {}
        chunk_text = _text_value(delta.get("content"))
        if chunk_text:
            chunks.append(chunk_text)
            content_chars += len(chunk_text)
        reasoning_chars += len(_text_value(delta.get("reasoning_content")))
        for call in delta.get("tool_calls") or []:
            index = call.get("index", 0)
            slot = slots.setdefault(index, {"id": None, "name": "", "args": ""})
            if call.get("id"):
                slot["id"] = call["id"]
            fn = call.get("function") or {}
            if fn.get("name"):
                slot["name"] = slot["name"] + fn["name"]
            if fn.get("arguments"):
                slot["args"] = slot["args"] + fn["arguments"]
        if first.get("finish_reason"):
            finish_reason = first["finish_reason"]
    gaps = []
    record["ttft"] = round(first_at - started, 3) if first_at else None
    record["stream_done"] = done
    record["sse_events"] = events
    record["sse_malformed"] = malformed
    record["finish_reason"] = finish_reason
    record["content_chars"] = content_chars
    record["reasoning_chars"] = reasoning_chars
    record["_content_text"] = "".join(chunks)
    record["stream_gap_max"] = round(max(gaps), 3) if gaps else None
    record["_tool_calls_raw"] = [slots[i] for i in sorted(slots)]
    record["empty_response"] = not has_user_visible_output(content_chars, len(slots))
    record["ok"] = record["status"] == 200 and done and events > 0 and not record["empty_response"]


# ---------------------------------------------------------------------------
# sandboxed execution of generated code
# ---------------------------------------------------------------------------

def _pick_target_function(namespace, calls):
    """Choose the generated function the fixtures actually target.

    Models often emit helper functions alongside the entry point (observed in
    the 2026-08-25 hard code-gen probe: a same-arity parse_line shadowed the
    real function under naive "first callable" selection). Arity alone cannot
    break ties, so each candidate is trial-invoked against a deep copy of the
    first fixture and the first one that runs cleanly wins.
    """
    candidates = [
        candidate
        for candidate in namespace.values()
        if callable(candidate) and getattr(candidate, "__module__", "") == "sandbox"
    ]
    if len(candidates) == 1:
        return candidates[0]
    probe_args, _ = calls[0] if calls else ((), None)
    for candidate in candidates:
        try:
            candidate(*[json.loads(json.dumps(probe_args))])
            return candidate
        except TypeError:
            # wrong arity for this fixture
            continue
        except Exception:
            # ran but failed on content; still a plausible arity match, keep as fallback
            continue
    return candidates[0] if candidates else None


def _sandbox_worker(payload_conn, code, calls):
    try:
        namespace = {}
        exec(compile(code, "<model>", "exec"), {"__name__": "sandbox"}, namespace)
        outcomes = []
        fn = _pick_target_function(namespace, calls)
        if fn is None:
            payload_conn.send({"compiled": True, "found_function": False})
            return
        for args, expected in calls:
            # calls entries are [single_argument, expected]; the argument is
            # copied so in-place mutation by one case cannot leak into the next
            arg = [json.loads(json.dumps(args))]
            got = fn(*arg)
            outcomes.append({"args": repr(args)[:80], "got": repr(got)[:120], "expected": repr(expected)[:80]})
        payload_conn.send({"compiled": True, "found_function": True, "outcomes": outcomes})
    except Exception as error:
        payload_conn.send({"compiled": True, "error": "%s: %.160s" % (type(error).__name__, error)})


def run_generated_function(code, calls, timeout=10):
    """Execute model code in a child process; return (results, all_passed)."""
    # spawn is the default start method on Windows and the only one available on
    # macOS without forking issues, but exec-based callers (remote_exec) have no
    # __main__ guard to re-import, so fall back to in-process execution there.
    try:
        ctx = multiprocessing.get_context()
        parent, child = ctx.Pipe(duplex=False)
        proc = ctx.Process(target=_sandbox_worker, args=(child, code, calls))
        proc.start()
    except (RuntimeError, OSError, ValueError):
        return _sandbox_inline(code, calls)
    proc.join(timeout)
    if proc.exitcode != 0 and not parent.poll():
        # spawn re-import failures surface as a non-zero exit with no result;
        # fall back so exec-based callers (no __main__ guard) still get a verdict
        return _sandbox_inline(code, calls)
    if proc.is_alive():
        proc.terminate()
        proc.join(2)
        return {"error": "sandbox_timeout"}, False
    if parent.poll():
        try:
            results = parent.recv()
        except EOFError:
            return {"error": "sandbox_crash"}, False
    else:
        return {"error": "sandbox_no_result"}, False
    outcomes = results.get("outcomes")
    if outcomes is None:
        return results, False
    all_ok = len(outcomes) == len(calls) and all(_outcome_matches(o) for o in outcomes)
    return results, all_ok


def _outcome_matches(outcome):
    # repr() equality with numeric normalization: 1 == 1.0 and equal lists of
    # numbers must pass, so compare the parsed literals when possible
    try:
        got_value = ast.literal_eval(outcome["got"])
        expected_value = ast.literal_eval(outcome["expected"])
    except Exception:
        return outcome["got"] == outcome["expected"]

    def normalize(value):
        if isinstance(value, (list, tuple)):
            return [normalize(item) for item in value]
        if isinstance(value, bool):
            return value
        if isinstance(value, (int, float)):
            return round(float(value), 9)
        return value

    return normalize(got_value) == normalize(expected_value)


def _sandbox_inline(code, calls):
    """In-process fallback used only when spawning a sandbox subprocess fails."""
    try:
        namespace = {}
        exec(compile(code, "<model>", "exec"), {"__name__": "sandbox"}, namespace)
        fn = _pick_target_function(namespace, calls)
        if fn is None:
            return {"error": "no_function"}, False
        outcomes = []
        for args, expected in calls:
            try:
                # deep-copy so in-place mutation by one case cannot leak into the next
                got = fn(*[json.loads(json.dumps(args))])
            except Exception as error:
                return {"error": "%s: %.160s" % (type(error).__name__, error)}, False
            outcomes.append({"args": repr(args)[:80], "got": repr(got)[:120], "expected": repr(expected)[:80]})
        all_ok = len(outcomes) == len(calls) and all(_outcome_matches(o) for o in outcomes)
        return {"outcomes": outcomes}, all_ok
    except Exception as error:
        return {"error": "%s: %.160s" % (type(error).__name__, error)}, False


# ---------------------------------------------------------------------------
# verifiers
# ---------------------------------------------------------------------------

def extract_code_block(text):
    blocks = re.findall(r"```(?:python|py)?\s*\n(.*?)```", text, re.S)
    return blocks[-1].strip() if blocks else None


def check_number_answer(text, marker, expected, tolerance=1e-9):
    matches = re.findall(re.escape(marker) + r"\s*[:：]?\s*\$?(-?\d+(?:\.\d+)?)", text, re.I)
    if not matches:
        return False
    return any(abs(float(m) - expected) < tolerance for m in matches)


MERGE_TESTS = [
    [[[1, 3], [2, 6], [8, 10], [15, 18]], [[1, 6], [8, 10], [15, 18]]],
    [[[1, 2], [2, 3]], [[1, 3]]],
    [[], []],
    [[[5, 7]], [[5, 7]]],
    [[[1, 4], [0, 2]], [[0, 4]]],
    [[[1, 4], [4, 5], [6, 9]], [[1, 5], [6, 9]]],
    [[[3, 5], [1, 2], [2, 4], [7, 8]], [[1, 5], [7, 8]]],
    [[[1, 10], [2, 3]], [[1, 10]]],
]

DEBUG_SOURCE = '''def running_average(values):
    result = []
    total = 0
    for i in range(len(values)):
        total += values[i]
        result.append(total / len(values))
    return result
'''

DEBUG_TESTS = [
    [[1, 2, 3], [1, 1.5, 2]],
    [[4], [4]],
    [[2, 2, 2], [2, 2, 2]],
]


def verify_instruction_following(text):
    sentences = [s for s in re.split(r"[.!?\n]+", text) if s.strip()]
    body = "\n".join(sentences[:-1]) if text.rstrip().endswith("DONE") else text
    lines = [line.strip() for line in text.strip().splitlines() if line.strip()]
    checks = {
        "sentence_count_exact_3": len(sentences) == (4 if text.rstrip().endswith("DONE") else 3),
        "has_hit": bool(re.search(r"\bhit\b", body, re.I)),
        "has_ttl": bool(re.search(r"\bTTL\b", body)),
        "has_miss": bool(re.search(r"\bmiss", body, re.I)),
        "no_forbidden_word": not re.search(r"\bvery\b", text, re.I),
        "ends_with_done_line": bool(lines) and lines[-1].rstrip(".!") == "DONE",
    }
    return checks


INSTRUCTION_PROMPT = (
    "Follow ALL of these rules exactly:\n"
    "(a) Write exactly 3 sentences about HTTP caching.\n"
    "(b) Sentence 1 must contain the word 'hit'.\n"
    "(c) Sentence 2 must contain the abbreviation 'TTL'.\n"
    "(d) Sentence 3 must contain a word starting with 'miss'.\n"
    "(e) Do NOT use the word 'very' anywhere.\n"
    "(f) The last line of your reply must be exactly: DONE\n"
)

IDENTITY_PROMPT = "In one short sentence: which company made you and what model are you? Answer in English."


def needle_payload(context_target=8192):
    target_chars = max(12000, int(context_target) * 4)
    filler_unit = "Background note: routine operational text without any codes. "
    filler = (filler_unit * ((target_chars // len(filler_unit)) + 2))[:target_chars]
    midpoint = len(filler) // 2
    document = filler[:midpoint] + " IMPORTANT: the secret access code is " + NEEDLE + ". " + filler[midpoint:]
    return {
        "stream": False,
        "max_tokens": 512,
        "messages": user(document + "\n\nReply with only the secret access code mentioned above."),
    }


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


def _reasoning_efforts(meta):
    if not meta.get("supports_reasoning"):
        return []
    allowed = {"none", "low", "medium", "high"}
    declared = [str(value).lower() for value in (meta.get("reasoning_levels") or [])]
    efforts = [value for value in declared if value in allowed]
    if meta.get("reasoning_zero_allowed") is False:
        efforts = [value for value in efforts if value != "none"]
    if meta.get("reasoning_levels") is not None:
        return list(dict.fromkeys(efforts))
    fallback = ["none", "low", "high"]
    if meta.get("reasoning_zero_allowed") is False:
        fallback.remove("none")
    return fallback


def build_plan(meta):
    efforts = _reasoning_efforts(meta)
    effort_mid = "medium" if "medium" in efforts else ("high" if "high" in efforts else None)
    plan = []

    def add(case, payload):
        payload["_case"] = case
        plan.append((case, payload))

    add("identity", {"stream": False, "max_tokens": 64, "messages": user(IDENTITY_PROMPT)})
    if effort_mid:
        add(
            "reasoning_bat_ball",
            {
                "stream": False,
                "max_tokens": 1024,
                "reasoning_effort": effort_mid,
                "messages": user(
                    "A bat and a ball cost $1.10 in total. The bat costs $1.00 more than the ball. "
                    "How much does the ball cost (in dollars)? Work step by step, then end with 'ANSWER: $X.XX'."
                ),
            },
        )
        add(
            "reasoning_multi_step",
            {
                "stream": False,
                "max_tokens": 1024,
                "reasoning_effort": effort_mid,
                "messages": user(
                    "Factory machines A, B, C. A makes 6 units/hour, B makes 4 units/hour, "
                    "and C makes half of what A and B make together per hour. All three run for exactly 3 hours. "
                    "How many units do they produce in total? Show brief steps, then end with 'TOTAL: N'."
                ),
            },
        )
    else:
        add(
            "reasoning_bat_ball",
            {
                "stream": False,
                "max_tokens": 1024,
                "messages": user(
                    "A bat and a ball cost $1.10 in total. The bat costs $1.00 more than the ball. "
                    "How much does the ball cost (in dollars)? Think step by step, then end with 'ANSWER: $X.XX'."
                ),
            },
        )
    add(
        "code_gen_merge_intervals",
        {
            "stream": False,
            "max_tokens": 1500,
            "messages": user(
                "Write a Python 3 function `merge_intervals(intervals)` that takes a list of [start, end] "
                "pairs of ints and returns a NEW sorted list of merged non-overlapping intervals. "
                "Touching intervals must merge: [1,2],[2,3] -> [1,3]. Input may be unsorted. "
                "Reply with ONLY a ```python code block containing the function."
            ),
        },
    )
    add(
        "debug_running_average",
        {
            "stream": False,
            "max_tokens": 1200,
            "messages": user(
                "This function should return the running average after each element "
                "(element i's average covers values[0..i] inclusive). It has exactly ONE bug.\n\n"
                "```python\n" + DEBUG_SOURCE + "```\n\n"
                "Reply with: (1) one short sentence naming the bug, then (2) the full corrected function "
                "in a ```python code block."
            ),
        },
    )
    add("instruction_following", {"stream": False, "max_tokens": 400, "messages": user(INSTRUCTION_PROMPT)})
    # reasoning models can burn the whole completion budget on hidden thinking
    # (observed: finish_reason=length with empty JSON), so give json_mode room.
    add(
        "json_mode",
        {
            "stream": False,
            "max_tokens": 1024,
            "response_format": {"type": "json_object"},
            "messages": user('Return a JSON object exactly {"ok": true, "items": 3, "label": "demo"}. No other text.'),
        },
    )
    add(
        "tools_parallel",
        {
            "stream": False,
            "max_tokens": 1024,
            "tools": [WEATHER_TOOL, TIME_TOOL],
            "tool_choice": "auto",
            "messages": user("What is the weather in Beijing and the current time in Tokyo? You must call both tools before answering."),
        },
    )
    for index in range(1, REPEAT_SAMPLES + 1):
        # auto-reasoning models produce hidden thinking even for "say OK"; a
        # 32-token budget truncates before any final content appears, which read
        # as false stability failures in the 2026-08-25 matrix.
        entry = {"stream": True, "max_tokens": 512, "messages": user("Reply with exactly the word OK.")}
        if efforts and "none" in efforts:
            entry["reasoning_effort"] = "none"
        add("stability_%d" % index, entry)
    add("context_needle_8192", needle_payload(8192))
    return plan, effort_mid


def annotate_common(record):
    record["needle_hit"] = NEEDLE in _text_value(record.get("_content_text", ""))
    return record


def run_case(model, case, payload, deadline):
    if time.monotonic() > deadline - 20:
        record = {"skipped": "budget", "ok": False}
    else:
        record = chat(model, payload)
        if not record["ok"] and record.get("error_code") in RETRYABLE and time.monotonic() < deadline - 40:
            time.sleep(10)
            retry = chat(model, payload)
            retry["retried"] = True
            record = retry
    record["model"] = model
    record["case"] = case
    return record


def verify_record(case, record):
    """Attach programmatic correctness verdicts; never stores raw text."""
    text = _text_value(record.get("_content_text", ""))
    if case == "identity":
        # collapse whitespace so the sanitized snippet survives the log sanitizer
        snippet = re.sub(r"[^A-Za-z0-9 .,:/-]", "", text)
        snippet = re.sub(r"\s+", " ", snippet).strip()[:100]
        record["self_id_snippet"] = snippet if snippet else "none"
        return
    if case == "reasoning_bat_ball":
        record["correct"] = check_number_answer(text, "ANSWER", 0.05) or bool(re.search(r"5\s*cents", text, re.I))
        record["answer_seen"] = check_number_answer(text, "ANSWER", 0.05)
        return
    if case == "reasoning_multi_step":
        record["correct"] = check_number_answer(text, "TOTAL", 45)
        return
    if case == "code_gen_merge_intervals":
        code = extract_code_block(text)
        if not code:
            record["correct"] = False
            record["verify_error"] = "no_code_block"
            return
        results, ok = run_generated_function(code, MERGE_TESTS)
        record["correct"] = ok
        record["sandbox_error"] = results.get("error")
        record["outcomes_failed"] = sum(
            1 for o in results.get("outcomes", []) if o.get("got") != o.get("expected")
        ) if results.get("outcomes") else None
        return
    if case == "debug_running_average":
        code = extract_code_block(text)
        record["named_bug"] = bool(re.search(r"len\(values\)|i \+ 1|i\+1|divide.{0,20}(count|index|elements so far)", text, re.I))
        if not code:
            record["correct"] = False
            record["verify_error"] = "no_code_block"
            return
        results, ok = run_generated_function(code, DEBUG_TESTS)
        record["correct"] = ok and record["named_bug"]
        record["fix_passes_tests"] = ok
        record["sandbox_error"] = results.get("error")
        return
    if case == "instruction_following":
        record.update({"if_" + key: value for key, value in verify_instruction_following(text).items()})
        record["correct"] = all(verify_instruction_following(text).values())
        return
    if case == "json_mode":
        try:
            value = json.loads(text.strip().removeprefix("```json").removesuffix("```").strip())
            record["correct"] = isinstance(value, dict) and value.get("ok") is True and value.get("items") == 3 and value.get("label") == "demo"
        except Exception:
            record["correct"] = False
        return
    if case == "tools_parallel":
        calls = record.get("_tool_calls_raw") or []
        names = sorted(str(c.get("name") or "") for c in calls)
        record["both_tools_called"] = names == ["get_time", "get_weather"]
        record["city_arg_ok"] = any("beijing" in str(c.get("args", "")).lower() for c in calls)
        return
    if case.startswith("stability_"):
        record["exact_ok"] = text.strip() == "OK"
        record["ok"] = bool(record.get("ok") and record["exact_ok"])
        return
    if case.startswith("context_needle"):
        record["needle_hit"] = NEEDLE in text
        return


def run_model_matrix(meta, deadline):
    model = meta["public_id"]
    plan, _ = build_plan(meta)
    records = []
    pending_followup = None
    for case, payload in plan:
        record = run_case(model, case, payload, deadline)
        verify_record(case, record)
        records.append(record)
        log("R", record)
        if case == "tools_parallel" and record.get("both_tools_called"):
            calls = record.get("_tool_calls_raw") or []
            pending_followup = (
                case,
                [
                    {
                        "role": "assistant",
                        "content": "",
                        "tool_calls": [
                            {
                                "id": call.get("id") or "call_%d" % idx,
                                "type": "function",
                                "function": {"name": call["name"], "arguments": call["args"]},
                            }
                            for idx, call in enumerate(calls)
                        ],
                    }
                ]
                + [
                    {
                        "role": "tool",
                        "tool_call_id": call.get("id") or "call_%d" % idx,
                        "content": json.dumps(TOOL_RESULTS.get(call["name"], {"value": "n/a"})),
                    }
                    for idx, call in enumerate(calls)
                ],
                calls,
            )
        time.sleep(1)
    if pending_followup and time.monotonic() < deadline - 25:
        _, history, calls = pending_followup
        original = next(payload for case, payload in plan if case == "tools_parallel")
        record = chat(model, {
            "stream": False,
            "max_tokens": 256,
            "messages": original["messages"] + history,
            "_case": "tools_followup",
        })
        record["model"] = model
        record["case"] = "tools_followup"
        text = _text_value(record.get("_content_text", ""))
        referenced = sum(
            1
            for call in calls
            for value in TOOL_RESULTS.get(call["name"], {}).values()
            if str(value) in text
        )
        record["tool_results_referenced"] = referenced
        record["tool_results_expected"] = len(calls)
        record["correct"] = referenced >= min(1, len(calls))
        records.append(record)
        log("R", record)
    return records


def main():
    password = sys.stdin.readline().rstrip("\r\n")
    status, _ = call_admin("POST", "/admin/api/auth/login", {"username": "admin", "password": password})
    log("L", {"step": "login", "status": status})
    if status != 200:
        raise SystemExit(2)
    try:
        status, body = call_admin("POST", "/admin/api/access-keys", {"name": RUN_TAG})
        created = unwrap(body) if status == 201 else {}
        access_key["id"] = created.get("id")
        access_key["key"] = created.get("key")
        log("L", {"step": "access_key", "status": status})
        if status != 201 or not access_key["key"]:
            raise SystemExit(3)
        status, body = call_admin("GET", "/admin/api/models")
        catalog = unwrap(body) if status == 200 else []
        models = [item for item in catalog if item.get("enabled") and item.get("kind") == "chat"]
        for meta in models:
            log("M", {
                "public_id": meta.get("public_id"),
                "provider": meta.get("provider"),
                "supports_reasoning": meta.get("supports_reasoning"),
                "supports_tools": meta.get("supports_tools"),
                "tools_status": meta.get("tools_status"),
                "wire": meta.get("reasoning_wire_format"),
                "context_length": meta.get("context_length"),
            })
        status, metrics = call_admin("GET", "/metrics", timeout=15)
        if status == 200:
            match = re.search(r"^nvidia_router_proxy_pool_healthy\s+(\d+)", metrics.decode("utf-8", "replace"), re.M)
            log("L", {"step": "pool", "healthy": int(match.group(1)) if match else None})

        # gate: two consecutive minimal requests per model, concurrent
        def gate(meta):
            attempts = [
                chat(meta["public_id"], {"stream": False, "max_tokens": 16, "messages": user("Reply with exactly OK.")}, timeout=GATE_TIMEOUT)
                for _ in range(GATE_ATTEMPTS)
            ]
            statuses = [a.get("status") for a in attempts]
            return {
                "model": meta["public_id"],
                "provider": meta.get("provider"),
                "statuses": statuses,
                "total": round(sum(a.get("total") or 0 for a in attempts), 3),
                "pass": all(s == 200 for s in statuses),
            }

        with ThreadPoolExecutor(max_workers=min(8, len(models) or 1)) as pool:
            gates = list(pool.map(gate, models))
        for entry in gates:
            entry["case"] = "gate"
            log("G", entry)
        passing = [entry for entry in gates if entry["pass"]]
        passing.sort(key=lambda e: e["total"])
        chosen_metas = [meta for meta in models if any(p["model"] == meta["public_id"] for p in passing[:MAX_MODELS])]
        log("L", {"step": "selected", "models": [m["public_id"] for m in chosen_metas]})

        deadline = time.monotonic() + MATRIX_BUDGET_SECONDS
        started = time.monotonic()
        with ThreadPoolExecutor(max_workers=min(MAX_MODELS, len(chosen_metas) or 1)) as pool:
            matrices = list(pool.map(lambda meta: run_model_matrix(meta, deadline), chosen_metas))
        elapsed = round(time.monotonic() - started, 1)

        for meta in chosen_metas:
            records = [r for rows in matrices for r in rows if r.get("model") == meta["public_id"]]
            cases = Counter(str(r.get("case")) for r in records)
            correct = Counter(str(r.get("case")) for r in records if r.get("correct"))
            statuses = Counter(str(r.get("status")) for r in records)
            totals = [r.get("total") for r in records if isinstance(r.get("total"), (int, float))]
            summary = {
                "model": meta["public_id"],
                "provider": meta.get("provider"),
                "records": len(records),
                "cases": dict(sorted(cases.items())),
                "correct_cases": {k: v for k, v in sorted(correct.items())},
                "statuses": dict(sorted(statuses.items())),
                "latency_total_sum": round(sum(totals), 1),
                "latency_total_mean": round(sum(totals) / len(totals), 2) if totals else None,
            }
            log("MODEL_SUMMARY", summary)
        log("SUMMARY", {"matrix_seconds": elapsed, "models": len(chosen_metas), "record_count": sum(len(rows) for rows in matrices)})
    finally:
        if access_key["id"] is not None:
            call_admin("DELETE", "/admin/api/access-keys/%s" % access_key["id"])
            log("L", {"step": "cleanup_access", "done": True})
        call_admin("POST", "/admin/api/auth/logout")


if __name__ == "__main__":
    main()
