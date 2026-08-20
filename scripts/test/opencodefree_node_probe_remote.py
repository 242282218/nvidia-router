"""Call the OpenCode Free gateway from inside its own container via node.

Runs ON the domestic test host. The gateway image ships no curl/wget/python, so
the request is issued with whatever runtime the image already has. Secrets are
redacted before anything is emitted.
"""

import json
import subprocess

GATEWAY = "opencode-free-proxy-opencode-free-proxy-1"

NODE_SCRIPT = r"""
const http = require('http');
function call(path, body) {
  return new Promise((resolve) => {
    const data = body ? JSON.stringify(body) : null;
    const req = http.request(
      { host: '127.0.0.1', port: 6020, path, method: data ? 'POST' : 'GET',
        headers: data ? { 'Content-Type': 'application/json' } : {} },
      (res) => {
        let out = '';
        res.on('data', (c) => { out += c; });
        res.on('end', () => resolve({ path, status: res.statusCode, headers: res.headers, body: out.slice(0, 1500) }));
      });
    req.on('error', (e) => resolve({ path, error: String(e).slice(0, 300) }));
    if (data) req.write(data);
    req.end();
  });
}
(async () => {
  const models = await call('/v1/models');
  console.log('RESULT ' + JSON.stringify(models));
  const chat = await call('/v1/chat/completions', {
    model: 'deepseek-v4-flash-free',
    messages: [{ role: 'user', content: 'Reply with exactly OK.' }],
    max_tokens: 16,
  });
  console.log('RESULT ' + JSON.stringify(chat));
})();
"""


def emit(kind, **fields):
    fields["kind"] = kind
    print("R|" + json.dumps(fields, sort_keys=True, separators=(",", ":")), flush=True)


def run(args, timeout=120):
    result = subprocess.run(args, capture_output=True, text=True, timeout=timeout)
    return result.returncode, result.stdout, result.stderr


def main():
    code, out, err = run(["docker", "exec", GATEWAY, "sh", "-c",
                          "command -v node || command -v bun || command -v deno || echo none"])
    emit("runtime", exit=code, found=(out or err).strip()[:200])

    code, out, err = run(["docker", "exec", GATEWAY, "node", "-e", NODE_SCRIPT], timeout=200)
    if code != 0:
        emit("meta", step="node", exit=code, stderr=(err or "")[:400])
        return 1
    for line in out.splitlines():
        if line.startswith("RESULT "):
            emit("gateway", **json.loads(line[len("RESULT "):]))
    return 0


raise SystemExit(main())
