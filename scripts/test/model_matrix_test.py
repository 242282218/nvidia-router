import unittest

from model_matrix import (
    CaseSpec,
    HttpResult,
    _record,
    analyze_json_response,
    analyze_sse,
    planned_cases,
    select_models,
)


class ResponseAnalysisTests(unittest.TestCase):
    def test_json_analysis_counts_content_reasoning_and_output_items(self):
        result = analyze_json_response(
            b'{"choices":[{"message":{"content":"OK","reasoning_content":"step"}}],'
            b'"usage":{"prompt_tokens":3,"completion_tokens":2}}'
        )

        self.assertTrue(result.valid)
        self.assertEqual(result.content_chars, 2)
        self.assertEqual(result.reasoning_chars, 4)
        self.assertEqual(result.output_tokens, 2)

    def test_sse_analysis_requires_done_and_counts_reasoning(self):
        result = analyze_sse(
            b'data: {"choices":[{"delta":{"reasoning_content":"step"}}]}\n\n'
            b'data: {"choices":[{"delta":{"content":"OK"},"finish_reason":"stop"}]}\n\n'
            b'data: [DONE]\n\n'
        )

        self.assertTrue(result.valid)
        self.assertTrue(result.done)
        self.assertEqual(result.reasoning_chars, 4)
        self.assertEqual(result.content_chars, 2)

    def test_responses_sse_detects_duplicate_output_indexes(self):
        result = analyze_sse(
            b'event: response.output_item.added\n'
            b'data: {"output_index":0,"item":{"type":"message"}}\n\n'
            b'event: response.output_item.added\n'
            b'data: {"output_index":0,"item":{"type":"function_call"}}\n\n'
            b'event: response.completed\n'
            b'data: {"response":{"status":"completed"}}\n\n'
            b'event: done\n'
            b'data: {"done":true}\n\n'
        )

        self.assertFalse(result.output_indexes_valid)


class MatrixPlanTests(unittest.TestCase):
    def test_reasoning_models_get_reasoning_and_output_cases(self):
        cases = planned_cases(
            {"public_id": "reasoning", "kind": "chat", "supports_reasoning": True}
        )

        names = {case.name for case in cases}
        self.assertIn("chat.reasoning.low", names)
        self.assertIn("chat.reasoning.medium", names)
        self.assertIn("chat.reasoning.high", names)
        self.assertIn("chat.thinking.enabled", names)
        self.assertIn("responses.reasoning.high", names)
        self.assertIn("chat.output.long", names)

    def test_model_selection_only_limits_execution_targets(self):
        models = [{"public_id": "a"}, {"public_id": "b"}]

        selected = select_models(models, "b")

        self.assertEqual(selected, [{"public_id": "b"}])

    def test_record_keeps_structured_error_code_without_error_body(self):
        record = _record(
            "chat.failure",
            "model",
            HttpResult(400, b'{"error":{"code":"invalid_parameter","type":"invalid_request_error","param":"max_tokens","message":"secret"}}', 1, 2),
            None,
            "CONTRACT_FAIL",
        )

        self.assertEqual(record["error_code"], "invalid_parameter")
        self.assertEqual(record["error_type"], "invalid_request_error")
        self.assertEqual(record["error_param"], "max_tokens")
        self.assertNotIn("secret", record)


if __name__ == "__main__":
    unittest.main()
