#!/usr/bin/env bash
# Build and deploy the single-container router with its built-in proxy pool.
set -euo pipefail
cd "$(dirname "$0")/../.."
TAG="${1:-deploy-$(date +%Y%m%d-%H%M%S)}"
export NVIDIA_ROUTER_IMAGE="${NVIDIA_ROUTER_IMAGE:-nvidia-router:$TAG}"

compose() {
  env -u COMPOSE_FILE -u COMPOSE_PROJECT_NAME NVIDIA_ROUTER_IMAGE="$NVIDIA_ROUTER_IMAGE" \
    docker compose --project-directory "$PWD" -p nvidia-router -f docker-compose.yml "$@"
}

if [[ ! -f .env ]]; then
  echo "error: .env is required; inject XApi credentials at runtime" >&2
  exit 1
fi

echo "==> validate Compose configuration"
compose_config="$(mktemp "${TMPDIR:-/tmp}/nvidia-router-compose.XXXXXX.json")"
trap 'rm -f -- "$compose_config"' EXIT
env -u COMPOSE_FILE -u COMPOSE_PROJECT_NAME docker compose --project-directory "$PWD" -p nvidia-router -f docker-compose.yml config --format json >"$compose_config"

echo "==> verify image $NVIDIA_ROUTER_IMAGE"
if ! docker image inspect "$NVIDIA_ROUTER_IMAGE" >/dev/null 2>&1; then
  echo "error: image $NVIDIA_ROUTER_IMAGE is not available; refusing to stop the running router" >&2
  exit 1
fi

echo "==> backup existing router data"
data_volume="$(docker volume ls -q --filter label=com.docker.compose.project=nvidia-router --filter label=com.docker.compose.volume=nvidia-router-data | head -n 1)"
if [[ -n "$data_volume" ]]; then
  backup_dir="backups/$(date +%Y%m%d-%H%M%S)"
  mkdir -p "$backup_dir"
  compose stop app
  compose run --rm --no-deps --user "$(id -u):$(id -g)" -v "$PWD/$backup_dir:/data-backups" app \
    db backup --output "/data-backups/router.db"
else
  echo "  no existing data volume; skipping backup"
fi

echo "==> start single-container router"
compose up -d --no-build

echo "==> verify liveness"
sleep 5
curl -fsS --max-time 5 http://127.0.0.1:3756/health/live
echo
echo "deployed single-container router on :3756 ($TAG)"
