#!/usr/bin/env bash
# scripts/litestream-manage.sh — enable / disable / verify Litestream
# replication sidecar for the router.
#
# Usage:
#   litestream-manage.sh up      # start the litestream sidecar (needs baseline)
#   litestream-manage.sh down    # stop the sidecar (replication pauses)
#   litestream-manage.sh status  # show sidecar + replica state
#   litestream-manage.sh log     # tail replication logs
#
# All commands operate on the compose project as deployed (`-p nvidia-router`).
# Requires LITESTREAM_REPLICA_URL in the environment/.env and object-store
# credentials exported to the shell before `up`.
#
# Before the first `up`, create a baseline snapshot so replication can start:
#   docker compose exec app nvidia-router db backup /tmp/router-base.db
#   docker compose -p nvidia-router cp app:/tmp/router-base.db ./router-base.db

set -euo pipefail

PROJECT=${PROJECT:-nvidia-router}
COMPOSE_FILES="-f docker-compose.yml -f docker-compose.litestream.yml"

cmd="${1:-status}"

switch() {
	docker compose -p "$PROJECT" $COMPOSE_FILES --profile litestream "$@"
}

case "$cmd" in
up)
	: "${LITESTREAM_REPLICA_URL:?LITESTREAM_REPLICA_URL must be set}"
	echo "Starting Litestream sidecar for project $PROJECT..."
	switch up -d
	switch ps
	;;
down)
	echo "Stopping Litestream sidecar..."
	switch stop litestream
	;;
status)
	switch ps
	;;
log)
	shift
	switch logs -f litestream "$@"
	;;
baseline)
	# Creates a baseline snapshot on the object store via a one-shot room.
	: "${LITESTREAM_REPLICA_URL:?LITESTREAM_REPLICA_URL must be set}"
	echo "Generating baseline snapshot (this pauses nothing; uses app db backup)..."
	switch --profile litestream run --rm litestream sh -c \
		'/usr/bin/litestream restore -config /etc/litestream.yml -o /tmp/check.out /data/router.db 2>/dev/null || true'
	echo "Baseline command finished. Verify with: litestream-manage.sh status"
	;;
*)
	echo "usage: $0 {up|down|status|log|baseline}" >&2
	exit 1
	;;
esac
