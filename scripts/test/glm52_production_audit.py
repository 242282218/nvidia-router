"""GLM-5.2 production-readiness audit on hangzhou2-2, direct vs built-in proxy mode.

Reads the admin password from NVR_ADMIN_PASS so the credential never lands in
Git or in this file. Every phase prints NDJSON progress lines; the final line is
a JSON blob with all raw samples for offline aggregation.
"""
import json
import os
import shlex
import statistics
import sys
import time

import paramiko

KEY = r'D:\PROJECT_ZZZZZZZZZ\服务器管理\hangzhou2-2\ssh_host_key'
HOST, PORT, USER = '114.55.25.190', 22, 'root'
BASE = 'http://127.0.0.1:3756'
ORIGIN = f'Origin: {BASE}'
JAR = '/tmp/glm52-audit-cookies.txt'
KEY_NAME = 'glm52-audit-20260816'
MODEL_MATCH = 'glm-5.2'
MARK = '\n__R__%{http_code} %{time_starttransfer} %{time_total}'

LONG_DOC = '背景资料：星空代理池通过XK上游采集HTTP代理，经出口验证后进入池内轮换，' \
           '路由器按会话粘滞选择出口并复用连接，验证失败的出口会被剔除。' * 200


class Remote:
    def __init__(self):
        k = paramiko.Ed25519Key.from_private_key_file(KEY)
        self.client = paramiko.SSHClient()
        self.client.set_missing_host_key_policy(paramiko.AutoAddPolicy())
        self.client.connect(HOST, username=USER, port=PORT, pkey=k, timeout=30)
        self.client.get_transport().set_keepalive(30)

    def run(self, cmd, timeout=240):
        _, stdout, _ = self.client.exec_command(cmd, timeout=timeout)
        out = stdout.read().decode(errors='replace')
        code = stdout.channel.recv_exit_status()
        return out, code

    def start(self, cmd):
        chan = self.client.get_transport().open_session()
        chan.settimeout(200)
        chan.exec_command(cmd)
        return chan

    @staticmethod
    def finish(chan):
        data = chan.makefile('rb').read().decode(errors='replace')
        code = chan.recv_exit_status()
        return data, code

    def close(self):
        self.client.close()


def http(remote, method, url, data=None, headers=(), max_time=120, raw=False, jar=None):
    parts = [f'curl -s --max-time {max_time}']
    if jar:
        parts.append(f'-b {jar}')
    parts += ['-w', shlex.quote(MARK)]
    if method != 'GET':
        parts.append(f'-X {method}')
    for h in headers:
        parts += ['-H', shlex.quote(h)]
    if data is not None:
        body = data if isinstance(data, str) else json.dumps(data, ensure_ascii=False)
        parts += ['-H', "'Content-Type: application/json'", '-d', shlex.quote(body)]
    parts.append(shlex.quote(url))
    out, _ = remote.run(' '.join(parts), timeout=max_time + 60)
    if '__R__' not in out:
        return {'status': 0, 'ttft': None, 'total': None, 'body': out[:2000]}
    body, tail = out.rsplit('__R__', 1)
    fields = tail.split()
    return {
        'status': int(fields[0]) if fields and fields[0].isdigit() else 0,
        'ttft': float(fields[1]) if len(fields) > 1 else None,
        'total': float(fields[2]) if len(fields) > 2 else None,
        'body': body if raw else body[:4000],
    }


def login(remote, password):
    # Cookie jar via -c/-b files; the password only appears in the transient
    # remote command line and is never echoed to stdout.
    cmd = (f"curl -s -c {JAR} -H {shlex.quote(ORIGIN)} -H 'Content-Type: application/json' "
           f"-d {shlex.quote(json.dumps({'username': 'admin', 'password': password}))} "
           f"{BASE}/admin/api/auth/login")
    out, _ = remote.run(cmd)
    ok = '"authenticated":true' in out
    if not ok:
        print('LOGIN_FAILED: ' + out[:300])
        sys.exit(2)


def admin_with_jar(remote, method, path, data=None):
    # raw=True keeps large admin JSON (proxy lists, key lists) intact for parsing
    return http(remote, method, BASE + path, data,
                headers=[ORIGIN], max_time=90, jar=JAR, raw=True)


def pool_snapshot(remote):
    r = admin_with_jar(remote, 'GET', '/admin/api/proxy-pool')
    try:
        return json.loads(r['body'])['data']
    except Exception:
        return {'parse_error': r['body'][:200]}


