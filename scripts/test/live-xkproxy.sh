#!/usr/bin/env bash

set -euo pipefail
umask 077

report() {
  local case_name="$1"
  local status="$2"
  local started="$3"
  printf 'case=%s status=%s duration_ms=%s\n' "$case_name" "$status" "$((($(date +%s%3N) - started)))"
}

run_self_test() {
  local fixture
  fixture="$(mktemp "${TMPDIR:-/tmp}/xkproxy-live-self-test.XXXXXX")"
  chmod 600 -- "$fixture"
  trap 'rm -f -- "$fixture"' RETURN
  printf 'case=ProxyPreflight status=PASS duration_ms=0\n' >"$fixture"
  python3 - "$fixture" <<'PY'
import re
import sys

with open(sys.argv[1], "r", encoding="utf-8") as stream:
    lines = [line.rstrip("\r\n") for line in stream]
if lines != ["case=ProxyPreflight status=PASS duration_ms=0"]:
    raise SystemExit(1)
if any(re.search(pattern, "\n".join(lines), re.IGNORECASE) for pattern in ("apikey", "sign", "xkdaili", "nvapi-", "nvr_")):
    raise SystemExit(1)
PY
  printf 'case=SelfTest status=PASS duration_ms=0\n'
}

if [[ "${NVIDIA_ROUTER_XK_PROXY_LIVE_SELF_TEST:-0}" == '1' ]]; then
  run_self_test
  exit 0
fi

if [[ "${NVIDIA_ROUTER_XK_PROXY_LIVE_ENABLE:-0}" != '1' ]]; then
  printf 'case=Preflight status=BLOCKED duration_ms=0\n' >&2
  exit 2
fi

required=(
  NVIDIA_ROUTER_XK_PROXY_API_URL
  NVIDIA_ROUTER_LIVE_BASE_URL
  NVIDIA_ROUTER_ADMIN_PASSWORD
  NVIDIA_ROUTER_LIVE_KEY
  NVIDIA_ROUTER_LIVE_CHAT_MODEL
  NVIDIA_ROUTER_LIVE_EMBEDDING_MODEL
)
for name in "${required[@]}"; do
  if [[ -z "${!name:-}" ]]; then
    printf 'case=Preflight status=FAIL duration_ms=0\n' >&2
    exit 1
  fi
done

if ! command -v bash >/dev/null 2>&1 || ! command -v curl >/dev/null 2>&1 || ! command -v python3 >/dev/null 2>&1; then
  printf 'case=Preflight status=FAIL duration_ms=0\n' >&2
  exit 1
fi

repo_root="$(git rev-parse --show-toplevel)"
cd "$repo_root"
repeat="${NVIDIA_ROUTER_XK_PROXY_LIVE_REPEATS:-20}"
if [[ ! "$repeat" =~ ^[1-9][0-9]*$ ]]; then
  printf 'case=Preflight status=FAIL duration_ms=0\n' >&2
  exit 1
fi

live_log="$(mktemp "${TMPDIR:-/tmp}/xkproxy-live-output.XXXXXX")"
chmod 600 -- "$live_log"
proxy_log="${NVIDIA_ROUTER_XK_PROXY_LIVE_LOG_PATH:-}"
started="$(date +%s%3N)"
status=PASS
for ((index = 1; index <= repeat; index++)); do
  if ! NVIDIA_ROUTER_LIVE_SELF_TEST=0 bash scripts/test/live-nvidia.sh >"$live_log" 2>&1; then
    status=FAIL
    break
  fi
  if ! python3 - "$live_log" <<'PY'
import re
import sys

with open(sys.argv[1], "r", encoding="utf-8", errors="replace") as stream:
    value = stream.read()
if re.search(r"apikey|sign|xkdaili|nvapi-|nvr_", value, re.IGNORECASE):
    raise SystemExit(1)
PY
  then
    status=FAIL
    break
  fi
done
rm -f -- "$live_log"
live_log=""

if [[ "$status" != PASS ]]; then
  report 'ProxyHotRequests' FAIL "$started"
  exit 1
fi
report 'ProxyHotRequests' PASS "$started"

if [[ -z "$proxy_log" ]]; then
  printf 'case=ProxyLeaseMetrics status=BLOCKED duration_ms=0\n' >&2
  exit 2
fi
if [[ ! -f "$proxy_log" || -L "$proxy_log" ]]; then
  printf 'case=ProxyLeaseMetrics status=FAIL duration_ms=0\n' >&2
  exit 1
fi

metrics="$(python3 - "$proxy_log" <<'PY'
import re
import sys

acquired = 0
retired = 0
served_requests = 0
reuse_hits = 0
with open(sys.argv[1], "r", encoding="utf-8", errors="replace") as stream:
    for line in stream:
        if "proxy_lease_acquired" in line:
            acquired += 1
        if "proxy_lease_retired" in line:
            retired += 1
            served = re.search(r"served_requests[=:](\d+)", line)
            reused = re.search(r"reuse_hits[=:](\d+)", line)
            served_requests += int(served.group(1)) if served else 0
            reuse_hits += int(reused.group(1)) if reused else 0
if acquired < 1:
    raise SystemExit(1)
print(f"fetch_count={acquired} retired_count={retired} served_requests={served_requests} reuse_hits={reuse_hits}")
PY
)" || {
  printf 'case=ProxyLeaseMetrics status=FAIL duration_ms=0\n' >&2
  exit 1
}
printf 'case=ProxyLeaseMetrics status=PASS %s duration_ms=0\n' "$metrics"
