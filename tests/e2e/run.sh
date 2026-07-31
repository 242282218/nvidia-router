#!/usr/bin/env bash
set -Eeuo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
HARNESS_LOG="$(mktemp)"
HARNESS_PID=""

cleanup() {
  status=$?
  if [[ -n "${HARNESS_PID}" ]] && kill -0 "${HARNESS_PID}" 2>/dev/null; then
    kill -TERM "${HARNESS_PID}" 2>/dev/null || true
    for _ in {1..50}; do
      if ! kill -0 "${HARNESS_PID}" 2>/dev/null; then
        break
      fi
      sleep 0.1
    done
    kill -KILL "${HARNESS_PID}" 2>/dev/null || true
  fi
  rm -f "${HARNESS_LOG}"
  exit "${status}"
}
trap cleanup EXIT INT TERM

cd "${ROOT_DIR}"
pnpm --dir web run build

# The E2E specs live outside the web workspace package. Keep their imports
# package-based while exposing the workspace's installed Playwright module.
export NODE_PATH="${ROOT_DIR}/web/node_modules${NODE_PATH:+:${NODE_PATH}}"

go run ./tests/e2e/harness >"${HARNESS_LOG}" 2> >(tee /dev/stderr >&2) &
HARNESS_PID=$!

for _ in {1..100}; do
  if [[ -s "${HARNESS_LOG}" ]]; then
    break
  fi
  if ! kill -0 "${HARNESS_PID}" 2>/dev/null; then
    printf 'E2E harness exited before printing its URL.\n' >&2
    exit 1
  fi
  sleep 0.1
done

IFS= read -r BASE_URL < "${HARNESS_LOG}"
if [[ -z "${BASE_URL}" ]]; then
  printf 'E2E harness did not provide a base URL.\n' >&2
  exit 1
fi

for _ in {1..100}; do
  if curl --fail --silent "${BASE_URL}/health/live" >/dev/null; then
    break
  fi
  sleep 0.1
done
curl --fail --silent "${BASE_URL}/health/live" >/dev/null

PLAYWRIGHT_BASE_URL="${BASE_URL}" pnpm --dir web run e2e
