#!/usr/bin/env python3
"""Reset the admin management password on the domestic host.

`admin reset-password` is an offline command: it takes the router database
process lock, so the app has to be stopped first. This script stops the app,
runs the reset with the currently deployed image (which also applies any pending
migrations), starts the app again and verifies that the new password logs in.

The password arrives on this script's stdin and is only ever written to another
process's stdin — never to argv, a file, an environment variable or the output.
Nothing here prints it, and the remote host never sees it in `ps` or shell
history.

Usage: printf '%s' "$NEW_PASSWORD" | python scripts/deploy/reset_admin_password_remote.py
"""

from __future__ import annotations

import json
import sys
from pathlib import Path

import paramiko

REPO = Path(__file__).resolve().parents[2]
SSH_CONFIG = REPO.parents[0] / "服务器管理" / "hangzhou2-2" / "ssh_config_local"
BASE = "http://127.0.0.1:3756"


def connect() -> paramiko.SSHClient:
    config = paramiko.SSHConfig()
    with SSH_CONFIG.open(encoding="utf-8") as handle:
        config.parse(handle)
    host = config.lookup("hangzhou2-2")
    client = paramiko.SSHClient()
    client.set_missing_host_key_policy(paramiko.AutoAddPolicy())
    client.connect(
        host["hostname"], port=int(host.get("port", 22)), username=host.get("user", "root"),
        key_filename=host["identityfile"][0], timeout=30,
    )
    return client


def run(client: paramiko.SSHClient, command: str, timeout: int = 600, check: bool = True,
        feed: str | None = None) -> str:
    print(f"$ {command}", flush=True)
    stdin, stdout, stderr = client.exec_command(command, timeout=timeout)
    if feed is not None:
        # The only channel the secret travels through.
        stdin.write(feed)
        stdin.flush()
        stdin.channel.shutdown_write()
    output = stdout.read().decode("utf-8", "replace")
    status = stdout.channel.recv_exit_status()
    error = stderr.read().decode("utf-8", "replace")
    tail = "\n".join(output.splitlines()[-15:])
    if tail:
        print(tail, flush=True)
    if status != 0:
        print(error[-2000:], file=sys.stderr, flush=True)
        if check:
            raise SystemExit(f"step failed (exit {status}): {command}")
    return output


def main() -> int:
    password = sys.stdin.readline().rstrip("\r\n")
    if len(password) < 12:
        # adminauth.validateNewPassword enforces 12 runes; fail before touching
        # the deployment rather than after the app is already stopped.
        raise SystemExit("password must be at least 12 characters")

    client = connect()
    try:
        release = run(client, "docker inspect nvidia-router-app-1 --format "
                              "'{{index .Config.Labels \"com.docker.compose.project.working_dir\"}}'").strip()
        image = run(client, "docker inspect nvidia-router-app-1 --format '{{.Config.Image}}'").strip()
        if not release or not image:
            raise SystemExit("cannot determine the running release; aborting")
        print(f"release: {release}\nimage:   {image}", flush=True)
        compose = (f"cd {release} && NVIDIA_ROUTER_IMAGE={image} docker compose "
                   f"-p nvidia-router -f docker-compose.yml -f docker-compose.deploy.yml")

        print("\n=== stop app (release the database process lock) ===", flush=True)
        run(client, f"{compose} stop app")

        print("\n=== reset admin password ===", flush=True)
        run(client, f"docker run --rm -i --user 10001:10001 -v nvr-data:/data {image} "
                    f"admin reset-password", feed=password + "\n")

        print("\n=== start app ===", flush=True)
        run(client, f"{compose} up -d app", timeout=900)

        print("\n=== health ===", flush=True)
        run(client, f"sleep 8 && curl -fsS {BASE}/health/live", check=False)
        run(client, f"curl -fsS {BASE}/health/ready", check=False)

        print("\n=== login verification ===", flush=True)
        # --data-binary @- keeps the credential on stdin. The admin API enforces a
        # same-origin guard, so Origin has to be sent explicitly.
        login = run(client,
                    f"curl -sS -o /dev/null -w '%{{http_code}}' -X POST "
                    f"-H 'Origin: {BASE}' -H 'Content-Type: application/json' "
                    f"--data-binary @- {BASE}/admin/api/auth/login",
                    feed=json.dumps({"username": "admin", "password": password}),
                    check=False).strip()
        print(f"login status: {login}", flush=True)
        if login != "200":
            raise SystemExit(f"login with the new password returned {login}, want 200")

        print("\n=== management gate (anonymous must be 401) ===", flush=True)
        anon = run(client, f"curl -sS -o /dev/null -w '%{{http_code}}' {BASE}/metrics",
                   check=False).strip()
        print(f"anonymous /metrics: {anon}", flush=True)
        if anon != "401":
            raise SystemExit(f"anonymous /metrics returned {anon}, want 401")
        print("\nadmin password reset and verified", flush=True)
    finally:
        client.close()
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
