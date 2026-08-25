import ast
import types
import unittest
from pathlib import Path


SCRIPT = Path(__file__).with_name("capability_eval_remote.py")


def load_module_definitions():
    tree = ast.parse(SCRIPT.read_text(encoding="utf-8"), filename=str(SCRIPT))
    filtered = []
    for node in tree.body:
        if isinstance(node, ast.If) and isinstance(node.test, ast.Compare):
            if any(isinstance(item, ast.Call) and isinstance(item.func, ast.Name) and item.func.id == "main" for item in ast.walk(node)):
                continue
        filtered.append(node)
    tree.body = filtered
    module = types.ModuleType("capability_eval_remote_unit")
    module.__file__ = str(SCRIPT)
    exec(compile(tree, str(SCRIPT), "exec"), module.__dict__)
    return module


MODULE = load_module_definitions()


class OutputSemanticsTest(unittest.TestCase):
    def test_reasoning_only_output_is_not_user_visible(self):
        self.assertFalse(MODULE.has_user_visible_output(0, 0))

    def test_tool_call_is_user_visible_without_text(self):
        self.assertTrue(MODULE.has_user_visible_output(0, 1))

    def test_context_probe_reserves_budget_for_needle_answer(self):
        self.assertEqual(MODULE.needle_payload(8192)["max_tokens"], 512)

    def test_stability_verifier_demotes_non_exact_answer(self):
        record = {"ok": True, "_content_text": "not OK"}

        MODULE.verify_record("stability_1", record)

        self.assertFalse(record["exact_ok"])
        self.assertFalse(record["ok"])

    def test_stability_verifier_rejects_ok_with_extra_text(self):
        record = {"ok": True, "_content_text": "OK with extra text"}

        MODULE.verify_record("stability_1", record)

        self.assertFalse(record["exact_ok"])
        self.assertFalse(record["ok"])

    def test_stability_does_not_force_disallowed_none_reasoning(self):
        plan, _ = MODULE.build_plan(
            {
                "supports_reasoning": True,
                "reasoning_levels": ["none", "low", "high"],
                "reasoning_zero_allowed": False,
            }
        )

        stability = next(payload for case, payload in plan if case == "stability_1")

        self.assertNotEqual(stability.get("reasoning_effort"), "none")

    def test_outcome_matching_does_not_evaluate_expressions(self):
        self.assertFalse(MODULE._outcome_matches({"got": "1 + 1", "expected": "2"}))

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


if __name__ == "__main__":
    unittest.main()
