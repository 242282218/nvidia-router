#!/usr/bin/env python3
"""Run a secret-safe compatibility and stability matrix against the router."""

from __future__ import annotations

import argparse
import json
import os
import statistics
import sys
import time
import uuid
from concurrent.futures import ThreadPoolExecutor
from dataclasses import dataclass
from http.cookiejar import CookieJar
from typing import Any
from urllib.error import HTTPError, URLError
from urllib.parse import urlsplit
from urllib.request import HTTPCookieProcessor, Request, build_opener, urlopen


MAX_CAPTURE_BYTES = 2 << 20
DEFAULT_TIMEOUT_SECONDS = 120
DEFAULT_LONG_TIMEOUT_SECONDS = 300


@dataclass(frozen=True)
class ResponseAnalysis:
    valid: bool
    content_chars: int = 0
    reasoning_chars: int = 0
    output_tokens: int | None = None
    events: int = 0
    done: bool = False
    output_indexes_valid: bool = True
    response_status: str = ""
    error: str = ""


@dataclass(frozen=True)
class CaseSpec:
    name: str
    endpoint: str
    stream: bool = False
    expected_status: int = 200
    documented_unsupported: bool = False


@dataclass(frozen=True)
class HttpResult:
    status: int
    body: bytes
    first_byte_ms: int | None
    total_ms: int
    truncated: bool = False
    error: str = ""


def _text_chars(value: Any) -> int:
    if isinstance(value, str):
        return len(value)
    if isinstance(value, list):
        return sum(_text_chars(item) for item in value)
    if isinstance(value, dict):
        return sum(_text_chars(value.get(key)) for key in ("text", "content", "delta") if key in value)
    return 0


def _usage_tokens(payload: dict[str, Any]) -> int | None:
    usage = payload.get("usage")
    if not isinstance(usage, dict):
        return None
    value = usage.get("completion_tokens", usage.get("output_tokens"))
    return value if isinstance(value, int) else None


def analyze_json_response(body: bytes) -> ResponseAnalysis:
    try:
        payload = json.loads(body)
    except (UnicodeDecodeError, json.JSONDecodeError):
        return ResponseAnalysis(False, error="invalid_json")
    if not isinstance(payload, dict):
        return ResponseAnalysis(False, error="json_not_object")

    choices = payload.get("choices")
    if isinstance(choices, list) and choices and isinstance(choices[0], dict):
        message = choices[0].get("message")
        if not isinstance(message, dict):
            return ResponseAnalysis(False, error="chat_message_missing")
        content = _text_chars(message.get("content"))
        reasoning = _text_chars(message.get("reasoning_content"))
        return ResponseAnalysis(
            True,
            content_chars=content,
            reasoning_chars=reasoning,
            output_tokens=_usage_tokens(payload),
            response_status=str(choices[0].get("finish_reason") or ""),
        )

    output = payload.get("output")
    if payload.get("object") == "response" and isinstance(output, list):
        content = 0
        reasoning = 0
        for item in output:
            if not isinstance(item, dict):
                continue
            if item.get("type") == "reasoning":
                reasoning += _text_chars(item.get("summary"))
            elif item.get("type") == "message":
                content += _text_chars(item.get("content"))
        if content == 0:
            content = _text_chars(payload.get("output_text"))
        return ResponseAnalysis(
            True,
            content_chars=content,
            reasoning_chars=reasoning,
            output_tokens=_usage_tokens(payload),
            response_status=str(payload.get("status") or ""),
        )

    return ResponseAnalysis(False, error="unexpected_json_shape")


def _analyze_sse_event(event_name: str, data: str, state: dict[str, Any]) -> None:
    if data.strip() == "[DONE]":
        state["done"] = True
        return
    try:
        payload = json.loads(data)
    except json.JSONDecodeError:
        state["error"] = "invalid_sse_json"
        return
    if not isinstance(payload, dict):
        state["error"] = "sse_data_not_object"
        return
    state["events"] += 1

    for choice in payload.get("choices", []):
        if not isinstance(choice, dict):
            continue
        delta = choice.get("delta")
        if isinstance(delta, dict):
            state["content_chars"] += _text_chars(delta.get("content"))
            state["reasoning_chars"] += _text_chars(delta.get("reasoning_content"))

    event_type = str(payload.get("type") or event_name or "")
    delta = payload.get("delta")
    if event_type.endswith("output_text.delta") and isinstance(delta, str):
        state["content_chars"] += len(delta)
    if event_type.endswith("reasoning_summary_text.delta") and isinstance(delta, str):
        state["reasoning_chars"] += len(delta)

    output_index = payload.get("output_index")
    if "output_item.added" in event_type and isinstance(output_index, int):
        if output_index in state["added_indexes"]:
            state["output_indexes_valid"] = False
        state["added_indexes"].add(output_index)
    if "output_item.done" in event_type and isinstance(output_index, int):
        if output_index not in state["added_indexes"]:
            state["output_indexes_valid"] = False

    if event_type in {"done", "response.completed", "response.incomplete", "response.failed"}:
        state["done"] = True
    if payload.get("done") is True:
        state["done"] = True


