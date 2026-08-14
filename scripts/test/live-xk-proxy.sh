#!/usr/bin/env bash

# Work-scene smoke test for the NVIDIA router through the Xingkong proxy pool.
# The NVIDIA key is read from a local ignored file and is never echoed, logged,
# persisted, or passed as a command-line argument. Admin credentials remain
# runtime-only environment variables.
set -euo pipefail
umask 077

repo_root="$(git rev-parse --show-toplevel 2>/dev/null || cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
live_key="${NVIDIA_ROUTER_LIVE_KEY:-}"
if [[ -z "$live_key" ]]; then
  key_file="${NVIDIA_ROUTER_KEY_FILE:-$repo_root/key/key.txt}"
  if [[ ! -f "$key_file" || ! -r "$key_file" ]]; then
    printf 'key file is missing or unreadable\n' >&2
    exit 1
  fi
  while IFS= read -r candidate || [[ -n "$candidate" ]]; do
    candidate="${candidate%$'\r'}"
    if [[ -n "$candidate" ]]; then
      live_key="$candidate"
      break
    fi
  done < "$key_file"
fi
if [[ -z "$live_key" ]]; then
  printf 'key file contains no usable key\n' >&2
  exit 1
fi

export NVIDIA_ROUTER_LIVE_REQUIRE_XK_PROXY=1
export NVIDIA_ROUTER_LIVE_REQUIRE_XK_MODE=built-in
export NVIDIA_ROUTER_LIVE_KEY="$live_key"
unset live_key

# The existing live suite performs the authenticated request lifecycle and
# cleans up temporary Access Keys, imported keys, and the admin session.
exec "$repo_root/scripts/test/live-nvidia.sh"
