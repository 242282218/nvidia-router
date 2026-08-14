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
# The baseline command starts the replicate process and confirms its status.

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
	: "${LITESTREAM_REPLICA_URL:?LITESTREAM_REPLICA_URL must be set}"
	echo "Starting Litestream replication baseline..."
	switch up -d litestream
	switch ps litestream
	;;
*)
	echo "usage: $0 {up|down|status|log|baseline}" >&2
	exit 1
	;;
esac
