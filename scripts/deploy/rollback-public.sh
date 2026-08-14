#!/usr/bin/env bash
# 回滚 hangzhou2-2 上的公网双服务到备份镜像/数据。
#
# 用法：./scripts/deploy/rollback-public.sh <deploy-tag>
# 例如：./scripts/deploy/rollback-public.sh deploy-20260810-204300
#
# 回滚镜像标签为传入的 <deploy-tag>，并从 backups/<时间>/ 恢复数据卷。

set -euo pipefail

cd "$(dirname "$0")/../.."

TAG="${1:?用法: rollback-public.sh <deploy-tag>}"
COMPOSE_PROJECT="nvidia-router"
export NVIDIA_ROUTER_IMAGE="${NVIDIA_ROUTER_IMAGE:-nvidia-router:$TAG}"

compose_config="$(mktemp "${TMPDIR:-/tmp}/nvidia-router-rollback.XXXXXX.json")"
trap 'rm -f -- "$compose_config"' EXIT
env -u COMPOSE_FILE -u COMPOSE_PROJECT_NAME NVIDIA_ROUTER_IMAGE="$NVIDIA_ROUTER_IMAGE" \
  docker compose --project-directory "$PWD" -p "$COMPOSE_PROJECT" -f docker-compose.yml config --format json >"$compose_config"

echo "==> 恢复数据卷"
BACKUP_DIR="backups"
LATEST="$(find "$BACKUP_DIR" -mindepth 1 -maxdepth 1 -type d -printf '%T@ %p\n' | sort -nr | head -n 1 | cut -d' ' -f2-)"
if [[ -z "$LATEST" || ! -f "$LATEST/data.tar.gz" ]]; then
  echo "error: no complete data backup found" >&2
  exit 1
fi
data_volume="$(docker volume ls -q --filter label=com.docker.compose.project=$COMPOSE_PROJECT --filter label=com.docker.compose.volume=nvidia-router-data | head -n 1)"
if [[ -z "$data_volume" ]]; then
  echo "error: cannot derive the Compose data volume" >&2
  exit 1
fi
echo "  restore data from $(basename "$LATEST")"
docker run --rm -v "$data_volume:/data" -v "$PWD/$LATEST:/backup" alpine sh -c \
  'rm -rf /data/* && tar xzf /backup/data.tar.gz -C /data'

echo "==> 用镜像标签 $TAG 重启"
env -u COMPOSE_FILE -u COMPOSE_PROJECT_NAME NVIDIA_ROUTER_IMAGE="$NVIDIA_ROUTER_IMAGE" \
  docker compose \
    --project-directory "$PWD" \
    -p "$COMPOSE_PROJECT" \
    -f docker-compose.yml \
    up -d --no-build --force-recreate

echo "==> 验证"
curl -fsS --max-time 5 http://127.0.0.1:3756/health/live && echo "  <- /health/live"

echo "回滚完成"
