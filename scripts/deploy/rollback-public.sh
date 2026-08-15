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
    docker compose --project-directory "$PWD" -p "$COMPOSE_PROJECT" \
      -f docker-compose.yml -f docker-compose.public.yml "$@"
}

compose_config="$(mktemp "${TMPDIR:-/tmp}/nvidia-router-rollback.XXXXXX.json")"
trap 'rm -f -- "$compose_config"' EXIT
env -u COMPOSE_FILE -u COMPOSE_PROJECT_NAME NVIDIA_ROUTER_IMAGE="$NVIDIA_ROUTER_IMAGE" \
  docker compose --project-directory "$PWD" -p "$COMPOSE_PROJECT" \
    -f docker-compose.yml -f docker-compose.public.yml config --format json >"$compose_config"

echo "==> verify image $NVIDIA_ROUTER_IMAGE"
if ! docker image inspect "$NVIDIA_ROUTER_IMAGE" >/dev/null 2>&1; then
  echo "error: image $NVIDIA_ROUTER_IMAGE is not available; refusing to stop the running router" >&2
  exit 1
fi

app_container="$(compose ps -q app)"
if [[ -z "$app_container" ]]; then
  echo "error: cannot identify the running app container; refusing to restore the database" >&2
  exit 1
fi
if [[ "$(docker inspect --format '{{.State.Running}}' "$app_container")" != "true" ]]; then
  echo "error: app container is not running; refusing to create a previous database snapshot" >&2
  exit 1
fi
current_image="$(docker inspect --format '{{.Image}}' "$app_container")"
if [[ -z "$current_image" ]] || ! docker image inspect "$current_image" >/dev/null 2>&1; then
  echo "error: running app image is not available; refusing to restore the database" >&2
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
data_mountpoint="$(docker volume inspect --format '{{.Mountpoint}}' "$data_volume")"
all_containers="$(docker ps -aq)"
while IFS= read -r container_id; do
  [[ -z "$container_id" ]] && continue
  container_status="$(docker inspect --format '{{.State.Status}}' "$container_id")"
  if [[ "$container_status" != "running" && "$container_status" != "paused" ]]; then
    continue
  fi
  mounted_names="$(docker inspect --format '{{range .Mounts}}{{if .Name}}{{println .Name}}{{end}}{{end}}' "$container_id")"
  mounted_sources="$(docker inspect --format '{{range .Mounts}}{{if .Source}}{{println .Source}}{{end}}{{end}}' "$container_id")"
  shared_mount=0
  while IFS= read -r mounted_name; do
    if [[ "$mounted_name" == "$data_volume" ]]; then
      shared_mount=1
    fi
  done <<< "$mounted_names"
  while IFS= read -r mounted_source; do
    if [[ "$mounted_source" == "$data_mountpoint" || "$mounted_source" == "$data_mountpoint/"* ]]; then
      shared_mount=1
    fi
  done <<< "$mounted_sources"
  if [[ "$shared_mount" -eq 1 ]]; then
    container_name="$(docker inspect --format '{{.Name}}' "$container_id")"
    echo "error: $container_status container $container_name still mounts $data_volume; refusing to restore the database" >&2
    exit 1
  fi
done <<< "$all_containers"
echo "  restore data from $(basename "$LATEST")"
echo "  preserve current database with the running image"
docker run --rm --user 10001:10001 -e NVIDIA_ROUTER_DATA_DIR=/data \
  -v "$data_volume:/data" "$current_image" \
  db backup --output /data/.router.db.previous.db
docker run --rm -v "$data_volume:/data" -v "$PWD/$LATEST:/backup:ro" alpine sh -c \
  '
    set -eu
    test -s /backup/router.db
    stage=/data/.router-db-rollback
    if [ -e "$stage" ]; then
      echo "error: rollback staging directory already exists" >&2
      exit 1
    fi
    mkdir "$stage"
    chown 10001:10001 "$stage"
    chmod 700 "$stage"
    cp /backup/router.db "$stage/router.db"
    test -s "$stage/router.db"
    chown 10001:10001 "$stage/router.db"
  '

docker run --rm --user 10001:10001 \
  -e NVIDIA_ROUTER_DATA_DIR=/data/.router-db-rollback \
  -v "$data_volume:/data" "$NVIDIA_ROUTER_IMAGE" \
  db backup --output /data/.router-db-rollback/validated.db

docker run --rm -v "$data_volume:/data" alpine sh -c \
  '
    set -eu
    stage=/data/.router-db-rollback
    validated=/data/.router.db.rollback.validated
    old_wal=/data/.router.db.rollback.old-wal
    old_shm=/data/.router.db.rollback.old-shm
    old_journal=/data/.router.db.rollback.old-journal
    quarantine_failed=0
    cleanup_failed=0
    if [ -e "$validated" ]; then
      echo "error: validated rollback database already exists" >&2
      exit 1
    fi
    if [ -e "$old_wal" ] || [ -e "$old_shm" ] || [ -e "$old_journal" ]; then
      echo "error: old SQLite sidecar quarantine already exists" >&2
      exit 1
    fi
    test -s "$stage/validated.db"
    mv "$stage/validated.db" "$validated"
    rm -f "$stage/router.db" "$stage/router.db-wal" "$stage/router.db-shm" "$stage/router.db-journal" "$stage/.router.db.lock"
    rmdir "$stage"

    # Keep the old main file and its sidecars intact until this atomic swap succeeds.
    mv "$validated" /data/router.db

    if [ -e /data/router.db-wal ]; then
      if ! mv /data/router.db-wal "$old_wal"; then
        quarantine_failed=1
        echo "error: quarantine old SQLite WAL failed" >&2
      fi
    fi
    if [ -e /data/router.db-shm ]; then
      if ! mv /data/router.db-shm "$old_shm"; then
        quarantine_failed=1
        echo "error: quarantine old SQLite shared memory failed" >&2
      fi
    fi
    if [ -e /data/router.db-journal ]; then
      if ! mv /data/router.db-journal "$old_journal"; then
        quarantine_failed=1
        echo "error: quarantine old SQLite journal failed" >&2
      fi
    fi
    if [ "$quarantine_failed" -ne 0 ]; then
      echo "error: old SQLite sidecars remain attached; refusing to start the router" >&2
      exit 1
    fi
    for sidecar in "$old_wal" "$old_shm" "$old_journal"; do
      if [ -e "$sidecar" ] && ! rm -f "$sidecar"; then
        cleanup_failed=1
        echo "warning: retain old SQLite sidecar outside active database: $sidecar" >&2
      fi
    done
    if [ "$cleanup_failed" -ne 0 ]; then
      echo "warning: rollback completed with quarantined old SQLite sidecars" >&2
    fi
  '

echo "==> 用镜像标签 $TAG 重启"
compose up -d --no-build --force-recreate

echo "==> 验证"
curl -fsS --max-time 5 http://127.0.0.1:3756/health/live && echo "  <- /health/live"

echo "回滚完成"
