#!/usr/bin/env bash
# Build and deploy the single-container router with its built-in proxy pool.
set -euo pipefail
cd "$(dirname "$0")/../.."
TAG="${1:-deploy-$(date +%Y%m%d-%H%M%S)}"
if [[ "${1:-}" == "--no-build" ]]; then TAG="${2:-deploy}"; fi

echo "==> build nvidia-router:$TAG"
docker build -t "nvidia-router:$TAG" .

if [[ ! -f .env ]]; then
  echo "error: .env is required; inject XApi credentials at runtime" >&2
  exit 1
fi

echo "==> validate Compose configuration"
compose_config="$(mktemp "${TMPDIR:-/tmp}/nvidia-router-compose.XXXXXX.json")"
trap 'rm -f -- "$compose_config"' EXIT
env -u COMPOSE_FILE -u COMPOSE_PROJECT_NAME docker compose --project-directory "$PWD" -p nvidia-router -f docker-compose.yml -f docker-compose.public.yml config --format json >"$compose_config"

echo "==> backup existing router data"
backup_dir="backups/$(date +%Y%m%d-%H%M%S)"
mkdir -p "$backup_dir"
if docker volume inspect nvr-data >/dev/null 2>&1; then
  docker run --rm -v nvr-data:/data -v "$PWD/$backup_dir:/backup" alpine tar czf "/backup/nvr-data.tar.gz" -C /data . || true
fi

echo "==> start single-container router"
env -u COMPOSE_FILE -u COMPOSE_PROJECT_NAME docker compose --project-directory "$PWD" -p nvidia-router -f docker-compose.yml -f docker-compose.public.yml up -d --build

echo "==> verify liveness"
sleep 5
curl -fsS --max-time 5 http://127.0.0.1:3756/health/live
echo
echo "deployed single-container router on :3756 ($TAG)"
