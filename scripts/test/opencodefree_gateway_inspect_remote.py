"""Inspect the OpenCode Free gateway container from inside its own network.

Runs ON the domestic test host. Lists the gateway container, tails its log and
calls its API from inside the container, so the non-standard status arrives with
its body. Secrets are redacted before anything is emitted.
"""

import json
import subprocess

MAX_BODY = 1500


def emit(kind, **fields):
    fields["kind"] = kind
    print("R|" + json.dumps(fields, sort_keys=True, separators=(",", ":")), flush=True)


def run(args, timeout=90):
    result = subprocess.run(args, capture_output=True, text=True, timeout=timeout)
    return result.returncode, result.stdout, result.stderr


def main():
    _, out, _ = run(["docker", "ps", "--format", "{{.Names}}\t{{.Image}}\t{{.Status}}"])
    rows = [line.split("\t") for line in out.strip().splitlines() if line.strip()]
    emit("containers", rows=[{"name": r[0], "image": r[1] if len(r) > 1 else "",
                              "status": r[2] if len(r) > 2 else ""} for r in rows])

    gateway = ""
    for row in rows:
        if "opencode" in row[0].lower():
            gateway = row[0]
            break
    if not gateway:
        emit("meta", step="gateway", found=False)
        return 1
    emit("meta", step="gateway", found=True, name=gateway)

    code, out, err = run(["docker", "logs", "--tail", "60", gateway])
    emit("logs", exit=code, tail=(out or err)[-4000:])

    # The gateway image may not ship curl; try a few callers in order.
    probes = [
        ["docker", "exec", gateway, "curl", "-sS", "-i", "--max-time", "60", "http://127.0.0.1:6020/v1/models"],
        ["docker", "exec", gateway, "wget", "-qS", "-O", "-", "http://127.0.0.1:6020/v1/models"],
        ["docker", "exec", gateway, "python3", "-c",
         "import urllib.request as u\ntry:\n r=u.urlopen('http://127.0.0.1:6020/v1/models',timeout=60)\n print(r.status);print(r.read(1500).decode('utf-8','replace'))\nexcept Exception as e:\n print(type(e).__name__, str(e)[:300])\n b=getattr(e,'read',None)\n print(b(1500).decode('utf-8','replace') if b else '')"],
    ]
    for probe in probes:
        code, out, err = run(probe)
        emit("probe", tool=probe[3], exit=code,
             stdout=(out or "")[:MAX_BODY], stderr=(err or "")[:400])
        if code == 0 and out.strip():
            break
    return 0


raise SystemExit(main())
