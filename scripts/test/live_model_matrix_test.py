import importlib.util
import unittest
from pathlib import Path


SCRIPT = Path(__file__).with_name("live-model-matrix.py")
SPEC = importlib.util.spec_from_file_location("live_model_matrix", SCRIPT)
MODULE = importlib.util.module_from_spec(SPEC)
assert SPEC.loader is not None
SPEC.loader.exec_module(MODULE)


class PayloadContractTest(unittest.TestCase):
    def test_output_payload_omits_reasoning_for_non_reasoning_model(self):
        payload = MODULE.payload_for(
            "nvidia/nemotron-3-ultra-550b-a55b",
            {"supports_reasoning": False},
            "output_64",
        )
        self.assertNotIn("reasoning_effort", payload)

    def test_output_payload_preserves_none_for_reasoning_model(self):
        payload = MODULE.payload_for(
            "deepseek-ai/deepseek-v4-flash-0731",
            {"supports_reasoning": True},
            "output_64",
        )
        self.assertEqual(payload.get("reasoning_effort"), "none")

    def test_reasoning_none_payload_omits_reasoning_for_non_reasoning_model(self):
        payload = MODULE.payload_for(
            "nvidia/nemotron-3-ultra-550b-a55b",
            {"supports_reasoning": False},
            "reasoning_none",
        )
        self.assertNotIn("reasoning_effort", payload)

    def test_all_reasoning_effort_cases_omit_field_for_non_reasoning_model(self):
        metadata = {"supports_reasoning": False}
        for case in ("reasoning_low", "reasoning_medium", "reasoning_high", "stream_low"):
            with self.subTest(case=case):
                self.assertNotIn("reasoning_effort", MODULE.payload_for("model", metadata, case))

    def test_remote_program_injects_compilable_payload_helpers(self):
        program = MODULE.build_remote_program(
            "output",
            ["nvidia/nemotron-3-ultra-550b-a55b"],
            0,
            30,
            False,
        )
        self.assertNotIn("__PAYLOAD_HELPERS__", program)
        compile(program, "<live-model-matrix-test>", "exec")


if __name__ == "__main__":
    unittest.main()
