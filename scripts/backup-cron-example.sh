#!/usr/bin/env bash
# Daily automatic backup for the NVIDIA API router.
#
# Intended to run from cron every day at 02:00 UTC:
#
#   TZ=UTC
#   0 2 * * * /path/to/nvida反代/scripts/backup-cron-example.sh >> /var/log/nvidia-router-backup.log 2>&1
#
# Defaults match the design doc: daily 02:00 UTC backup into /data-backups with
# a 7-day retention. Override with BACKUP_DIR / KEEP_DAYS when needed.
#
# The default mode stops the app during the backup window so the snapshot is
# never taken while the WAL journal is actively rotating (see
# docs/备份与恢复说明.md key principle 1). Set SKIP_STOP=1 to skip the stop and
# rely on the SQLite online-backup API instead; that mode is slightly faster but
# shares the database with a running writer and can hit a busy timeout.
set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"

# Host-side backup directory and its mount point inside the temporary container.
BACKUP_DIR="${BACKUP_DIR:-${PROJECT_DIR}/backups}"
BACKUP_DIR_INSIDE="${BACKUP_DIR_INSIDE:-/data-backups}"

# Retention in days; backups older than this are removed.
KEEP_DAYS="${KEEP_DAYS:-7}"

# Set to 1 to back up without stopping the app (online-backup mode).
SKIP_STOP="${SKIP_STOP:-0}"

if ! command -v docker >/dev/null 2>&1; then
  printf 'backup-cron-example: docker is required on the host.\n' >&2
  exit 1
fi

mkdir -p "${BACKUP_DIR}"
stamp="$(date -u +%Y%m%dT%H%M%SZ)"
output="${BACKUP_DIR_INSIDE}/router-${stamp}.db"

cd "${PROJECT_DIR}"

if [[ "${SKIP_STOP}" == "0" ]]; then
  docker compose stop app
  trap 'docker compose up -d app' EXIT
fi

docker compose run --rm --no-deps \
  -v "${BACKUP_DIR}:${BACKUP_DIR_INSIDE}" \
  app db backup --output "${output}"

# Keep only the newest KEEP_DAYS of backups on the host side.
find "${BACKUP_DIR}" -maxdepth 1 -type f -name 'router-*.db' -mtime +"${KEEP_DAYS}" -print -delete

printf 'backup-cron-example: completed %s\n' "${BACKUP_DIR}/router-${stamp}.db"
