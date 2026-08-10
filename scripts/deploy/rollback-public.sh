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

echo "==> 恢复数据卷"
BACKUP_DIR="backups"
LATEST=$(ls -1t "$BACKUP_DIR" | head -1)
if [ -n "${LATEST:-}" ]; then
  for name in nvr-data pool-data; do
    if [ -f "$BACKUP_DIR/$LATEST/$name.tar.gz" ]; then
      echo "  恢复 $name 从 $BACKUP_DIR/$LATEST"
      docker run --rm -v "$name:/data" -v "$PWD/$BACKUP_DIR/$LATEST:/backup" alpine sh -c \
        "rm -rf /data/* && tar xzf /backup/$name.tar.gz -C /data" || true
    fi
  done
fi

echo "==> 用镜像标签 $TAG 重启"
env -u COMPOSE_FILE -u COMPOSE_PROJECT_NAME \
  docker compose \
    --project-directory "$PWD" \
    -p "$COMPOSE_PROJECT" \
    -f docker-compose.yml \
    -f docker-compose.public.yml \
    up -d --force-recreate

echo "==> 验证"
curl -fsS --max-time 5 http://127.0.0.1:3756/health/live && echo "  <- /health/live"
curl -fsS --max-time 5 http://127.0.0.1:18081/healthz && echo "  <- pool /healthz"

echo "回滚完成"
