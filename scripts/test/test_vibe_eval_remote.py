import ast
import json
import os
import subprocess
import sys
import types
import unittest
from pathlib import Path


SCRIPT = Path(__file__).with_name("vibe_eval_remote.py")


def load_module_definitions():
    tree = ast.parse(SCRIPT.read_text(encoding="utf-8"), filename=str(SCRIPT))
    tree.body = [
        node
        for node in tree.body
        if not (
            isinstance(node, ast.Expr)
            and isinstance(node.value, ast.Call)
            and isinstance(node.value.func, ast.Name)
            and node.value.func.id == "main"
        )
    ]
    module = types.ModuleType("vibe_eval_remote_unit")
    module.__file__ = str(SCRIPT)
    exec(compile(tree, str(SCRIPT), "exec"), module.__dict__)
    return module


MODULE = load_module_definitions()


class ImportContractTest(unittest.TestCase):
    def test_import_does_not_read_stdin_or_access_network(self):
        code = "import importlib.util; p = r'%s'; s = importlib.util.spec_from_file_location('probe', p); m = importlib.util.module_from_spec(s); s.loader.exec_module(m)" % SCRIPT
        result = subprocess.run(
            [sys.executable, "-c", code],
            stdin=subprocess.DEVNULL,
            capture_output=True,
            text=True,
            timeout=5,
            env=os.environ.copy(),
        )
        self.assertEqual(result.returncode, 0, result.stderr)


class RedactionTest(unittest.TestCase):
    def test_sanitize_record_removes_content_messages_and_raw_tool_arguments(self):
        record = {
            "status": 200,
            "ok": True,
            "content_text": "Bearer live-key and admin-password",
            "message": {"role": "assistant", "content": "private response"},
            "tool_calls_raw": [
                {"id": "call-1", "name": "get_weather", "args": '{"city":"Hangzhou"}'},
            ],
        }

        safe = MODULE.sanitize_record(record)
        encoded = json.dumps(safe, ensure_ascii=False)

        self.assertEqual(safe["status"], 200)
        self.assertTrue(safe["ok"])
        self.assertEqual(safe["tool_call_count"], 1)
        self.assertTrue(safe["tool_names_present"])
        self.assertTrue(safe["tool_args_valid"])
        self.assertNotIn("content_text", safe)
        self.assertNotIn("message", safe)
        self.assertNotIn("tool_calls_raw", safe)
        self.assertNotIn("Bearer live-key", encoded)
        self.assertNotIn("admin-password", encoded)
        self.assertNotIn("Hangzhou", encoded)

    def test_log_serializes_only_safe_metrics(self):
        from contextlib import redirect_stdout
        from io import StringIO

        output = StringIO()
        with redirect_stdout(output):
            MODULE.log("R", {"content_text": "secret", "tool_calls_raw": [{"args": "private"}]})

        self.assertNotIn("secret", output.getvalue())
        self.assertNotIn("private", output.getvalue())


class ErrorClassificationTest(unittest.TestCase):
    def test_error_body_is_structured_without_returning_message_text(self):
        body = json.dumps(
            {"error": {"code": "upstream_error", "message": "Bearer private-key"}}
        ).encode()

        parsed = MODULE.parse_error_body(body)

        self.assertEqual(parsed["error_code"], "upstream_error")
        self.assertEqual(parsed["error_message_length"], len("Bearer private-key"))
        self.assertNotIn("Bearer private-key", json.dumps(parsed))

    def test_error_statuses_are_classified_separately(self):
        self.assertEqual(MODULE.classify_error(501, "not_implemented"), "unsupported")
        self.assertEqual(MODULE.classify_error(502, "upstream_error"), "bad_gateway")
        self.assertEqual(MODULE.classify_error(503, "upstream_unavailable"), "service_unavailable")
        self.assertEqual(MODULE.classify_error(0, "timeout"), "timeout")
        self.assertEqual(MODULE.classify_error(0, "transport"), "transport")


