#!/usr/bin/env python3
"""Orchestrator: run multi-phase audit on working models only."""

import json
import os
import subprocess
import sys
from pathlib import Path

REMOTE_EXEC = Path(__file__).parent / "remote_exec.py"
REMOTE_PROBE = Path(__file__).parent / "model_whitelist_audit_remote.py"
PASSWORD = os.environ.get("NVIDIA_ROUTER_ADMIN_PASSWORD")
if not PASSWORD:
    raise SystemExit("NVIDIA_ROUTER_ADMIN_PASSWORD required")

WORKING_MODELS = [
    "minimaxai/minimax-m3",
    "nvidia/nemotron-3-ultra-550b-a55b",
    "stepfun-ai/step-3.7-flash",
    "z-ai/glm-5.2",
]

PHASES = [
    ("compat", 4, 300),
    ("thinking", 4, 400),
    ("output", 4, 400),
    ("long", 2, 600),
    ("stability", 4, 300),
    ("concurrency", 4, 200),
    ("errors", 4, 240),
    ("responses", 4, 240),
]

OUTDIR = Path(__file__).parents[2] / "tmp" / "audit"
OUTDIR.mkdir(parents=True, exist_ok=True)

for phase, workers, timeout in PHASES:
    print(f"=== PHASE {phase} workers={workers} timeout={timeout} ===", flush=True)
    out = OUTDIR / f"{phase}.jsonl"
    cmd = [
        "python", str(REMOTE_EXEC), str(REMOTE_PROBE),
        "--stdin-env", "NVIDIA_ROUTER_ADMIN_PASSWORD",
        "--timeout", str(timeout * len(WORKING_MODELS) + 180),
        "--arg", f"PHASE={phase}",
        "--arg", f"MODELS={json.dumps(WORKING_MODELS)}",
        "--arg", f"WORKERS={workers}",
        "--arg", f"TIMEOUT={timeout}",
        "--arg", "REPEATS=10",
    ]
    with out.open("w", encoding="utf-8") as handle:
        result = subprocess.run(cmd, text=True, capture_output=True, timeout=(timeout * len(WORKING_MODELS) + 240))
        handle.write(result.stdout)
        if result.stderr:
            sys.stderr.write(result.stderr)
        if result.returncode != 0:
            print(f"PHASE {phase} exit={result.returncode}", flush=True)
        else:
            lines = [line for line in result.stdout.splitlines() if line.startswith("R|")]
            success = sum(1 for line in lines if '"protocol_ok":true' in line)
            total = sum(1 for line in lines if '"kind":"case"' in line)
            print(f"PHASE {phase} done: {success}/{total} protocol_ok", flush=True)

print("All phases complete. Results in tmp/audit/*.jsonl", flush=True)
