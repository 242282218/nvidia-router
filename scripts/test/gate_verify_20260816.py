"""Post-deploy verification: capability gate must be gone for every chat model.

Covers tools (non-stream + stream), reasoning params passthrough, and a model
whose DB flag still says supports_tools=0 (minimax-m3). Temp key cleaned up.
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
JAR = '/tmp/glm52-gate-verify-cookies.txt'
KEY_NAME = 'gate-verify-20260816'

WEATHER_TOOLS = [{'type': 'function', 'function': {
    'name': 'get_weather', 'description': 'Get current weather for a city',
    'parameters': {'type': 'object',
                   'properties': {'city': {'type': 'string'}},
                   'required': ['city']}}}]


def main():
    password = os.environ.get('NVR_ADMIN_PASS')
    if not password:
        print('NVR_ADMIN_PASS env var is required')
        return 2
    k = paramiko.Ed25519Key.from_private_key_file(KEY)
    client = paramiko.SSHClient()
    client.set_missing_host_key_policy(paramiko.AutoAddPolicy())
    client.connect(HOST, username=USER, port=PORT, pkey=k, timeout=30)

    def run(cmd, timeout=200):
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
        snap = json.loads(admin('GET', '/admin/api/proxy-pool'))['data']
        print('proxy_mode:', snap.get('mode'), 'enabled:', snap.get('enabled'))

        created = json.loads(admin('POST', '/admin/api/access-keys', {'name': KEY_NAME}))
        ak = created['key']

        def chat(payload, stream=False, max_time=150):
            body = json.dumps(payload, ensure_ascii=False)
            flag = ' -N' if stream else ''
            return run(f"curl -s{flag} --max-time {max_time} "
                       f"-w '\\n__HTTP__:%{{http_code}}' "
                       f"-H {shlex.quote('Authorization: Bearer ' + ak)} "
                       f"-H 'Content-Type: application/json' "
                       f"-d {shlex.quote(body)} {BASE}/v1/chat/completions")

        cases = [
            ('glm-5.2 tools nonstream', 'z-ai/glm-5.2',
             {'messages': [{'role': 'user', 'content': "Weather in Hangzhou? Use the tool."}],
              'tools': WEATHER_TOOLS, 'tool_choice': 'auto', 'max_tokens': 200}, False),
            ('glm-5.2 tools stream', 'z-ai/glm-5.2',
             {'messages': [{'role': 'user', 'content': "Weather in Beijing? Use the tool."}],
              'tools': WEATHER_TOOLS, 'max_tokens': 200, 'stream': True}, True),
            ('glm-5.2 reasoning_effort passthrough', 'z-ai/glm-5.2',
             {'messages': [{'role': 'user', 'content': '1+1=? Reply with just the number.'}],
              'reasoning_effort': 'high', 'max_tokens': 500}, False),
            ('glm-5.2 thinking passthrough', 'z-ai/glm-5.2',
             {'messages': [{'role': 'user', 'content': '2+2=? Reply with just the number.'}],
              'thinking': {'type': 'enabled', 'budget_tokens': 2048}, 'max_tokens': 500}, False),
            ('minimax-m3 tools (db flag still false)', 'minimaxai/minimax-m3',
             {'messages': [{'role': 'user', 'content': "Weather in Shanghai? Use the tool."}],
              'tools': WEATHER_TOOLS, 'max_tokens': 200}, False),
        ]
        for name, model, payload, stream in cases:
            payload = dict(payload, model=model)
            out = chat(payload, stream=stream)
            head, _, tail = out.rpartition('\n__HTTP__:') if '__HTTP__:' in out else ('', '', out)
            status = tail.strip()[:3] if tail else '???'
            passed = 'tool_calls' in out or 'tool_call_chunks' in out
            body_head = (head or out)[:400].replace('\n', ' ')
            print(f'[{name}] status={status} tool_calls={passed}')
            print(f'  body: {body_head}')

        models = run(f'curl -s -H {shlex.quote("Authorization: Bearer " + ak)} {BASE}/v1/models')
        ids = [m.get('id') for m in json.loads(models).get('data', [])]
        print('models:', ids)
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
