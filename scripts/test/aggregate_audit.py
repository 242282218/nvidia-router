#!/usr/bin/env python3
"""Aggregate model_whitelist_audit_remote.py JSONL output into per-dimension tables.

Usage: python scripts/test/aggregate_audit.py [tmp/audit] [phase ...]
"""

import json
import sys
from pathlib import Path

root = Path(sys.argv[1]) if len(sys.argv) > 1 else Path("tmp/audit")
wanted = set(sys.argv[2:])

for path in sorted(root.glob("*.jsonl")):
    phase = path.stem
    if wanted and phase not in wanted:
        continue
    rows = []
    for line in path.read_text(encoding="utf-8", errors="replace").splitlines():
        if not line.startswith("R|"):
            continue
        try:
            rows.append(json.loads(line[2:]))
        except json.JSONDecodeError:
            continue
    cases = [r for r in rows if r.get("kind") == "case"]
    if not cases:
        continue
    print(f"\n########## PHASE {phase} ({len(cases)} cases) ##########")
    by_model = {}
    for row in cases:
        by_model.setdefault(row.get("model", "?"), []).append(row)
    for model, items in sorted(by_model.items()):
        ok = sum(1 for i in items if i.get("protocol_ok"))
        print(f"\n--- {model}  ok={ok}/{len(items)} ---")
        for item in items:
            flag = "PASS" if item.get("protocol_ok") else "FAIL"
            bits = [
                f"{flag} {item.get('case','?'):28s}",
                f"http={item.get('status')}",
                f"ms={item.get('elapsed_ms')}",
            ]
            for key in (
                "ttft_ms", "content_chars", "reasoning_chars", "finish_reason",
                "completion_tokens", "reasoning_tokens", "sse_events", "sse_malformed",
                "max_idle_gap_ms", "tool_calls", "tool_args_valid", "usage_in_stream",
                "error_code", "error_type", "note", "exception",
            ):
                value = item.get(key)
                if value not in (None, "", 0, False, []):
                    bits.append(f"{key}={value}")
            print("  " + " ".join(str(b) for b in bits))