class SSEStatsTest(unittest.TestCase):
    def test_sse_stats_require_done_and_reject_malformed_events(self):
        lines = [
            b'data: {"choices":[{"delta":{"content":"OK"}}]}\n',
            b"data: {not-json}\n",
            b'data: {"choices":[{"finish_reason":"stop","delta":{}}]}\n',
            b"data: [DONE]\n",
        ]

        stats = MODULE.parse_sse_events(lines, clock=iter([1.0, 1.2, 1.3]).__next__)

        self.assertTrue(stats["done"])
        self.assertEqual(stats["content_chars"], 2)
        self.assertEqual(stats["malformed_events"], 1)
        self.assertEqual(stats["stream_error_code"], "malformed_sse")
        self.assertFalse(MODULE.stream_record_ok(200, stats))

    def test_eof_without_done_is_an_explicit_truncation(self):
        stats = MODULE.parse_sse_events([b'data: {"choices":[{"delta":{"content":"partial"}}]}\n'])

        self.assertFalse(stats["done"])
        self.assertEqual(stats["stream_error_code"], "upstream_stream_truncated")
        self.assertFalse(MODULE.stream_record_ok(200, stats))

    def test_empty_done_only_stream_is_not_a_success(self):
        stats = MODULE.parse_sse_events([b"data: [DONE]\n"])

        self.assertTrue(stats["done"])
        self.assertEqual(stats["stream_error_code"], "empty_stream")
        self.assertFalse(MODULE.stream_record_ok(200, stats))

    def test_reasoning_only_stream_is_not_a_user_visible_success(self):
        stats = {
            "done": True,
            "semantic_events": 2,
            "malformed_events": 0,
            "stream_error_code": None,
            "content_chars": 0,
            "reasoning_chars": 120,
            "tool_call_count": 0,
        }

        self.assertFalse(MODULE.stream_record_ok(200, stats))

    def test_tool_only_stream_is_a_user_visible_success(self):
        stats = {
            "done": True,
            "semantic_events": 2,
            "malformed_events": 0,
            "stream_error_code": None,
            "content_chars": 0,
            "reasoning_chars": 0,
            "tool_call_count": 1,
        }

        self.assertTrue(MODULE.stream_record_ok(200, stats))

    def test_stream_parser_keeps_internal_content_for_task_verification(self):
        stats = MODULE.parse_sse_events(
            [b'data: {"choices":[{"delta":{"content":"OK"}}]}\n', b"data: [DONE]\n"],
            include_raw=True,
        )

        self.assertEqual(stats["_content_text"], "OK")


class ChatRecordTest(unittest.TestCase):
    def test_chat_carries_stream_content_to_stability_verifier(self):
        class Response:
            status = 200

            def __enter__(self):
                return self

            def __exit__(self, *args):
                return False

            def __iter__(self):
                return iter([b'data: {"choices":[{"delta":{"content":"OK"}}]}\n', b"data: [DONE]\n"])

        class Opener:
            def open(self, request, timeout):
                return Response()

        original_opener = MODULE.v1_opener
        original_key = MODULE.access_key
        MODULE.v1_opener = Opener()
        MODULE.access_key = {"id": None, "key": "test-key"}
        try:
            record = MODULE.chat("stable/model", {"stream": True, "messages": []})
        finally:
            MODULE.v1_opener = original_opener
            MODULE.access_key = original_key

        self.assertEqual(record["_content_text"], "OK")

    def test_chat_marks_complete_nonstream_response(self):
        class Response:
            status = 200

            def __enter__(self):
                return self

            def __exit__(self, *args):
                return False

            def read(self):
                return b'{"choices":[{"message":{"content":"OK"},"finish_reason":"stop"}]}'

        class Opener:
            def open(self, request, timeout):
                return Response()

        original_opener = MODULE.v1_opener
        original_key = MODULE.access_key
        MODULE.v1_opener = Opener()
        MODULE.access_key = {"id": None, "key": "test-key"}
        try:
            record = MODULE.chat("stable/model", {"stream": False, "messages": []})
        finally:
            MODULE.v1_opener = original_opener
            MODULE.access_key = original_key

        self.assertTrue(record["done"])


