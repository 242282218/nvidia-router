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

compose() {
  env -u COMPOSE_FILE -u COMPOSE_PROJECT_NAME NVIDIA_ROUTER_IMAGE="$NVIDIA_ROUTER_IMAGE" \
    docker compose --project-directory "$PWD" -p "$COMPOSE_PROJECT" -f docker-compose.yml "$@"
}

compose_config="$(mktemp "${TMPDIR:-/tmp}/nvidia-router-rollback.XXXXXX.json")"
trap 'rm -f -- "$compose_config"' EXIT
env -u COMPOSE_FILE -u COMPOSE_PROJECT_NAME NVIDIA_ROUTER_IMAGE="$NVIDIA_ROUTER_IMAGE" \
  docker compose --project-directory "$PWD" -p "$COMPOSE_PROJECT" -f docker-compose.yml config --format json >"$compose_config"

echo "==> verify image $NVIDIA_ROUTER_IMAGE"
if ! docker image inspect "$NVIDIA_ROUTER_IMAGE" >/dev/null 2>&1; then
  echo "error: image $NVIDIA_ROUTER_IMAGE is not available; refusing to stop the running router" >&2
  exit 1
fi

echo "==> 恢复数据卷"
BACKUP_DIR="backups"
LATEST=""
while IFS= read -r candidate; do
  if [[ -f "$candidate/router.db" ]]; then
    LATEST="$candidate"
    break
  fi
done < <(find "$BACKUP_DIR" -mindepth 1 -maxdepth 1 -type d -printf '%T@ %p\n' | sort -nr | cut -d' ' -f2-)
if [[ -z "$LATEST" ]]; then
  echo "error: no complete database backup found" >&2
  exit 1
fi
data_volume="$(docker volume ls -q --filter label=com.docker.compose.project=$COMPOSE_PROJECT --filter label=com.docker.compose.volume=nvidia-router-data | head -n 1)"
if [[ -z "$data_volume" ]]; then
  echo "error: cannot derive the Compose data volume" >&2
  exit 1
fi
compose stop app
litestream_containers="$(docker ps -q \
  --filter "label=com.docker.compose.project=$COMPOSE_PROJECT" \
  --filter "label=com.docker.compose.service=litestream" \
  --filter status=running)"
if [[ -n "$litestream_containers" ]]; then
  docker stop $litestream_containers
fi
running_containers="$(docker ps -q \
  --filter "label=com.docker.compose.project=$COMPOSE_PROJECT" \
  --filter status=running)"
if [[ -n "$running_containers" ]]; then
  echo "error: Compose project still has running containers; refusing to restore the database" >&2
  exit 1
fi
echo "  restore data from $(basename "$LATEST")"
docker run --rm -v "$data_volume:/data" -v "$PWD/$LATEST:/backup" alpine sh -c \
  '
    set -eu
    staged=""
    previous=""
    previous_ready=0
    cleanup() {
      if [ -n "$staged" ]; then rm -f "$staged"; fi
      if [ "$previous_ready" -eq 0 ] && [ -n "$previous" ]; then rm -f "$previous"; fi
    }
    trap cleanup EXIT
    test -s /backup/router.db
    staged=$(mktemp /data/.router.db.restore.XXXXXX)
    cp /backup/router.db "$staged"
    test -s "$staged"
    chown 10001:10001 "$staged"
    if [ -e /data/router.db ]; then
      previous=$(mktemp /data/.router.db.previous.XXXXXX)
      cp -p /data/router.db "$previous"
      test -s "$previous"
      chown 10001:10001 "$previous"
      previous_ready=1
    fi
    rm -f /data/router.db-wal /data/router.db-shm
    mv "$staged" /data/router.db
    staged=""
    if [ -n "$previous" ]; then
      echo "  previous database retained at $previous"
    fi
  '

echo "==> 用镜像标签 $TAG 重启"
compose up -d --no-build --force-recreate

echo "==> 验证"
curl -fsS --max-time 5 http://127.0.0.1:3756/health/live && echo "  <- /health/live"

echo "回滚完成"
