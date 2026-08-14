#!/usr/bin/env bash
# Build and deploy the single-container router with its built-in proxy pool.
set -euo pipefail
cd "$(dirname "$0")/../.."
TAG="${1:-deploy-$(date +%Y%m%d-%H%M%S)}"
export NVIDIA_ROUTER_IMAGE="${NVIDIA_ROUTER_IMAGE:-nvidia-router:$TAG}"

if [[ ! -f .env ]]; then
  echo "error: .env is required; inject XApi credentials at runtime" >&2
  exit 1
fi

echo "==> validate Compose configuration"
compose_config="$(mktemp "${TMPDIR:-/tmp}/nvidia-router-compose.XXXXXX.json")"
trap 'rm -f -- "$compose_config"' EXIT
env -u COMPOSE_FILE -u COMPOSE_PROJECT_NAME docker compose --project-directory "$PWD" -p nvidia-router -f docker-compose.yml config --format json >"$compose_config"

echo "==> backup existing router data"
backup_dir="backups/$(date +%Y%m%d-%H%M%S)"
mkdir -p "$backup_dir"
data_volume="$(docker volume ls -q --filter label=com.docker.compose.project=nvidia-router --filter label=com.docker.compose.volume=nvidia-router-data | head -n 1)"
if [[ -z "$data_volume" ]]; then
  echo "error: cannot derive the Compose data volume" >&2
  exit 1
fi
docker run --rm -v "$data_volume:/data:ro" -v "$PWD/$backup_dir:/backup" alpine tar czf "/backup/data.tar.gz" -C /data .

echo "==> start single-container router"
env -u COMPOSE_FILE -u COMPOSE_PROJECT_NAME NVIDIA_ROUTER_IMAGE="$NVIDIA_ROUTER_IMAGE" docker compose --project-directory "$PWD" -p nvidia-router -f docker-compose.yml up -d --no-build

echo "==> verify liveness"
sleep 5
curl -fsS --max-time 5 http://127.0.0.1:3756/health/live
echo
echo "deployed single-container router on :3756 ($TAG)"
