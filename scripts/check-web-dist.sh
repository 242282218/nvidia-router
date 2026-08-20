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
    NVIDIA_ROUTER_WEB_DIST_SELF_TEST=0 NVIDIA_ROUTER_WEB_DIST_ROOT="$root" bash "$0" >/dev/null 2>&1 || status=$?
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

  mkdir -p "$scratch/nested/assets/chunks"
  printf '<script src="/assets/app-ddd.js"></script>' >"$scratch/nested/index.html"
  printf 'assets/chunks/chunk-ddd.js' >"$scratch/nested/assets/app-ddd.js"
  printf 'x' >"$scratch/nested/assets/chunks/chunk-ddd.js"
  run_case nested-reachable pass "$scratch/nested"
  printf 'x' >"$scratch/nested/assets/chunks/chunk-old.js"
  run_case nested-stale fail "$scratch/nested"

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

entry = Path(sys.argv[1]).resolve()
root = entry.parent
assets = root / "assets"
asset_files = {
    path.relative_to(root).as_posix(): path.resolve()
    for path in assets.rglob("*")
    if path.is_file()
}

if not assets.is_dir():
    raise SystemExit(f"Missing embedded frontend assets directory: {assets}")

def asset_reference(value: str, base: Path) -> Path | None:
    value = value.split("?", 1)[0].split("#", 1)[0]
    if value.startswith(("http://", "https://", "data:", "#")):
        return None
    if value.startswith("/"):
        return (root / value.lstrip("/")).resolve()
    if value.startswith("assets/"):
        return (root / value).resolve()
    if value.startswith("./"):
        return (base.parent / value[2:]).resolve()
    return None

reference_patterns = (
    re.compile(r'(?:src|href)=["\']([^"\']+)["\']'),
    re.compile(r'import\(\s*["\']([^"\']+)["\']'),
    re.compile(r'(?:from|import)\s*["\']([^"\']+)["\']'),
    re.compile(r'url\(\s*["\']?([^"\')]+)'),
)

reachable = set()
missing = set()
pending = [entry]
while pending:
    current = pending.pop()
    if current in reachable or not current.is_file():
        continue
    reachable.add(current)
    content = current.read_text(encoding="utf-8")

    for pattern in reference_patterns:
        for reference in pattern.findall(content):
            target = asset_reference(reference, current)
            if target is None:
                continue
            if target.is_file():
                pending.append(target)
            else:
                missing.add(reference)

    # Vite writes lazy chunks into the entry's dependency map. Follow every
    # exact fingerprinted filename mentioned by a reachable asset, including
    # nested assets, while normal relative imports are handled above.
    for name, target in asset_files.items():
        if name in content and target not in reachable:
            pending.append(target)

if missing:
    raise SystemExit("Missing embedded frontend assets: " + ", ".join(sorted(missing)))

# Stale fingerprinted files still get embedded by //go:embed all:dist and bloat
# the binary, so treat any unreferenced asset as a build-output drift failure.
orphans = sorted(path.name for path in asset_files.values() if path not in reachable)
if orphans:
    raise SystemExit(
        "Stale embedded frontend assets (rerun 'pnpm --dir web run build'): "
        + ", ".join(orphans)
    )
PY

printf 'Embedded frontend assets are complete and free of stale files.\n'