def analyze_sse(body: bytes) -> ResponseAnalysis:
    state: dict[str, Any] = {
        "events": 0,
        "content_chars": 0,
        "reasoning_chars": 0,
        "done": False,
        "output_indexes_valid": True,
        "added_indexes": set(),
        "error": "",
    }
    event_name = ""
    data_lines: list[str] = []

    def flush() -> None:
        nonlocal event_name, data_lines
        if data_lines:
            _analyze_sse_event(event_name, "\n".join(data_lines), state)
        event_name = ""
        data_lines = []

    for line in body.decode("utf-8", errors="replace").splitlines():
        if line == "":
            flush()
        elif line.startswith("event:"):
            event_name = line[6:].strip()
        elif line.startswith("data:"):
            data_lines.append(line[5:].lstrip())
    flush()

    valid = bool(state["events"]) and state["done"] and not state["error"]
    return ResponseAnalysis(
        valid,
        content_chars=state["content_chars"],
        reasoning_chars=state["reasoning_chars"],
        events=state["events"],
        done=state["done"],
        output_indexes_valid=state["output_indexes_valid"],
        error=state["error"] or ("output_index_mismatch" if not state["output_indexes_valid"] else ""),
    )


def planned_cases(model: dict[str, Any]) -> list[CaseSpec]:
    cases = [
        CaseSpec("chat.nonstream", "/v1/chat/completions"),
        CaseSpec("chat.stream", "/v1/chat/completions", stream=True),
        CaseSpec("responses.nonstream", "/v1/responses"),
        CaseSpec("responses.stream", "/v1/responses", stream=True),
        CaseSpec("chat.output.short", "/v1/chat/completions"),
        CaseSpec("chat.output.long", "/v1/chat/completions"),
        CaseSpec("chat.long_input", "/v1/chat/completions"),
        CaseSpec("chat.developer_role", "/v1/chat/completions"),
        CaseSpec("chat.structured", "/v1/chat/completions"),
        CaseSpec(
            "responses.structured",
            "/v1/responses",
            expected_status=400,
            documented_unsupported=True,
        ),
    ]
    if model.get("supports_reasoning") is True:
        cases.extend(
            [
                CaseSpec("chat.reasoning.low", "/v1/chat/completions"),
                CaseSpec("chat.reasoning.medium", "/v1/chat/completions"),
                CaseSpec("chat.reasoning.high", "/v1/chat/completions"),
                CaseSpec("chat.thinking.enabled", "/v1/chat/completions"),
                CaseSpec("responses.reasoning.high", "/v1/responses"),
            ]
        )
    if model.get("supports_tools") is True:
        cases.extend(
            [
                CaseSpec("chat.tools", "/v1/chat/completions"),
                CaseSpec("responses.tools", "/v1/responses"),
            ]
        )
    return cases


def select_models(models: list[dict[str, Any]], selection: str) -> list[dict[str, Any]]:
    wanted = {value.strip() for value in selection.split(",") if value.strip()}
    if not wanted:
        return models
    return [model for model in models if str(model.get("public_id")) in wanted]


def _chat_payload(model: str, *, stream: bool = False, prompt: str = "Reply with exactly: OK.", max_tokens: int = 64) -> dict[str, Any]:
    return {
        "model": model,
        "messages": [{"role": "user", "content": prompt}],
        "stream": stream,
        "max_tokens": max_tokens,
    }


def _responses_payload(model: str, *, stream: bool = False, prompt: str = "Reply with exactly: OK.", max_tokens: int = 64) -> dict[str, Any]:
    return {"model": model, "input": prompt, "stream": stream, "max_output_tokens": max_tokens}


