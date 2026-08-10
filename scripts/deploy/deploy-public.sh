#!/usr/bin/env bash
# 构建并发布公网双服务到 hangzhou2-2。
#
# 用法（在 hangzhou2-2 的部署目录执行）：
#   ./scripts/deploy/deploy-public.sh [--tag <deploy-tag>] [--no-build]
#
# 需要本机具备：docker compose、目标服务器已存在 router-internal 外部网络。
# 真实密钥放在部署目录的 .env（反代）与 pool.env（代理池），本脚本不写密钥。

set -euo pipefail

cd "$(dirname "$0")/../.."

TAG="${1:-deploy-$(date +%Y%m%d-%H%M%S)}"
BUILD=1
if [ "${1:-}" = "--no-build" ]; then
  BUILD=0
  TAG="${2:-deploy}"
fi

echo "==> 构建反代镜像 nvidia-router:$TAG"
docker build -t "nvidia-router:$TAG" .

if [ "$BUILD" = "1" ]; then
  echo "==> 构建代理池镜像 star-proxy-pool:$TAG"
  # 代理池源码在星空代理池仓库，构建前需要先同步或本地已构建
  # 此处假设已在同一工作区准备好 build context；如需从源码构建，
  # 用：docker build -t "star-proxy-pool:$TAG" "$POOL_SOURCE_DIR"
  if [ -f "star-proxy-pool/Dockerfile" ]; then
    docker build -t "star-proxy-pool:$TAG" star-proxy-pool
  else
    echo "!! 未找到 star-proxy-pool/Dockerfile，跳过代理池构建；请先构建镜像或传入 POOL_IMAGE。"
  fi
fi

echo "==> 校验 .env 与 pool.env 存在"
for f in .env pool.env; do
  if [ ! -f "$f" ]; then
    echo "错误：缺少 $f（请从 .env.example / .env.ci 复制并填写真实密钥）" >&2
    exit 1
  fi
done

echo "==> 确保外部网络 router-internal"
docker network inspect router-internal >/dev/null 2>&1 || docker network create router-internal

echo "==> 创建数据目录并设置代理池 UID(10001) 可写"
mkdir -p pool-data
chown 10001:10001 pool-data

echo "==> 备份现有数据卷"
BACKUP_DIR="backups/$(date +%Y%m%d-%H%M%S)"
mkdir -p "$BACKUP_DIR"
for name in nvr-data pool-data; do
  if docker volume inspect "$name" >/dev/null 2>&1; then
    docker run --rm -v "$name:/data" -v "$PWD/$BACKUP_DIR:/backup" alpine tar czf "/backup/$name.tar.gz" -C /data . || true
  fi
done

echo "==> 启动/更新（显式 Compose 项目避免宿主全局 Compose 配置）"
env -u COMPOSE_FILE -u COMPOSE_PROJECT_NAME \
  docker compose \
    --project-directory "$PWD" \
    -p nvidia-router \
    -f docker-compose.yml \
    -f docker-compose.public.yml \
    up -d --build

echo "==> 验证"
sleep 5
curl -fsS --max-time 5 http://127.0.0.1:3756/health/live && echo "  <- /health/live"
curl -fsS --max-time 5 http://127.0.0.1:18081/healthz && echo "  <- pool /healthz"

echo "部署完成：反代公网 :3756，代理池公网 :18081，镜像标签 $TAG"