def pool_status(remote):
    r = admin_with_jar(remote, 'GET', '/admin/api/proxy-pool/status')
    try:
        d = json.loads(r['body'])['data']
        return {k: d.get(k) for k in ('mode', 'configured', 'collector_enabled',
                                      'healthy_size', 'total_size', 'panic_mode',
                                      'last_success_at', 'last_error_code')}
    except Exception:
        return {'parse_error': r['body'][:200]}


def metrics(remote):
    r = http(remote, 'GET', BASE + '/metrics', max_time=30)
    values = {}
    for line in r['body'].splitlines():
        if line.startswith('nvidia_router_requests') or line.startswith('nvidia_router_proxy_pool'):
            parts = line.split()
            if len(parts) == 2:
                values[parts[0]] = parts[1]
    return values


def set_proxy(remote, enabled):
    r = admin_with_jar(remote, 'PATCH', '/admin/api/proxy-pool', {'enabled': enabled})
    snap = pool_snapshot(remote)
    return r['status'] == 200 and snap.get('enabled') == enabled


def chat(remote, ak, payload, max_time=150, stream=False, tag=''):
    headers = [f'Authorization: Bearer {ak}']
    flag = ' -N' if stream else ''
    body = json.dumps(payload, ensure_ascii=False)
    cmd = (f'curl -s{flag} --max-time {max_time} -w {shlex.quote(MARK)} '
           f'-H {shlex.quote(headers[0])} -H \'Content-Type: application/json\' '
           f'-d {shlex.quote(body)} {shlex.quote(BASE + "/v1/chat/completions")}')
    out, _ = remote.run(cmd, timeout=max_time + 60)
    rec = {'dim': tag, 'ts': time.time()}
    if '__R__' not in out:
        rec.update(status=0, ttft=None, total=None, body=out[:400])
        return rec
    raw_body, tail = out.rsplit('__R__', 1)
    fields = tail.split()
    rec['status'] = int(fields[0]) if fields and fields[0].isdigit() else 0
    rec['ttft'] = float(fields[1]) if len(fields) > 1 else None
    rec['total'] = float(fields[2]) if len(fields) > 2 else None
    rec['ok'] = rec['status'] == 200
    rec['check'] = ''
    if rec['ok']:
        try:
            data = json.loads(raw_body)
            choice = data.get('choices', [{}])[0]
            content = choice.get('message', {}).get('content', '') if choice.get('message') else ''
            rec['finish'] = choice.get('finish_reason')
            rec['content_head'] = (content or '')[:120]
            rec['usage'] = data.get('usage')
            rec['ok'] = bool(content or choice.get('message', {}).get('refusal') is not None)
            if not rec['ok']:
                rec['check'] = 'empty_content'
        except Exception as exc:
            rec['ok'] = False
            rec['check'] = f'json_parse:{type(exc).__name__}'
            rec['content_head'] = raw_body[:120]
    else:
        rec['content_head'] = raw_body[:200]
    return rec


def chat_stream(remote, ak, payload, max_time=180, tag=''):
    payload = dict(payload, stream=True)
    headers = [f'Authorization: Bearer {ak}']
    body = json.dumps(payload, ensure_ascii=False)
    cmd = (f'curl -s -N --max-time {max_time} -w {shlex.quote(MARK)} '
           f'-H {shlex.quote(headers[0])} -H \'Content-Type: application/json\' '
           f'-d {shlex.quote(body)} {shlex.quote(BASE + "/v1/chat/completions")}')
    out, _ = remote.run(cmd, timeout=max_time + 60)
    rec = {'dim': tag, 'ts': time.time()}
    if '__R__' not in out:
        rec.update(status=0, ttft=None, total=None, body=out[:300])
        return rec
    raw_body, tail = out.rsplit('__R__', 1)
    fields = tail.split()
    rec['status'] = int(fields[0]) if fields and fields[0].isdigit() else 0
    rec['ttft'] = float(fields[1]) if len(fields) > 1 else None
    rec['total'] = float(fields[2]) if len(fields) > 2 else None
    rec['ok'] = rec['status'] == 200
    rec['check'] = ''
    text = ''
    has_done = '[DONE]' in raw_body
    chunk_count = raw_body.count('data:')
    for line in raw_body.splitlines():
        if line.startswith('data:') and '[DONE]' not in line:
            try:
                chunk = json.loads(line[5:].strip())
                delta = chunk.get('choices', [{}])[0].get('delta', {})
                text += delta.get('content') or ''
            except Exception:
                pass
    rec['chunks'] = chunk_count
    rec['has_done'] = has_done
    rec['content_head'] = text[:120]
    if rec['ok'] and (not text or not has_done):
        rec['ok'] = False
        rec['check'] = 'no_done' if not has_done else 'no_content'
    if not rec['ok']:
        rec['body'] = raw_body[:300]
    return rec


