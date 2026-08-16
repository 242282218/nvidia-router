set -euo pipefail
# Parameterized deploy swap: REL=new release dir, OLDREL=running release dir.
# Derives image tags from the compose files so the names always match reality.
REL="${REL:-/opt/nvidia-router-releases/20260816-no-capability-gate}"
OLDREL="${OLDREL:-/opt/nvidia-router-releases/20260815-ui-alpha-fix}"

img_of() { grep -o 'image: nvidia-router:[^"[:space:]]*' "$1/docker-compose.deploy.yml" | awk '{print $2}'; }
IMG_OLD="$(img_of "$OLDREL")"
IMG_NEW="$(img_of "$REL")"

cd "$REL"

echo "--- build $IMG_NEW"
docker build --build-arg GOPROXY=https://goproxy.cn,direct -t "$IMG_NEW" .

echo "--- stop current app"
env -u COMPOSE_FILE -u COMPOSE_PROJECT_NAME docker compose \
  --project-directory "$OLDREL" -p nvidia-router -f "$OLDREL/docker-compose.deploy.yml" stop app

echo "--- backup database with old image ($IMG_OLD)"
docker run --rm -v nvr-data:/data -v "$REL/backups:/data-backups" \
  --env-file "$REL/.env" "$IMG_OLD" \
  db backup --output "/data-backups/router-db-pre-$(basename "$REL").db"
chmod 600 "$REL/backups/router-db-pre-$(basename "$REL").db"

echo "--- start new app ($IMG_NEW)"
env -u COMPOSE_FILE -u COMPOSE_PROJECT_NAME docker compose \
  --project-directory "$REL" -p nvidia-router -f docker-compose.deploy.yml up -d

echo "--- wait for health"
for i in $(seq 1 30); do
  status=$(docker inspect -f '{{.State.Health.Status}} {{.RestartCount}}' nvidia-router-app-1 2>/dev/null || echo starting)
  echo "attempt $i: $status"
  case "$status" in healthy*) break ;; esac
  sleep 5
done

echo "--- endpoints"
curl -fsS --max-time 5 http://127.0.0.1:3756/health/live && echo
curl -fsS --max-time 5 http://127.0.0.1:3756/health/ready && echo
docker ps --filter name=nvidia-router-app-1 --format '{{.Image}} {{.Status}}'
