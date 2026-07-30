#!/usr/bin/env bash

set -euo pipefail

readonly allowed_nvidia_key='nvapi-fixture-not-a-real-key-123456789'
readonly secret_pattern='\bnvapi-[A-Za-z0-9_-]{32,}|\bnvr_[A-Za-z0-9_-]{43}'

repo_root="$(git rev-parse --show-toplevel)"
cd "$repo_root"

tracked_list="$(mktemp)"
trap 'rm -f "$tracked_list"' EXIT
git ls-files -z >"$tracked_list"
mapfile -d '' -t tracked_files <"$tracked_list"

if (( ${#tracked_files[@]} == 0 )); then
  printf 'No tracked files to scan.\n'
  exit 0
fi

if matches="$(rg --only-matching --no-filename --no-line-number --text -- "$secret_pattern" "${tracked_files[@]}")"; then
  while IFS= read -r candidate; do
    if [[ "$candidate" == "$allowed_nvidia_key" ]]; then
      continue
    fi
    printf 'Potential NVIDIA or access key secret found in tracked files.\n' >&2
    exit 1
  done <<<"$matches"
else
  status=$?
  if (( status != 1 )); then
    printf 'Failed to scan tracked files.\n' >&2
    exit "$status"
  fi
fi

printf 'No NVIDIA or access key secrets found in tracked files.\n'