def responses_api(remote, ak, model, stream=False, tag=''):
    payload = {'model': model, 'input': 'Reply with exactly: OK'}
    if stream:
        payload['stream'] = True
    body = json.dumps(payload)
    cmd = (f'curl -s --max-time 150 -w {shlex.quote(MARK)} '
           f'-H {shlex.quote("Authorization: Bearer " + ak)} -H \'Content-Type: application/json\' '
           f'-d {shlex.quote(body)} {shlex.quote(BASE + "/v1/responses")}')
    out, _ = remote.run(cmd, timeout=210)
    rec = {'dim': tag, 'ts': time.time()}
    if '__R__' not in out:
        rec.update(status=0, ttft=None, total=None, body=out[:300])
        return rec
    raw_body, tail = out.rsplit('__R__', 1)
    fields = tail.split()
    rec['status'] = int(fields[0]) if fields and fields[0].isdigit() else 0
    rec['ttft'] = float(fields[1]) if len(fields) > 1 else None
    rec['total'] = float(fields[2]) if len(fields) > 2 else None
    rec['ok'] = rec['status'] == 200
    rec['check'] = ''
    if rec['ok']:
        if stream:
            rec['ok'] = 'response.completed' in raw_body
            if not rec['ok']:
                rec['check'] = 'no_completed_event'
        else:
            try:
                data = json.loads(raw_body)
                rec['ok'] = bool(data.get('output'))
                rec['content_head'] = str(data.get('output_text', ''))[:100] or \
                    str(data.get('output', [{}])[-1])[:100]
            except Exception:
                rec['ok'] = False
                rec['check'] = 'json_parse'
    if not rec['ok']:
        rec['body'] = raw_body[:300]
    return rec


def concurrent_chat(remote, ak, model, n=5, tag='concurrency'):
    payload = {'model': model,
               'messages': [{'role': 'user', 'content': 'Reply with exactly: OK'}],
               'max_tokens': 16}
    body = json.dumps(payload)
    cmd_tpl = (f'curl -s --max-time 150 -w {shlex.quote(MARK)} '
               f'-H {shlex.quote("Authorization: Bearer " + ak)} -H \'Content-Type: application/json\' '
               f'-d {shlex.quote(body)} {shlex.quote(BASE + "/v1/chat/completions")}')
    t0 = time.monotonic()
    chans = [remote.start(cmd_tpl) for _ in range(n)]
    results = []
    for c in chans:
        out, _ = Remote.finish(c)
        rec = {'dim': tag, 'ts': time.time()}
        if '__R__' not in out:
            rec.update(status=0, ok=False, total=None, ttft=None, check='no_marker')
            results.append(rec)
            continue
        raw_body, tail = out.rsplit('__R__', 1)
        fields = tail.split()
        rec['status'] = int(fields[0]) if fields and fields[0].isdigit() else 0
        rec['ttft'] = float(fields[1]) if len(fields) > 1 else None
        rec['total'] = float(fields[2]) if len(fields) > 2 else None
        rec['ok'] = rec['status'] == 200 and '"content"' in raw_body
        if not rec['ok']:
            rec['body'] = raw_body[:200]
        results.append(rec)
    return results, time.monotonic() - t0


SHORT = {'messages': [{'role': 'user', 'content': 'Reply with exactly: OK'}], 'max_tokens': 16}


