#!/usr/bin/env bash
set -euo pipefail

repo_root="$(git rev-parse --show-toplevel)"
cd "$repo_root"

# Self-test mode builds throwaway dist trees to prove both failure modes are
# detected, so CI catches regressions in this gate itself.
if [[ "${NVIDIA_ROUTER_WEB_DIST_SELF_TEST:-0}" == "1" ]]; then
  scratch="$(mktemp -d)"
  trap 'rm -rf "$scratch"' EXIT

  run_case() {
    local name="$1" expect="$2" root="$3"
    local status=0
    NVIDIA_ROUTER_WEB_DIST_ROOT="$root" bash "$0" >/dev/null 2>&1 || status=$?
    if [[ "$expect" == "pass" && "$status" -ne 0 ]]; then
      printf 'self-test %s: expected pass, got exit %d\n' "$name" "$status" >&2
      exit 1
    fi
    if [[ "$expect" == "fail" && "$status" -eq 0 ]]; then
      printf 'self-test %s: expected failure, got exit 0\n' "$name" >&2
      exit 1
    fi
    printf 'self-test %s: ok\n' "$name"
  }

  mkdir -p "$scratch/complete/assets"
  printf '<script src="/assets/app-aaa.js"></script>' >"$scratch/complete/index.html"
  printf 'x' >"$scratch/complete/assets/app-aaa.js"
  run_case complete pass "$scratch/complete"

  mkdir -p "$scratch/missing/assets"
  printf '<script src="/assets/app-bbb.js"></script>' >"$scratch/missing/index.html"
  run_case missing-asset fail "$scratch/missing"

  mkdir -p "$scratch/stale/assets"
  printf '<script src="/assets/app-ccc.js"></script>' >"$scratch/stale/index.html"
  printf 'x' >"$scratch/stale/assets/app-ccc.js"
  printf 'x' >"$scratch/stale/assets/app-old.js"
  run_case stale-asset fail "$scratch/stale"

  printf 'Embedded frontend dist gate self-test passed.\n'
  exit 0
fi

dist_root="${NVIDIA_ROUTER_WEB_DIST_ROOT:-internal/web/dist}"
entry="$dist_root/index.html"
if [[ ! -f "$entry" ]]; then
  printf 'Missing embedded frontend entry: %s\n' "$entry" >&2
  exit 1
fi

python3 - "$entry" <<'PY'
from pathlib import Path
import re
import sys

entry = Path(sys.argv[1])
root = entry.parent
html = entry.read_text(encoding="utf-8")
references = re.findall(r'(?:src|href)=["\']([^"\']+)["\']', html)

reachable = set()
missing = []
for reference in references:
    if reference.startswith(("http://", "https://", "data:", "#")):
        continue
    target = root / reference.lstrip("/")
    if target.is_file():
        reachable.add(target.resolve())
    else:
        missing.append(reference)
if missing:
    raise SystemExit("Missing embedded frontend assets: " + ", ".join(missing))

# Stale fingerprinted files still get embedded by //go:embed all:dist and bloat
# the binary, so treat any unreferenced asset as a build-output drift failure.
assets = root / "assets"
orphans = sorted(
    path.name
    for path in assets.glob("*")
    if path.is_file() and path.resolve() not in reachable
) if assets.is_dir() else []
if orphans:
    raise SystemExit(
        "Stale embedded frontend assets (rerun 'pnpm --dir web run build'): "
        + ", ".join(orphans)
    )
PY

printf 'Embedded frontend assets are complete and free of stale files.\n'