class NeedleAndSummaryTest(unittest.TestCase):
    def test_needle_hit_returns_only_a_boolean(self):
        self.assertTrue(MODULE.needle_hit("answer: ZEBRA-42-VECTOR"))
        self.assertFalse(MODULE.needle_hit("answer: ZEBRA-42"))
        self.assertIsInstance(MODULE.needle_hit("ZEBRA-42-VECTOR"), bool)

    def test_inferred_tools_are_eligible_for_tool_matrix(self):
        self.assertTrue(
            MODULE._tools_are_supported(
                {"supports_tools": True, "tools_status": "inferred"}
            )
        )

    def test_reasoning_efforts_respect_zero_allowed(self):
        self.assertEqual(
            MODULE._reasoning_efforts(
                {
                    "supports_reasoning": True,
                    "reasoning_levels": ["none", "low", "high"],
                    "reasoning_zero_allowed": False,
                }
            ),
            ["low", "high"],
        )

    def test_unexpressible_declared_reasoning_profile_is_skipped(self):
        self.assertEqual(
            MODULE._reasoning_efforts(
                {
                    "supports_reasoning": True,
                    "reasoning_levels": ["none"],
                    "reasoning_zero_allowed": False,
                }
            ),
            [],
        )

    def test_stability_does_not_force_positive_reasoning_into_tiny_budget(self):
        plan = MODULE.matrix_plan(
            {
                "supports_reasoning": True,
                "reasoning_levels": ["low", "high"],
                "reasoning_zero_allowed": True,
                "supports_tools": False,
                "tools_status": "unknown",
                "context_length": 0,
            },
            repeat=1,
            context_targets=[],
        )
        stability = next(payload for case, payload in plan if case == "stability_1")
        self.assertNotIn("reasoning_effort", stability)

    def test_stability_reserves_budget_for_a_visible_answer(self):
        plan = MODULE.matrix_plan(
            {
                "supports_reasoning": True,
                "reasoning_levels": ["low", "high"],
                "reasoning_zero_allowed": True,
                "supports_tools": False,
                "tools_status": "unknown",
                "context_length": 0,
            },
            repeat=1,
            context_targets=[],
        )
        stability = next(payload for case, payload in plan if case == "stability_1")
        self.assertEqual(stability["max_tokens"], 512)

    def test_context_targets_do_not_invent_an_undeclared_window(self):
        self.assertEqual(
            MODULE.context_targets_for_model({"context_length": 8192}, [8192, 32768]),
            [8192],
        )
        self.assertEqual(
            MODULE.context_targets_for_model({"context_length": 0}, [8192, 32768]),
            [],
        )

    def test_unknown_context_runs_lower_bound_probe_without_declaring_window(self):
        plan = MODULE.matrix_plan(
            {
                "supports_reasoning": True,
                "reasoning_levels": ["low", "high"],
                "context_length": 0,
                "supports_tools": False,
                "tools_status": "unknown",
            },
            repeat=1,
            context_targets=[8192],
        )

        cases = [case for case, _ in plan]
        self.assertIn("context_needle_observed_8192", cases)
        self.assertNotIn("context_needle_skipped", cases)

    def test_context_probe_reserves_budget_for_needle_answer(self):
        self.assertEqual(MODULE.needle_payload(8192)["max_tokens"], 512)

    def test_stability_summary_has_twenty_sample_gate_and_no_content(self):
        records = [
            {"case": "stability_%d" % index, "ok": index != 20, "ttft": index / 100.0, "status": 200}
            for index in range(1, 21)
        ]

        summary = MODULE.summarize_stability(records)
        encoded = json.dumps(summary)

        self.assertEqual(summary["samples"], 20)
        self.assertEqual(summary["successes"], 19)
        self.assertEqual(summary["success_rate"], 0.95)
        self.assertTrue(summary["passed"])
        self.assertNotIn("content_text", encoded)

    def test_matrix_summary_counts_safe_error_categories(self):
        records = [
            {"case": "base", "ok": True, "status": 200},
            {"case": "tools", "ok": False, "status": 501, "error_category": "unsupported"},
            {"case": "stream", "ok": False, "status": 0, "error_category": "timeout", "content_text": "secret"},
        ]

        summary = MODULE.summarize_records(records, models=["model-a"], matrix_seconds=12.5)
        encoded = json.dumps(summary)

        self.assertEqual(summary["models"], ["model-a"])
        self.assertEqual(summary["status_counts"], {"0": 1, "200": 1, "501": 1})
        self.assertEqual(summary["error_categories"], {"timeout": 1, "unsupported": 1})
        self.assertTrue(summary["redaction_passed"])
        self.assertNotIn("secret", encoded)
        self.assertNotIn("content_text", encoded)