def suite(remote, ak, model, mode, results, stability_n=20):
    def add(recs):
        for r in recs:
            r['mode'] = mode
            results.append(r)
            line = {'mode': mode, 'dim': r['dim'], 'status': r.get('status'),
                    'ok': r.get('ok'), 'ttft': r.get('ttft'), 'total': r.get('total'),
                    'check': r.get('check', ''), 'head': r.get('content_head', '')[:60]}
            print(json.dumps(line, ensure_ascii=False), flush=True)

    # model discovery
    r = http(remote, 'GET', BASE + '/v1/models',
             headers=[f'Authorization: Bearer {ak}'])
    ids = []
    try:
        ids = [m.get('id') for m in json.loads(r['body']).get('data', [])]
    except Exception:
        pass
    rec = {'dim': 'models', 'status': r['status'], 'ok': model in ids,
           'total': r.get('total'), 'ttft': r.get('ttft'),
           'check': '' if model in ids else 'model_missing',
           'count': len(ids)}
    add([rec])

    base = {'model': model, **SHORT}

    # chat non-stream x5
    add([chat(remote, ak, base, tag='chat_nonstream') for _ in range(5)])

    # chat stream x3
    add([chat_stream(remote, ak, base, tag='chat_stream') for _ in range(3)])

    # responses API
    add([responses_api(remote, ak, model, stream=False, tag='responses_nonstream')])
    add([responses_api(remote, ak, model, stream=True, tag='responses_stream')])

    # json mode
    jp = {'model': model, 'max_tokens': 120,
          'messages': [{'role': 'user',
                        'content': '返回一个JSON对象，字段name值为glm，字段version值为5.2。只输出JSON。'}],
          'response_format': {'type': 'json_object'}}
    jr = chat(remote, ak, jp, tag='json_mode')
    try:
        parsed = json.loads(jr.get('content_head', '') or '')
        jr['ok'] = jr.get('ok') and isinstance(parsed, dict)
        if not isinstance(parsed, dict):
            jr['check'] = 'not_json_object'
    except Exception:
        jr['ok'] = False
        jr['check'] = (jr.get('check') or '') + 'invalid_json'
    add([jr])

    # chinese quality
    cp = {'model': model, 'max_tokens': 200,
          'messages': [{'role': 'user', 'content': '用中文写一段50字左右的文案介绍杭州西湖，第一句以"西湖"开头。'}]}
    cr = chat(remote, ak, cp, tag='chinese')
    if cr.get('ok'):
        text = cr.get('content_head', '')
        cr['ok'] = any('\u4e00' <= ch <= '\u9fff' for ch in text)
        if not cr['ok']:
            cr['check'] = 'no_cjk_content'
    add([cr])

    # code generation
    kp = {'model': model, 'max_tokens': 400,
          'messages': [{'role': 'user', 'content': '用Go写一个迭代法计算斐波那契第n项的函数fib(n int) int，只输出代码。'}]}
    kr = chat(remote, ak, kp, tag='code')
    if kr.get('ok'):
        text = kr.get('content_head', '')
        kr['ok'] = 'func ' in text or 'func' in text
        if not kr['ok']:
            kr['check'] = 'no_code_block'
    add([kr])

    # long context ~13k chars
    lp = {'model': model, 'max_tokens': 100,
          'messages': [{'role': 'user',
                        'content': LONG_DOC + '\n请用一句话总结上文主题，并以"主题："开头。'}]}
    add([chat(remote, ak, lp, max_time=180, tag='long_context')])

    # tool calls (candidate declares supports_tools=false; expect 501)
    tp = {'model': model, 'max_tokens': 100,
          'messages': SHORT['messages'],
          'tools': [{'type': 'function', 'function': {
              'name': 'get_weather', 'description': 'Get weather',
              'parameters': {'type': 'object', 'properties': {'city': {'type': 'string'}},
                             'required': ['city']}}}]}
    tr = chat(remote, ak, tp, tag='tool_calls')
    tr['expected_501'] = True
    if tr.get('status') == 501:
        tr['ok'] = True
        tr['check'] = 'tools_unsupported_as_declared'
    elif tr.get('status') == 200:
        tr['ok'] = True
        tr['check'] = 'tools_works_better_than_declared'
    add([tr])

    # concurrency x5
    recs, wall = concurrent_chat(remote, ak, model)
    for rec_ in recs:
        rec_['wall_total'] = round(wall, 3)
    add(recs)

    # stability xN sequential
    for i in range(stability_n):
        rec_ = chat(remote, ak, base, tag='stability')
        rec_['seq'] = i + 1
        add([rec_])


def pct(values, q):
    if not values:
        return None
    s = sorted(values)
    return round(s[min(len(s) - 1, int(round(q * (len(s) - 1))))], 3)


def summarize(results):
    out = {}
    for mode in ('proxy', 'direct'):
        rows = [r for r in results if r.get('mode') == mode and r.get('dim') not in (None, 'models')]
        dims = {}
        for r in rows:
            d = r['dim']
            dims.setdefault(d, []).append(r)
        summary = {}
        for d, items in dims.items():
            ok = [i for i in items if i.get('ok')]
            lat = [i['total'] for i in items if i.get('total')]
            ttft = [i['ttft'] for i in items if i.get('ttft')]
            entry = {'n': len(items), 'ok': len(ok),
                     'ok_rate': round(len(ok) / len(items), 4) if items else None}
            if lat:
                entry.update(p50=pct(lat, .5), p95=pct(lat, .95), mx=round(max(lat), 3),
                             mean=round(statistics.mean(lat), 3))
            if ttft:
                entry.update(ttft_p50=pct(ttft, .5), ttft_mx=round(max(ttft), 3))
            fails = [i.get('check') or i.get('body', '')[:60] for i in items if not i.get('ok')]
            if fails:
                entry['failures'] = fails[:5]
            summary[d] = entry
        out[mode] = summary
    return out


