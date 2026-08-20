"""Exercise one OpenCode Free gateway model directly, from inside the gateway
container.

Runs ON the domestic test host. The router's key is read out of the app
container and handed to node on stdin, so it never appears in argv, in the host
process table or in the output; it is also redacted from every emitted body.

Placeholder: __MODEL__.
"""

import json
import subprocess

GATEWAY = "opencode-free-proxy-opencode-free-proxy-1"
APP = "nvidia-router-app-1"
MODEL = "__MODEL__"

NODE_SCRIPT = r"""
const http = require('http');
const MODEL = process.argv[1];
let key = '';
process.stdin.on('data', (c) => { key += c; });
process.stdin.on('end', async () => {
  key = key.trim();
  const call = (body, stream) => new Promise((resolve) => {
    const data = JSON.stringify(body);
    const started = Date.now();
    const req = http.request(
      { host: '127.0.0.1', port: 6020, path: '/v1/chat/completions', method: 'POST',
        headers: { Authorization: 'Bearer ' + key, 'Content-Type': 'application/json',
                   Accept: stream ? 'text/event-stream' : 'application/json' } },
      (res) => {
        let out = '';
        let ttft = null;
        res.on('data', (c) => { if (ttft === null) ttft = Date.now() - started; out += c; });
        res.on('end', () => resolve({
          status: res.statusCode, ttft_ms: ttft, ms: Date.now() - started,
          bytes: out.length, body: out.slice(0, 2500),
        }));
      });
    req.on('error', (e) => resolve({ error: String(e).slice(0, 300) }));
    req.write(data);
    req.end();
  });

  console.log('CASE nonstream ' + JSON.stringify(await call({
    model: MODEL,
    messages: [{ role: 'user', content: 'Reply with exactly OK.' }],
    max_tokens: 64,
  }, false)));

  console.log('CASE reasoning ' + JSON.stringify(await call({
    model: MODEL,
    messages: [{ role: 'user', content: 'A farmer has 17 sheep; all but 9 run away. How many remain? Explain briefly.' }],
    max_tokens: 512,
  }, false)));

  console.log('CASE stream ' + JSON.stringify(await call({
    model: MODEL,
    messages: [{ role: 'user', content: 'Count from 1 to 5, one number per line.' }],
    max_tokens: 128,
    stream: true,
  }, true)));
});
"""


def emit(kind, **fields):
    fields["kind"] = kind
    print("R|" + json.dumps(fields, sort_keys=True, separators=(",", ":")), flush=True)


def main():
    key = subprocess.run(
        ["docker", "exec", APP, "printenv", "NVIDIA_ROUTER_OPENCODEFREE_AUTH_KEY"],
        capture_output=True, text=True, timeout=60,
    ).stdout.strip()
    if not key:
        emit("meta", step="key", present=False)
        return 1
    result = subprocess.run(
        ["docker", "exec", "-i", GATEWAY, "node", "-e", NODE_SCRIPT, "--", MODEL],
        input=key + "\n", capture_output=True, text=True, timeout=600,
    )
    if result.returncode != 0:
        emit("meta", step="node", exit=result.returncode,
             stderr=result.stderr[:500].replace(key, "[redacted]"))
        return 1
    for line in result.stdout.splitlines():
        if line.startswith("CASE "):
            _, case, payload = line.split(" ", 2)
            emit("case", case=case, model=MODEL, **json.loads(payload.replace(key, "[redacted]")))
    return 0


raise SystemExit(main())
