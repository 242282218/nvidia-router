#!/usr/bin/env bash
set -Eeuo pipefail

project="nvr-acceptance-$$"

cleanup() {
  local status=$?

  if (( status != 0 )); then
    docker compose -p "$project" ps >&2 || true
    docker compose -p "$project" logs --tail 100 >&2 || true
  fi

  docker compose -p "$project" down -v --remove-orphans || true
  return "$status"
}

trap cleanup EXIT

export NVIDIA_ROUTER_MASTER_KEY="$(openssl rand -base64 32 | tr '+/' '-_' | tr -d '=')"
docker compose -p "$project" config >/dev/null
docker compose -p "$project" build
docker compose -p "$project" up -d --wait
curl --fail --silent --show-error http://127.0.0.1:3756/health/live >/dev/null

ready_status="$(curl --silent --show-error --output /dev/null --write-out '%{http_code}' http://127.0.0.1:3756/health/ready)"
if [[ "$ready_status" == "200" ]]; then
  echo 'ready unexpectedly passed before the initial password change' >&2
  exit 1
fi