class MatrixRecordTest(unittest.TestCase):
    def test_matrix_results_keep_case_for_stability_summary(self):
        original_plan = MODULE.matrix_plan
        original_chat = MODULE.chat
        original_log = MODULE.log
        MODULE.matrix_plan = lambda meta, repeat, context_targets: [
            ("stability_1", {"stream": False, "messages": []})
        ]
        MODULE.chat = lambda public_id, payload: {
            "ok": True,
            "status": 200,
            "ttft": 0.01,
        }
        MODULE.log = lambda kind, payload: None
        try:
            records = MODULE.run_matrix(
                {"public_id": "stable/model", "provider": "opencodefree"},
                deadline=MODULE.time.monotonic() + 30,
                repeat=1,
                context_targets=[],
            )
        finally:
            MODULE.matrix_plan = original_plan
            MODULE.chat = original_chat
            MODULE.log = original_log

        self.assertEqual(records[0]["case"], "stability_1")

    def test_budget_skips_keep_case_for_summary(self):
        original_plan = MODULE.matrix_plan
        original_log = MODULE.log
        MODULE.matrix_plan = lambda meta, repeat, context_targets: [
            ("stability_1", {"stream": False, "messages": []})
        ]
        MODULE.log = lambda kind, payload: None
        try:
            records = MODULE.run_matrix(
                {"public_id": "stable/model", "provider": "opencodefree"},
                deadline=MODULE.time.monotonic() + 1,
                repeat=1,
                context_targets=[],
            )
        finally:
            MODULE.matrix_plan = original_plan
            MODULE.log = original_log

        self.assertEqual(records[0], {"case": "stability_1", "ok": False, "skipped": "budget"})

    def test_stability_requires_exact_visible_answer(self):
        self.assertTrue(MODULE.stability_record_ok({"ok": True, "_content_text": "OK"}))
        self.assertFalse(MODULE.stability_record_ok({"ok": True, "_content_text": "maybe"}))
        self.assertFalse(MODULE.stability_record_ok({"ok": True, "_content_text": ""}))


class ArgumentTest(unittest.TestCase):
    def test_gate_reserves_budget_for_a_visible_answer(self):
        self.assertEqual(MODULE.gate_payload()["max_tokens"], 512)

    def test_explicit_model_allowlist_is_parsed(self):
        args = MODULE.parse_args(["--models", "stable/model,second/model"])

        self.assertEqual(args.models, ["stable/model", "second/model"])

    def test_model_allowlist_filters_catalog_before_gate(self):
        selected = MODULE.select_models(
            [
                {"public_id": "stable/model", "enabled": True},
                {"public_id": "other/model", "enabled": True},
                {"public_id": "disabled/model", "enabled": False},
            ],
            ["stable/model"],
        )

        self.assertEqual([item["public_id"] for item in selected], ["stable/model"])

    def test_matrix_arguments_are_parameterized(self):
        args = MODULE.parse_args(
            [
                "--repeat",
                "20",
                "--context-targets",
                "8192,32768",
                "--max-models",
                "3",
                "--matrix-budget-seconds",
                "600",
            ]
        )

        self.assertEqual(args.repeat, 20)
        self.assertEqual(args.context_targets, [8192, 32768])
        self.assertEqual(args.max_models, 3)
        self.assertEqual(args.matrix_budget_seconds, 600.0)

    def test_default_repeat_is_twenty(self):
        args = MODULE.parse_args([])
        self.assertEqual(args.repeat, 20)


if __name__ == "__main__":
    unittest.main()
