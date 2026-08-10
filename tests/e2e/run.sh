#!/usr/bin/env bash
set -Eeuo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
HARNESS_TMP_DIR="$(mktemp -d)"
HARNESS_BIN="${HARNESS_TMP_DIR}/nvidia-router-e2e-harness"
ARTIFACT_DIR="${E2E_ARTIFACT_DIR:-${ROOT_DIR}/web/test-results}"
HARNESS_LOG="${HARNESS_TMP_DIR}/harness.log"
HARNESS_ARTIFACT_LOG="${ARTIFACT_DIR}/harness.log"
HARNESS_PID=""
mkdir -p "${ARTIFACT_DIR}"

print_harness_log() {
  if [[ -f "${HARNESS_LOG}" ]]; then
    printf '%s\n' '--- E2E harness log ---' >&2
    while IFS= read -r line; do
      printf '%s\n' "${line}" >&2
    done < "${HARNESS_LOG}"
    printf '%s\n' '--- end E2E harness log ---' >&2
  fi
}

cleanup() {
  status=$?
  trap - EXIT INT TERM
  if [[ -n "${HARNESS_PID}" ]] && kill -0 "${HARNESS_PID}" 2>/dev/null; then
    kill -TERM -- "-${HARNESS_PID}" 2>/dev/null || kill -TERM "${HARNESS_PID}" 2>/dev/null || true
    for _ in {1..50}; do
      if ! kill -0 "${HARNESS_PID}" 2>/dev/null; then
        break
      fi
      sleep 0.1
    done
    if kill -0 "${HARNESS_PID}" 2>/dev/null; then
      printf 'E2E harness did not stop after SIGTERM; forcing cleanup.\n' >&2
      kill -KILL -- "-${HARNESS_PID}" 2>/dev/null || kill -KILL "${HARNESS_PID}" 2>/dev/null || true
    fi
    wait "${HARNESS_PID}" 2>/dev/null || true
  fi
  if (( status != 0 )); then
    print_harness_log
  fi
  if [[ -f "${HARNESS_LOG}" ]]; then
    cp "${HARNESS_LOG}" "${HARNESS_ARTIFACT_LOG}"
  fi
  rm -rf "${HARNESS_TMP_DIR}"
  exit "${status}"
}
trap cleanup EXIT INT TERM

cd "${ROOT_DIR}"
pnpm --dir web run build
go build -o "${HARNESS_BIN}" ./tests/e2e/harness

# The E2E specs live outside the web workspace package. Keep their imports
# package-based while exposing the workspace's installed Playwright module.
export NODE_PATH="${ROOT_DIR}/web/node_modules${NODE_PATH:+:${NODE_PATH}}"

if command -v setsid >/dev/null 2>&1; then
  setsid "${HARNESS_BIN}" >"${HARNESS_LOG}" 2>&1 &
else
  # Git Bash on Windows may not ship setsid; the harness has its own signal
  # handler, so a direct child is sufficient when process groups are unavailable.
  "${HARNESS_BIN}" >"${HARNESS_LOG}" 2>&1 &
fi
HARNESS_PID=$!

BASE_URL=""
for _ in {1..100}; do
  if ! kill -0 "${HARNESS_PID}" 2>/dev/null; then
    printf 'E2E harness exited before printing its URL.\n' >&2
    exit 1
  fi
  if [[ -s "${HARNESS_LOG}" ]]; then
    IFS= read -r BASE_URL < "${HARNESS_LOG}" || true
    if [[ -n "${BASE_URL}" ]]; then
      break
    fi
  fi
  sleep 0.1
done
if [[ -z "${BASE_URL}" ]]; then
  printf 'E2E harness did not provide a base URL.\n' >&2
  print_harness_log
  exit 1
fi

for _ in {1..100}; do
  if ! kill -0 "${HARNESS_PID}" 2>/dev/null; then
    printf 'E2E harness exited while waiting for health.\n' >&2
    print_harness_log
    exit 1
  fi
  if curl --fail --silent "${BASE_URL}/health/live" >/dev/null; then
    break
  fi
  sleep 0.1
done
if ! kill -0 "${HARNESS_PID}" 2>/dev/null; then
  printf 'E2E harness exited before health became ready.\n' >&2
  print_harness_log
  exit 1
fi
if ! curl --fail --silent "${BASE_URL}/health/live" >/dev/null; then
  printf 'E2E harness health check failed.\n' >&2
  print_harness_log
  exit 1
fi

PLAYWRIGHT_BASE_URL="${BASE_URL}" pnpm --dir web run e2e
