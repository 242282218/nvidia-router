set -euo pipefail
# Parameterized release prep: REL=target release dir, SRC=source tarball on the
# remote host. Falls back to the 20260816 defaults for one-shot reuse.
REL="${REL:-/opt/nvidia-router-releases/20260816-no-capability-gate}"
SRC="${SRC:-/tmp/nvr-src-20260816.tar.gz}"
OLD=/opt/nvidia-router-releases/20260815-ui-alpha-fix

mkdir -p "$REL"
tar -xzf "$SRC" -C "$REL"
cp "$OLD/.env" "$REL/.env"
chmod 600 "$REL/.env"
cp "$OLD/docker-compose.deploy.yml" "$REL/docker-compose.deploy.yml"
sed -i "s|nvidia-router:deploy-[^\"']*|nvidia-router:deploy-$(basename "$REL")|" "$REL/docker-compose.deploy.yml"
mkdir -p "$REL/backups"
chown 10001:10001 "$REL/backups"
rm -f "$SRC"

echo "--- release contents ---"
ls -A "$REL"
echo "--- image line ---"
grep image "$REL/docker-compose.deploy.yml"
echo "--- .env perms ---"
stat -c '%a %U' "$REL/.env"
