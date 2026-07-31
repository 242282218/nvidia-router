import fnmatch
import io
import os
import sys
import tarfile

import paramiko

HOST = "149.71.241.250"
USER = "root"
PASSWORD = "REDACTED_CREDENTIAL"
REMOTE_DIR = "/opt/nvidia-router"
LOCAL_ROOT = os.path.abspath(".")

# Exclude only build artifacts and sensitive files, keeping the whole project
# (docs, scripts, tests, .env.example) available on the test machine.
EXCLUDED = [
    ".git", ".github", ".superpowers",
    "chat.test.exe", "nvidia-router.exe",
    "web/node_modules", "node_modules",
    "web/dist", "internal/web/dist",
    "web/test-results", "web/playwright-report",
    "*.db", "*.db-*", "*.sqlite*", "*-wal", "*-shm",
    "*.log", "coverage",
]


def should_exclude(rel):
    parts = rel.split("/")
    for pattern in EXCLUDED:
        if pattern in ("node_modules", "web/dist", "internal/web/dist", "web/test-results", "web/playwright-report"):
            if pattern in parts or rel.startswith(pattern + "/") or rel == pattern:
                return True
            continue
        if fnmatch.fnmatch(rel, pattern) or fnmatch.fnmatch(rel, "**/" + pattern):
            return True
    return False


def sync_repo(client):
    remote_tar = "/tmp/nvidia-router-src.tar.gz"
    stream = io.BytesIO()
    with tarfile.open(fileobj=stream, mode="w:gz") as archive:
        for root, dirs, files in os.walk(LOCAL_ROOT):
            dirs[:] = [d for d in dirs if not d.startswith((".git", "node_modules", "web/dist", "internal/web/dist", "web/test-results", "web/playwright-report"))]
            for name in files:
                local_path = os.path.join(root, name)
                rel = os.path.relpath(local_path, LOCAL_ROOT).replace(os.sep, "/")
                if should_exclude(rel):
                    continue
                archive.add(local_path, arcname=rel)
    stream.seek(0)
    sftp = client.open_sftp()
    try:
        sftp.putfo(stream, remote_tar)
    finally:
        sftp.close()

    stdin, stdout, stderr = client.exec_command(
        f"mkdir -p {REMOTE_DIR} && rm -rf {REMOTE_DIR}/* && tar -xzf {remote_tar} -C {REMOTE_DIR} && rm -f {remote_tar} && ls {REMOTE_DIR} | head -20",
        timeout=120,
    )
    out = stdout.read().decode("utf-8", "replace").strip()
    err = stderr.read().decode("utf-8", "replace").strip()
    code = stdout.channel.recv_exit_status()
    print(f"=== sync (exit {code}) ===")
    print(out if out else err)
    return code == 0


def main():
    client = paramiko.SSHClient()
    client.set_missing_host_key_policy(paramiko.AutoAddPolicy())
    client.connect(HOST, username=USER, password=PASSWORD, timeout=20)
    try:
        if not sync_repo(client):
            raise SystemExit(1)
        print("Sync complete.")
    finally:
        client.close()


if __name__ == "__main__":
    sys.exit(main())
