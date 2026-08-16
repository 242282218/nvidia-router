"""Post-audit cleanup for glm52_production_audit: verify proxy restored, remove
the temporary access key and cookie jar, and confirm service health."""
import json
import os
import shlex
import sys

import paramiko

KEY = r'D:\PROJECT_ZZZZZZZZZ\服务器管理\hangzhou2-2\ssh_host_key'
HOST, PORT, USER = '114.55.25.190', 22, 'root'
BASE = 'http://127.0.0.1:3756'
ORIGIN = f'Origin: {BASE}'
JAR = '/tmp/glm52-audit-cookies.txt'
KEY_NAME = 'glm52-audit-20260816'


def main():
    password = os.environ.get('NVR_ADMIN_PASS')
    if not password:
        print('NVR_ADMIN_PASS env var is required')
        return 2
    k = paramiko.Ed25519Key.from_private_key_file(KEY)
    client = paramiko.SSHClient()
    client.set_missing_host_key_policy(paramiko.AutoAddPolicy())
    client.connect(HOST, username=USER, port=PORT, pkey=k, timeout=30)

    def run(cmd, timeout=60):
        _, stdout, _ = client.exec_command(cmd, timeout=timeout)
        return stdout.read().decode(errors='replace')

    def admin(method, path, data=None):
        body = ''
        if data is not None:
            body = f"-H 'Content-Type: application/json' -d {shlex.quote(json.dumps(data))}"
        return run(f"curl -s -b {JAR} -H {shlex.quote(ORIGIN)} {body} "
                   f"-X {method} {shlex.quote(BASE + path)}")

    login = run(f"curl -s -c {JAR} -H {shlex.quote(ORIGIN)} -H 'Content-Type: application/json' "
                f"-d {shlex.quote(json.dumps({'username': 'admin', 'password': password}))} "
                f"{BASE}/admin/api/auth/login")
    print('login_ok:', '"authenticated":true' in login)

    snap = json.loads(admin('GET', '/admin/api/proxy-pool'))['data']
    status = json.loads(admin('GET', '/admin/api/proxy-pool/status'))['data']
    print('proxy:', json.dumps({
        'enabled': snap.get('enabled'), 'mode': snap.get('mode'),
        'source': snap.get('source'),
        'collector_enabled': status.get('collector_enabled'),
        'healthy_size': status.get('healthy_size'),
        'total_size': status.get('total_size'),
        'last_success_at': status.get('last_success_at'),
        'last_error_code': status.get('last_error_code')}, ensure_ascii=False))

    listing = json.loads(admin('GET', '/admin/api/access-keys'))
    removed = []
    for item in listing.get('data', []):
        if item.get('name') == KEY_NAME:
            admin('DELETE', f"/admin/api/access-keys/{item['id']}")
            removed.append(item['id'])
    print('removed_key_ids:', removed)

    remaining = [i.get('name') for i in
                 json.loads(admin('GET', '/admin/api/access-keys')).get('data', [])]
    print('remaining_keys:', remaining)

    admin('POST', '/admin/api/auth/logout')
    run(f'rm -f {JAR}')

    live = run(f"curl -s -o /dev/null -w '%{{http_code}}' {BASE}/health/live").strip()
    ready = run(f"curl -s -o /dev/null -w '%{{http_code}}' {BASE}/health/ready").strip()
    docker = run("docker inspect -f '{{.RestartCount}} {{.State.Health.Status}}' "
                 "nvidia-router-app-1").strip()
    print('health:', json.dumps({'live': live, 'ready': ready, 'container': docker}))
    client.close()
    return 0


if __name__ == '__main__':
    sys.exit(main())
