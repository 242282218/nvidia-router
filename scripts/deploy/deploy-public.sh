#!/usr/bin/env bash
# Build and deploy the single-container router with its built-in proxy pool.
set -euo pipefail
cd "$(dirname "$0")/../.."
TAG="${1:-deploy-$(date +%Y%m%d-%H%M%S)}"
export NVIDIA_ROUTER_IMAGE="${NVIDIA_ROUTER_IMAGE:-nvidia-router:$TAG}"

compose() {
  env -u COMPOSE_FILE -u COMPOSE_PROJECT_NAME NVIDIA_ROUTER_IMAGE="$NVIDIA_ROUTER_IMAGE" \
    docker compose --project-directory "$PWD" -p nvidia-router \
      -f docker-compose.yml -f docker-compose.public.yml "$@"
}

compose_config=""
app_stopped=0
previous_image=""
require_ready=0
data_volume=""
backup_dir=""
database_migration_started=0

restore_deployment_database() {
  if [[ -z "$data_volume" || -z "$previous_image" || -z "$backup_dir" ]]; then
    return 1
  fi
  if ! docker run --rm -v "$data_volume:/data" -v "$PWD/$backup_dir:/backup:ro" alpine sh -c '
    set -eu
    source=/backup/router.db
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
    -v "$data_volume:/data" "$previous_image" \
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
  local ready_required="$1"
  for attempt in {1..30}; do
    if curl -fsS --max-time 5 http://127.0.0.1:3756/health/live >/dev/null \
      && { [[ "$ready_required" -eq 0 ]] || curl -fsS --max-time 5 http://127.0.0.1:3756/health/ready >/dev/null; }; then
      return 0
    fi
    sleep 1
  done
  return 1
}

recover_on_exit() {
  local status=$?
  if [[ "$status" -ne 0 && "$app_stopped" -eq 1 ]]; then
    echo "error: deployment failed; attempting to restore the running app" >&2
    compose stop app >/dev/null 2>&1 || true
    recovery_failed=0
    if [[ "$database_migration_started" -eq 1 ]] && ! restore_deployment_database; then
      recovery_failed=1
      echo "error: failed to restore the previous database; app remains stopped" >&2
    fi
    if [[ "$recovery_failed" -eq 0 && -n "$previous_image" ]]; then
      if ! NVIDIA_ROUTER_IMAGE="$previous_image" compose up -d --no-build; then
        echo "error: failed to restore the previous app automatically" >&2
      elif ! wait_for_health 1; then
        echo "error: previous app started but did not become ready" >&2
      fi
    elif [[ "$recovery_failed" -eq 0 ]]; then
      echo "error: failed to restore the running app automatically" >&2
    fi
  fi
  if [[ -n "$compose_config" ]]; then
    rm -f -- "$compose_config"
  fi
  exit "$status"
}

trap recover_on_exit EXIT

if [[ ! -f .env ]]; then
  echo "error: .env is required; inject XApi credentials at runtime" >&2
  exit 1
fi

echo "==> validate Compose configuration"
compose_config="$(mktemp "${TMPDIR:-/tmp}/nvidia-router-compose.XXXXXX.json")"
env -u COMPOSE_FILE -u COMPOSE_PROJECT_NAME docker compose \
  --project-directory "$PWD" -p nvidia-router \
  -f docker-compose.yml -f docker-compose.public.yml config --format json >"$compose_config"

echo "==> verify image $NVIDIA_ROUTER_IMAGE"
if ! docker image inspect "$NVIDIA_ROUTER_IMAGE" >/dev/null 2>&1; then
  echo "error: image $NVIDIA_ROUTER_IMAGE is not available; refusing to stop the running router" >&2
  exit 1
fi

echo "==> backup existing router data"
data_volume="$(docker volume ls -q --filter label=com.docker.compose.project=nvidia-router --filter label=com.docker.compose.volume=nvidia-router-data | head -n 1)"
if [[ -n "$data_volume" ]]; then
  require_ready=1
  backup_dir="backups/$(date +%Y%m%d-%H%M%S)"
  mkdir -p "$backup_dir"
  app_container="$(compose ps -q app)"
  if [[ -n "$app_container" ]]; then
    previous_image="$(docker inspect --format '{{.Config.Image}}' "$app_container" 2>/dev/null || true)"
  fi
  app_stopped=1
  compose stop app
  if [[ -n "$previous_image" ]]; then
    NVIDIA_ROUTER_IMAGE="$previous_image" compose run --rm --no-deps --user "$(id -u):$(id -g)" -v "$PWD/$backup_dir:/data-backups" app \
      db backup --output "/data-backups/router.db"
  else
    compose run --rm --no-deps --user "$(id -u):$(id -g)" -v "$PWD/$backup_dir:/data-backups" app \
      db backup --output "/data-backups/router.db"
  fi
else
  echo "  no existing data volume; skipping backup"
fi

echo "==> start single-container router"
database_migration_started=1
compose up -d --no-build

echo "==> verify readiness"
if ! wait_for_health "$require_ready"; then
  echo "error: router did not become ready" >&2
  exit 1
fi
app_stopped=0
if [[ "$require_ready" -eq 1 ]]; then
  echo "  /health/live and /health/ready passed"
else
  echo "  /health/live passed (new database may require initial password change before ready)"
fi
echo "deployed single-container router on :3756 ($TAG)"
