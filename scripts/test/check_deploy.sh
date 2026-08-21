#!/bin/bash
set -e

echo "=== Container Status ==="
docker ps --filter name=nvidia-router-app-1 --format '{{.Image}}\t{{.Status}}'

echo -e "\n=== Health Check ==="
curl -fsS http://127.0.0.1:3756/health/live
echo
curl -fsS http://127.0.0.1:3756/health/ready

echo -e "\n=== Metrics (proxy pool) ==="
curl -fsS http://127.0.0.1:3756/metrics 2>&1 | grep -E "proxy_pool_healthy|proxy_pool_total" | head -5