def payload_for(case: CaseSpec, model: dict[str, Any]) -> dict[str, Any]:
    public_id = str(model["public_id"])
    name = case.name
    if name.startswith("responses"):
        payload = _responses_payload(public_id, stream=case.stream)
    else:
        payload = _chat_payload(public_id, stream=case.stream)

    if name.endswith("output.short"):
        payload["messages" if name.startswith("chat") else "input"] = "Return one short sentence."
        payload["max_tokens" if name.startswith("chat") else "max_output_tokens"] = 16
    elif name.endswith("output.long"):
        prompt = "Write eight numbered observations, each with two complete sentences."
        payload["messages" if name.startswith("chat") else "input"] = prompt
        payload["max_tokens" if name.startswith("chat") else "max_output_tokens"] = 512
    elif name.endswith("long_input"):
        prompt = "Summarize the following deterministic context in three bullets.\n" + ("context-line-0123456789\n" * 160)
        payload["messages" if name.startswith("chat") else "input"] = prompt
        payload["max_tokens" if name.startswith("chat") else "max_output_tokens"] = 128
    elif name.endswith("developer_role"):
        payload["messages"] = [
            {"role": "developer", "content": "Answer concisely."},
            {"role": "user", "content": "Reply with exactly: OK."},
        ]
    elif name.endswith("structured"):
        if name.startswith("chat"):
            payload["response_format"] = {"type": "json_object"}
            payload["messages"] = [{"role": "user", "content": 'Return only JSON: {"ok":true}.'}]
        else:
            payload["text"] = {"format": {"type": "json_object"}}
    elif name.startswith("chat.reasoning"):
        level = name.rsplit(".", 1)[-1]
        payload["reasoning_effort"] = level
    elif name == "chat.thinking.enabled":
        payload["thinking"] = {"type": "enabled", "budget_tokens": 256}
    elif name == "responses.reasoning.high":
        payload["reasoning"] = {"effort": "high"}
    elif name.endswith("tools"):
        tool_schema = {
            "type": "function",
            "name": "lookup_weather",
            "description": "Return a weather summary.",
            "parameters": {
                "type": "object",
                "properties": {"city": {"type": "string"}},
                "required": ["city"],
                "additionalProperties": False,
            },
        }
        if name.startswith("chat"):
            tool_schema = {
                "type": "function",
                "function": {key: value for key, value in tool_schema.items() if key != "type"},
            }
        payload["tools"] = [tool_schema]
        payload["tool_choice"] = "none"
    return payload


class RouterClient:
    def __init__(self, base_url: str) -> None:
        parsed = urlsplit(base_url)
        if parsed.scheme not in {"http", "https"} or not parsed.netloc or parsed.username or parsed.password:
            raise ValueError("base URL must be an HTTP(S) URL without credentials")
        self.base_url = base_url.rstrip("/")
        self.origin = f"{parsed.scheme}://{parsed.netloc}"
        self.cookies = CookieJar()
        self.admin_opener = build_opener(HTTPCookieProcessor(self.cookies))

    def request(
        self,
        method: str,
        path: str,
        payload: dict[str, Any] | None = None,
        *,
        access_key: str = "",
        admin: bool = False,
        timeout: int = DEFAULT_TIMEOUT_SECONDS,
    ) -> HttpResult:
        body = None if payload is None else json.dumps(payload, separators=(",", ":")).encode()
        headers = {"Accept": "application/json", "Origin": self.origin}
        if body is not None:
            headers["Content-Type"] = "application/json"
        if access_key:
            headers["Authorization"] = f"Bearer {access_key}"
        request = Request(self.base_url + path, data=body, headers=headers, method=method)
        started = time.perf_counter()
        opener = self.admin_opener if admin else None
        try:
            response = (opener.open(request, timeout=timeout) if opener else urlopen(request, timeout=timeout))
            return self._read_response(response, started)
        except HTTPError as error:
            return self._read_response(error, started, status=error.code)
        except (TimeoutError, URLError, OSError) as error:
            return HttpResult(0, b"", None, _elapsed_ms(started), error=type(error).__name__)

    @staticmethod
    def _read_response(response: Any, started: float, *, status: int | None = None) -> HttpResult:
        first_byte_ms: int | None = None
        captured = bytearray()
        total = 0
        truncated = False
        try:
            while True:
                chunk = response.read(8192)
                if not chunk:
                    break
                if first_byte_ms is None:
                    first_byte_ms = _elapsed_ms(started)
                total += len(chunk)
                if len(captured) < MAX_CAPTURE_BYTES:
                    remaining = MAX_CAPTURE_BYTES - len(captured)
                    captured.extend(chunk[:remaining])
                if len(captured) >= MAX_CAPTURE_BYTES and total > MAX_CAPTURE_BYTES:
                    truncated = True
        finally:
            response.close()
        return HttpResult(status or int(response.status), bytes(captured), first_byte_ms, _elapsed_ms(started), truncated)