def main():
    password = os.environ.get('NVR_ADMIN_PASS')
    if not password:
        print('NVR_ADMIN_PASS env var is required')
        return 2
    remote = Remote()
    try:
        login(remote, password)
        print(json.dumps({'phase': 'login', 'ok': True}), flush=True)

        # clean stale audit keys, then create a fresh one
        listing = admin_with_jar(remote, 'GET', '/admin/api/access-keys')
        try:
            for item in json.loads(listing['body']).get('data', []):
                if item.get('name') == KEY_NAME:
                    admin_with_jar(remote, 'DELETE', f"/admin/api/access-keys/{item['id']}")
        except Exception:
            pass
        created = admin_with_jar(remote, 'POST', '/admin/api/access-keys', {'name': KEY_NAME})
        ak = json.loads(created['body']).get('key')
        if not ak:
            print('ACCESS_KEY_CREATE_FAILED: ' + created['body'][:300])
            return 2
        print(json.dumps({'phase': 'access_key', 'ok': True}), flush=True)

        m = http(remote, 'GET', BASE + '/v1/models', headers=[f'Authorization: Bearer {ak}'])
        model = MODEL_MATCH
        try:
            ids = [x.get('id') for x in json.loads(m['body']).get('data', [])]
            exact = [i for i in ids if MODEL_MATCH in (i or '')]
            if exact:
                model = exact[0]
        except Exception:
            pass
        print(json.dumps({'phase': 'model_resolved', 'model': model}), flush=True)

        results = []
        meta = {}

        # Phase 1: proxy mode (current warm state)
        snap = pool_snapshot(remote)
        meta['proxy_snapshot_before'] = {'enabled': snap.get('enabled'), 'mode': snap.get('mode')}
        meta['proxy_pool_status_before'] = pool_status(remote)
        meta['metrics_before'] = metrics(remote)
        if not snap.get('enabled'):
            print('POOL_NOT_ENABLED_AT_START', flush=True)
            set_proxy(remote, True)
            time.sleep(5)
        suite(remote, ak, model, 'proxy', results)
        meta['metrics_after_proxy'] = metrics(remote)
        meta['proxy_pool_status_after'] = pool_status(remote)

        # Phase 2: direct mode
        if not set_proxy(remote, False):
            print('FAILED_TO_DISABLE_PROXY', flush=True)
            raise RuntimeError('proxy disable failed')
        time.sleep(3)
        meta['direct_snapshot'] = {'enabled': pool_snapshot(remote).get('enabled')}
        suite(remote, ak, model, 'direct', results)
        meta['metrics_after_direct'] = metrics(remote)

        print(json.dumps({'phase': 'suites_done'}), flush=True)
    finally:
        # Restore: proxy back on, temp key removed, admin session closed.
        try:
            set_proxy(remote, True)
            deadline = time.time() + 90
            healthy = None
            while time.time() < deadline:
                st = pool_status(remote)
                healthy = st.get('healthy_size')
                if isinstance(healthy, int) and healthy > 0:
                    break
                time.sleep(5)
            listing = admin_with_jar(remote, 'GET', '/admin/api/access-keys')
            try:
                for item in json.loads(listing['body']).get('data', []):
                    if item.get('name') == KEY_NAME:
                        admin_with_jar(remote, 'DELETE', f"/admin/api/access-keys/{item['id']}")
            except Exception as exc:
                print(json.dumps({'phase': 'key_cleanup_error', 'error': str(exc)}), flush=True)
            admin_with_jar(remote, 'POST', '/admin/api/auth/logout')
            remote.run(f'rm -f {JAR}')
            live = http(remote, 'GET', BASE + '/health/live', max_time=20)
            ready = http(remote, 'GET', BASE + '/health/ready', max_time=20)
            docker, _ = remote.run(
                "docker inspect -f '{{.RestartCount}} {{.State.Health.Status}}' nvidia-router-app-1")
            restore = {'proxy_enabled': pool_snapshot(remote).get('enabled'),
                       'healthy_size': healthy,
                       'live': live['status'], 'ready': ready['status'],
                       'container': docker.strip()}
            print(json.dumps({'phase': 'restore', **restore}, ensure_ascii=False), flush=True)
        except Exception as exc:
            print(json.dumps({'phase': 'restore_error', 'error': str(exc)}), flush=True)
        remote.close()

    print('===SUMMARY===')
    print(json.dumps({'meta': meta, 'summary': summarize(results),
                      'samples': results}, ensure_ascii=False, default=str))
    return 0


if __name__ == '__main__':
    sys.exit(main())
