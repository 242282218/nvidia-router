"""Live-verify whether NVIDIA upstream actually supports tool calls for GLM-5.2.

1. Records the candidate metadata for z-ai/glm-5.2 (what discovery declared).
2. Patches the registered model's supports_tools to true (bypasses the local gate).
3. Sends real tools requests (non-stream + stream) through the router.
4. Cleans up the temp access key; leaves supports_tools=true only if upstream works
   (the caller decides to revert on failure).
"""
import json
import os
import shlex
import sys

import paramiko

KEY = r'D:\PROJECT_ZZZZZZZZZ\服务器管理\hangzhou2-2\ssh_host_key'
HOST, PORT, USER = '114.55.25.190', 22, 'root'
BASE = 'http://127.0.0.1:3756'
ORIGIN = f'Origin: {BASE}'
JAR = '/tmp/glm52-tools-verify-cookies.txt'
MODEL = 'z-ai/glm-5.2'
KEY_NAME = 'glm52-tools-verify'

TOOLS_PAYLOAD = {
    'model': MODEL,
    'messages': [{'role': 'user',
                  'content': "What's the weather in Hangzhou right now? Call the get_weather tool."}],
    'tools': [{'type': 'function', 'function': {
        'name': 'get_weather', 'description': 'Get current weather for a city',
        'parameters': {'type': 'object',
                       'properties': {'city': {'type': 'string'}},
                       'required': ['city']}}}],
    'tool_choice': 'auto',
    'max_tokens': 300,
}


def main():
    password = os.environ.get('NVR_ADMIN_PASS')
    if not password:
        print('NVR_ADMIN_PASS env var is required')
        return 2
    k = paramiko.Ed25519Key.from_private_key_file(KEY)
    client = paramiko.SSHClient()
    client.set_missing_host_key_policy(paramiko.AutoAddPolicy())
    client.connect(HOST, username=USER, port=PORT, pkey=k, timeout=30)

    def run(cmd, timeout=240):
        _, stdout, _ = client.exec_command(cmd, timeout=timeout)
        return stdout.read().decode(errors='replace')

    def admin(method, path, data=None):
        body = ''
        if data is not None:
            body = f"-H 'Content-Type: application/json' -d {shlex.quote(json.dumps(data))}"
        return run(f'curl -s -b {JAR} -H {shlex.quote(ORIGIN)} {body} '
                   f'-X {method} {shlex.quote(BASE + path)}')

    login = run(f"curl -s -c {JAR} -H {shlex.quote(ORIGIN)} -H 'Content-Type: application/json' "
                f"-d {shlex.quote(json.dumps({'username': 'admin', 'password': password}))} "
                f'{BASE}/admin/api/auth/login')
    assert '"authenticated":true' in login, 'login failed: ' + login[:200]

    try:
        models = json.loads(admin('GET', '/admin/api/models'))['data']
        glm = next(m for m in models if m['public_id'] == MODEL)
        print('model_before:', json.dumps(
            {f: glm[f] for f in ('id', 'supports_tools', 'supports_vision', 'supports_reasoning')}))

        cands = json.loads(admin('GET', '/admin/api/models/candidates'))['data']
        cand = next((c for c in cands if c['upstream_id'] == MODEL), None)
        print('candidate_metadata:', json.dumps(cand, ensure_ascii=False))

        patched = json.loads(admin('PATCH', f"/admin/api/models/{glm['id']}",
                                   {'supports_tools': True}))
        print('model_after:', json.dumps(
            {f: patched[f] for f in ('id', 'supports_tools', 'enabled')}))

        for item in json.loads(admin('GET', '/admin/api/access-keys')).get('data', []):
            if item.get('name') == KEY_NAME:
                admin('DELETE', f"/admin/api/access-keys/{item['id']}")
        created = json.loads(admin('POST', '/admin/api/access-keys', {'name': KEY_NAME}))
        ak = created['key']

        for stream in (False, True):
            payload = dict(TOOLS_PAYLOAD, stream=True) if stream else TOOLS_PAYLOAD
            flag = ' -N' if stream else ''
            out = run(f"curl -s{flag} --max-time 120 "
                      f"-H {shlex.quote('Authorization: Bearer ' + ak)} "
                      f"-H 'Content-Type: application/json' "
                      f"-d {shlex.quote(json.dumps(payload))} "
                      f"{BASE}/v1/chat/completions")
            tag = 'stream' if stream else 'nonstream'
            print(f'=== tools {tag} (first 1500 chars) ===')
            print(out[:1500])
            print(f'=== has tool_calls marker: {("tool_calls" in out) or ("tool_call_chunks" in out)} ===')
    finally:
        try:
            for item in json.loads(admin('GET', '/admin/api/access-keys')).get('data', []):
                if item.get('name') == KEY_NAME:
                    admin('DELETE', f"/admin/api/access-keys/{item['id']}")
            admin('POST', '/admin/api/auth/logout')
        finally:
            run(f'rm -f {JAR}')
        client.close()
    return 0


if __name__ == '__main__':
    sys.exit(main())