def _elapsed_ms(started: float) -> int:
    return max(0, int((time.perf_counter() - started) * 1000))


def _json_body(result: HttpResult) -> dict[str, Any] | None:
    try:
        value = json.loads(result.body)
    except (UnicodeDecodeError, json.JSONDecodeError):
        return None
    return value if isinstance(value, dict) else None


def _error_code(result: HttpResult) -> str:
    return _error_metadata(result).get("error_code", "")


def _error_metadata(result: HttpResult) -> dict[str, str]:
    payload = _json_body(result)
    error = payload.get("error") if isinstance(payload, dict) else None
    if not isinstance(error, dict):
        return {}
    metadata: dict[str, str] = {}
    for source, target in (("code", "error_code"), ("type", "error_type"), ("param", "error_param")):
        value = error.get(source)
        if not isinstance(value, str) or not value or len(value) > 80:
            continue
        if all(character.isalnum() or character in "._-" for character in value):
            metadata[target] = value
    return metadata


def _outcome(result: HttpResult, analysis: ResponseAnalysis | None, case: CaseSpec) -> str:
    if result.status == case.expected_status:
        if case.documented_unsupported:
            return "EXPECTED_REJECTION" if _error_code(result) else "CONTRACT_FAIL"
        if analysis is None or not analysis.valid or not analysis.output_indexes_valid:
            return "CONTRACT_FAIL"
        return "PASS"
    if result.status in {0, 408, 425, 429, 500, 502, 503, 504, 529}:
        return "UPSTREAM_UNAVAILABLE"
    return "CONTRACT_FAIL"


def _record(case: str, model: str, result: HttpResult, analysis: ResponseAnalysis | None, outcome: str, **extra: Any) -> dict[str, Any]:
    record: dict[str, Any] = {
        "case": case,
        "model": model,
        "outcome": outcome,
        "http_status": result.status,
        "first_byte_ms": result.first_byte_ms,
        "total_ms": result.total_ms,
        "response_bytes": len(result.body),
    }
    if result.error:
        record["transport_error"] = result.error
    record.update(_error_metadata(result))
    if analysis is not None:
        record.update(
            {
                "content_chars": analysis.content_chars,
                "reasoning_chars": analysis.reasoning_chars,
                "reasoning_present": analysis.reasoning_chars > 0,
                "sse_events": analysis.events,
                "sse_done": analysis.done,
                "output_indexes_valid": analysis.output_indexes_valid,
                "response_status": analysis.response_status,
            }
        )
        if analysis.output_tokens is not None:
            record["output_tokens"] = analysis.output_tokens
        if analysis.error:
            record["analysis_error"] = analysis.error
    record.update(extra)
    return record


def _print_record(record: dict[str, Any]) -> None:
    print(json.dumps(record, ensure_ascii=False, sort_keys=True), flush=True)


