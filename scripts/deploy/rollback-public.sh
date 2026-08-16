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

compose_config=""
app_stopped=0
current_image=""
data_volume=""
database_swap_started=0

restore_previous_database() {
  if [[ -z "$data_volume" || -z "$current_image" ]]; then
    return 1
  fi
  if ! docker run --rm -v "$data_volume:/data" alpine sh -c '
    set -eu
    source=/data/.router.db.previous.db
    stage=/data/.router-db-recovery
    test -s "$source"
    if [ -e "$stage" ]; then
      echo "error: database recovery staging directory already exists" >&2
      exit 1
    fi
    mkdir "$stage"
    cp "$source" "$stage/router.db"
    chown 10001:10001 "$stage/router.db"
    chmod 600 "$stage/router.db"
  '; then
    return 1
  fi
  if ! docker run --rm --user 10001:10001 \
    -e NVIDIA_ROUTER_DATA_DIR=/data/.router-db-recovery \
    -v "$data_volume:/data" "$current_image" \
    db backup --output /data/.router-db-recovery/validated.db; then
    return 1
  fi
  if ! docker run --rm -v "$data_volume:/data" alpine sh -c '
    set -eu
    stage=/data/.router-db-recovery
    test -s "$stage/validated.db"
    rm -f /data/router.db-wal /data/router.db-shm /data/router.db-journal /data/.router.db.lock
    mv "$stage/validated.db" /data/router.db
    chown 10001:10001 /data/router.db
    chmod 600 /data/router.db
    rm -f "$stage/router.db" "$stage/router.db-wal" "$stage/router.db-shm" "$stage/router.db-journal" "$stage/.router.db.lock"
    rmdir "$stage"
  '; then
    return 1
  fi
}

wait_for_health() {
  for attempt in {1..30}; do
    if curl -fsS --max-time 5 http://127.0.0.1:3756/health/live >/dev/null \
      && curl -fsS --max-time 5 http://127.0.0.1:3756/health/ready >/dev/null; then
      return 0
    fi
    sleep 1
  done
  return 1
}

recover_on_exit() {
  local status=$?
  if [[ "$status" -ne 0 && "$app_stopped" -eq 1 ]]; then
    echo "error: rollback failed; attempting to restore the running app" >&2
    recovery_failed=0
    if ! compose stop app >/dev/null 2>&1; then
      recovery_failed=1
      echo "error: failed to stop the app during rollback recovery" >&2
    fi
    recovery_litestream=""
    if ! recovery_litestream="$(docker ps -q \
      --filter "label=com.docker.compose.project=$COMPOSE_PROJECT" \
      --filter "label=com.docker.compose.service=litestream" \
      --filter status=running 2>/dev/null)"; then
      recovery_failed=1
      echo "error: failed to inspect the Litestream container during rollback recovery" >&2
    fi
    if [[ -n "$recovery_litestream" ]]; then
      if ! docker stop "$recovery_litestream" >/dev/null 2>&1; then
        recovery_failed=1
        echo "error: failed to stop Litestream during rollback recovery" >&2
      fi
    fi
    if [[ "$database_swap_started" -eq 1 ]] && ! restore_previous_database; then
      recovery_failed=1
      echo "error: failed to restore the previous database; app remains stopped" >&2
    fi
    if [[ "$recovery_failed" -eq 0 && -n "$current_image" ]]; then
      if ! NVIDIA_ROUTER_IMAGE="$current_image" compose up -d --no-build --force-recreate; then
        echo "error: failed to restore the previous app automatically" >&2
      elif ! wait_for_health; then
        echo "error: previous app started but did not become ready" >&2
      fi
    elif [[ "$recovery_failed" -eq 0 ]]; then
      echo "error: previous app image is unavailable; app remains stopped" >&2
    fi
  fi
  if [[ -n "$compose_config" ]]; then
    rm -f -- "$compose_config"
  fi
  exit "$status"
}

trap recover_on_exit EXIT

compose_config="$(mktemp "${TMPDIR:-/tmp}/nvidia-router-rollback.XXXXXX.json")"
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
current_image="$(docker inspect --format '{{.Config.Image}}' "$app_container")"
if [[ -z "$current_image" ]]; then
  echo "error: running app image name is unavailable; refusing to restore the database" >&2
  exit 1
fi
if ! docker image inspect "$current_image" >/dev/null 2>&1; then
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
app_stopped=1
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

database_swap_started=1
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
if ! wait_for_health; then
  echo "error: rolled-back router did not become ready" >&2
  exit 1
fi
app_stopped=0
echo "  /health/live and /health/ready passed"

echo "回滚完成"
