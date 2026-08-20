#!/usr/bin/env python3
"""Roll audit JSONL up into per-model / per-error totals for the report."""

import collections
import json
import sys
from pathlib import Path

root = Path(sys.argv[1]) if len(sys.argv) > 1 else Path("tmp/audit")
rows = []
for path in sorted(root.glob("*.jsonl")):
    for line in path.read_text(encoding="utf-8", errors="replace").splitlines():
        if not line.startswith("R|"):
            continue
        record = json.loads(line[2:])
        if record.get("kind") == "case":
            rows.append(record)

print("total cases:", len(rows))

by_model = collections.defaultdict(lambda: [0, 0])
by_phase = collections.defaultdict(lambda: [0, 0])
errors = collections.Counter()
for record in rows:
    ok = bool(record.get("protocol_ok"))
    for table, key in ((by_model, record["model"]), (by_phase, record.get("phase", "?"))):
        table[key][1] += 1
        table[key][0] += int(ok)
    if not ok:
        errors[(record["model"], str(record.get("error_code") or record.get("status")))] += 1

print("\n-- per model --")
for name, (ok, total) in sorted(by_model.items()):
    print(f"{name:42s} {ok}/{total}  {100 * ok / total:.0f}%")

print("\n-- per phase --")
for name, (ok, total) in sorted(by_phase.items()):
    print(f"{name:14s} {ok}/{total}  {100 * ok / total:.0f}%")

print("\n-- failures --")
for (model, code), count in errors.most_common():
    print(f"{model:42s} {code:28s} {count}")

print("\n-- latency (successful cases, ms) --")
for name in sorted(by_model):
    values = sorted(r["elapsed_ms"] for r in rows if r["model"] == name and r.get("protocol_ok"))
    if values:
        mid = values[len(values) // 2]
        print(f"{name:42s} n={len(values):3d} min={values[0]:6d} p50={mid:6d} max={values[-1]:6d}")