class MatrixRunner:
    def __init__(self, client: RouterClient, args: argparse.Namespace, password: str) -> None:
        self.client = client
        self.args = args
        self.password = password
        self.access_key = ""
        self.access_id: int | None = None
        self.logged_in = False
        self.records: list[dict[str, Any]] = []

    def emit(self, record: dict[str, Any]) -> None:
        self.records.append(record)
        _print_record(record)

    def admin_login(self) -> None:
        result = self.client.request(
            "POST",
            "/admin/api/auth/login",
            {"username": self.args.admin_username, "password": self.password},
            admin=True,
            timeout=20,
        )
        payload = _json_body(result)
        if result.status != 200 or not isinstance(payload, dict) or payload.get("authenticated") is not True:
            raise RuntimeError("admin_login_failed")
        self.logged_in = True

    def enabled_models(self) -> list[dict[str, Any]]:
        result = self.client.request("GET", "/admin/api/models", admin=True, timeout=20)
        payload = _json_body(result)
        items = payload.get("data") if isinstance(payload, dict) else None
        if result.status != 200 or not isinstance(items, list):
            raise RuntimeError("admin_model_list_failed")
        return [item for item in items if isinstance(item, dict) and item.get("enabled") is True]

    def create_access_key(self) -> None:
        name = f"model-matrix-{time.strftime('%Y%m%dT%H%M%SZ', time.gmtime())}-{uuid.uuid4().hex[:8]}"
        result = self.client.request("POST", "/admin/api/access-keys", {"name": name}, admin=True, timeout=20)
        payload = _json_body(result)
        if result.status != 201 or not isinstance(payload, dict):
            raise RuntimeError("access_key_create_failed")
        key = payload.get("key")
        access_id = payload.get("id")
        if not isinstance(key, str) or not key or not isinstance(access_id, int) or access_id <= 0:
            raise RuntimeError("access_key_response_invalid")
        self.access_key = key
        self.access_id = access_id

    def run_catalog_checks(self, models: list[dict[str, Any]]) -> None:
        enabled_ids = {str(item.get("public_id")) for item in models if item.get("public_id")}
        result = self.client.request("GET", "/v1/models", access_key=self.access_key, timeout=20)
        payload = _json_body(result)
        public_ids = {
            str(item.get("id"))
            for item in (payload.get("data", []) if isinstance(payload, dict) else [])
            if isinstance(item, dict) and item.get("id")
        }
        outcome = "PASS" if result.status == 200 and public_ids == enabled_ids else "CONTRACT_FAIL"
        self.emit(_record("whitelist.catalog", "*", result, None, outcome, enabled_count=len(enabled_ids), public_count=len(public_ids)))

        unknown = "__model_matrix_not_allowlisted__"
        result = self.client.request(
            "POST",
            "/v1/chat/completions",
            _chat_payload(unknown, max_tokens=1),
            access_key=self.access_key,
            timeout=20,
        )
        outcome = "PASS" if result.status in {400, 404} else "CONTRACT_FAIL"
        self.emit(_record("whitelist.unknown_model", unknown, result, None, outcome, error_code=_error_code(result)))

    def run_case(self, model: dict[str, Any], case: CaseSpec) -> dict[str, Any]:
        model_id = str(model["public_id"])
        payload = payload_for(case, model)
        timeout = self.args.long_timeout if case.name in {"chat.output.long", "chat.long_input"} else self.args.timeout
        result = self.client.request("POST", case.endpoint, payload, access_key=self.access_key, timeout=timeout)
        analysis = None
        if result.status == 200:
            analysis = analyze_sse(result.body) if case.stream else analyze_json_response(result.body)
        outcome = _outcome(result, analysis, case)
        record = _record(case.name, model_id, result, analysis, outcome, endpoint=case.endpoint)
        self.emit(record)
        return record

    def run_stability(self, model: dict[str, Any]) -> None:
        model_id = str(model["public_id"])
        samples: list[dict[str, Any]] = []
        for _ in range(self.args.repeats):
            result = self.client.request(
                "POST",
                "/v1/chat/completions",
                _chat_payload(model_id, max_tokens=32),
                access_key=self.access_key,
                timeout=self.args.timeout,
            )
            analysis = analyze_json_response(result.body) if result.status == 200 else None
            samples.append(_record("stability.sample", model_id, result, analysis, _outcome(result, analysis, CaseSpec("stability", ""))))
        successes = sum(sample["outcome"] == "PASS" for sample in samples)
        latencies = [sample["total_ms"] for sample in samples]
        record = {
            "case": "stability.repeat",
            "model": model_id,
            "outcome": "PASS" if successes == len(samples) else "UPSTREAM_UNAVAILABLE" if successes else "CONTRACT_FAIL",
            "repeats": len(samples),
            "successes": successes,
            "success_rate": successes / len(samples) if samples else 0,
            "p50_ms": int(statistics.median(latencies)) if latencies else None,
            "max_ms": max(latencies) if latencies else None,
        }
        self.emit(record)

    def run_concurrency(self, model: dict[str, Any]) -> None:
        model_id = str(model["public_id"])

        def one() -> dict[str, Any]:
            result = self.client.request(
                "POST",
                "/v1/chat/completions",
                _chat_payload(model_id, max_tokens=32),
                access_key=self.access_key,
                timeout=self.args.timeout,
            )
            analysis = analyze_json_response(result.body) if result.status == 200 else None
            return _record("concurrency.sample", model_id, result, analysis, _outcome(result, analysis, CaseSpec("concurrency", "")))

        started = time.perf_counter()
        with ThreadPoolExecutor(max_workers=self.args.concurrency) as executor:
            samples = list(executor.map(lambda _: one(), range(self.args.concurrency)))
        successes = sum(sample["outcome"] == "PASS" for sample in samples)
        self.emit(
            {
                "case": "stability.concurrent",
                "model": model_id,
                "outcome": "PASS" if successes == len(samples) else "UPSTREAM_UNAVAILABLE" if successes else "CONTRACT_FAIL",
                "concurrency": len(samples),
                "successes": successes,
                "success_rate": successes / len(samples) if samples else 0,
                "wall_ms": _elapsed_ms(started),
            }
        )

    def run(self) -> int:
        try:
            self.admin_login()
            all_models = self.enabled_models()
            models = select_models(all_models, self.args.models)
            if not models:
                raise RuntimeError("no_enabled_models_selected")
            self.create_access_key()
            self.run_catalog_checks(all_models)
            for model in models:
                if model.get("kind") != "chat":
                    self.emit({"case": "model.kind", "model": str(model.get("public_id")), "outcome": "SKIP", "kind": model.get("kind")})
                    continue
                cases = planned_cases(model)
                if self.args.profile == "smoke":
                    cases = [
                        case
                        for case in cases
                        if case.name
                        in {
                            "chat.nonstream",
                            "chat.stream",
                            "responses.nonstream",
                            "responses.stream",
                            "chat.reasoning.low",
                            "chat.reasoning.medium",
                            "chat.reasoning.high",
                            "chat.thinking.enabled",
                            "responses.reasoning.high",
                            "chat.output.long",
                        }
                    ]
                for case in cases:
                    self.run_case(model, case)
                self.run_stability(model)
                self.run_concurrency(model)
        except (RuntimeError, ValueError) as error:
            self.emit({"case": "runner", "model": "*", "outcome": "CONTRACT_FAIL", "error": str(error)})
        finally:
            self.cleanup()

        bad = {"CONTRACT_FAIL"}
        if any(record.get("outcome") in bad for record in self.records):
            return 1
        if any(record.get("outcome") == "UPSTREAM_UNAVAILABLE" for record in self.records):
            return 2
        return 0

    def cleanup(self) -> None:
        if self.access_id is not None and self.logged_in:
            result = self.client.request("DELETE", f"/admin/api/access-keys/{self.access_id}", admin=True, timeout=20)
            self.emit({"case": "cleanup.revoke_access_key", "model": "*", "outcome": "PASS" if result.status == 204 else "CONTRACT_FAIL", "http_status": result.status})
        if self.logged_in:
            result = self.client.request("POST", "/admin/api/auth/logout", admin=True, timeout=20)
            self.emit({"case": "cleanup.logout", "model": "*", "outcome": "PASS" if result.status == 204 else "CONTRACT_FAIL", "http_status": result.status})
        self.access_key = ""
        self.password = ""


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--base-url", default=os.environ.get("NVIDIA_ROUTER_LIVE_BASE_URL", "http://127.0.0.1:3756"))
    parser.add_argument("--admin-username", default=os.environ.get("NVIDIA_ROUTER_ADMIN_USERNAME", "admin"))
    parser.add_argument("--admin-password-stdin", action="store_true")
    parser.add_argument("--models", default=os.environ.get("NVIDIA_ROUTER_MATRIX_MODELS", ""))
    parser.add_argument("--profile", choices=("smoke", "full"), default="full")
    parser.add_argument("--repeats", type=int, default=3)
    parser.add_argument("--concurrency", type=int, default=2)
    parser.add_argument("--timeout", type=int, default=DEFAULT_TIMEOUT_SECONDS)
    parser.add_argument("--long-timeout", type=int, default=DEFAULT_LONG_TIMEOUT_SECONDS)
    args = parser.parse_args()
    if args.repeats < 1 or args.concurrency < 1 or args.timeout < 1 or args.long_timeout < 1:
        parser.error("numeric limits must be positive")
    return args


def read_password(args: argparse.Namespace) -> str:
    if args.admin_password_stdin:
        return sys.stdin.readline().rstrip("\r\n")
    return os.environ.get("NVIDIA_ROUTER_ADMIN_PASSWORD", "")


def main() -> int:
    args = parse_args()
    password = read_password(args)
    if not password:
        print("runner_error=missing_admin_password", file=sys.stderr)
        return 1
    try:
        runner = MatrixRunner(RouterClient(args.base_url), args, password)
    except ValueError as error:
        print(f"runner_error={error}", file=sys.stderr)
        return 1
    return runner.run()


if __name__ == "__main__":
    raise SystemExit(main())
