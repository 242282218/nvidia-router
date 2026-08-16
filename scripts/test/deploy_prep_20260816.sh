set -euo pipefail
REL=/opt/nvidia-router-releases/20260816-no-capability-gate
OLD=/opt/nvidia-router-releases/20260815-ui-alpha-fix

mkdir -p "$REL"
tar -xzf /tmp/nvr-src-20260816.tar.gz -C "$REL"
cp "$OLD/.env" "$REL/.env"
chmod 600 "$REL/.env"
cp "$OLD/docker-compose.deploy.yml" "$REL/docker-compose.deploy.yml"
sed -i 's|nvidia-router:deploy-20260815-ui-alpha-fix|nvidia-router:deploy-20260816-no-capability-gate|' "$REL/docker-compose.deploy.yml"
mkdir -p "$REL/backups"
chown 10001:10001 "$REL/backups"
rm -f /tmp/nvr-src-20260816.tar.gz

echo "--- release contents ---"
ls -A "$REL"
echo "--- image line ---"
grep image "$REL/docker-compose.deploy.yml"
echo "--- .env perms ---"
stat -c '%a %U' "$REL/.env"
