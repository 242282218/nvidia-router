"""Inspect the live deployment before touching it.

Runs ON the domestic test host. Read-only: reports the release layout, the
compose files in use, the app container's image and the data volume, so a
deployment plan is based on what is actually running rather than on memory.
Secrets are never read or printed — only file names and permissions.
"""

import json
import subprocess

APP = "nvidia-router-app-1"


def emit(kind, **fields):
    fields["kind"] = kind
    print("R|" + json.dumps(fields, sort_keys=True, separators=(",", ":")), flush=True)


def run(args, timeout=90):
    result = subprocess.run(args, capture_output=True, text=True, timeout=timeout)
    return result.returncode, result.stdout.strip(), result.stderr.strip()


def main():
    _, releases, _ = run(["ls", "-1t", "/opt/nvidia-router-releases"])
    emit("releases", entries=releases.splitlines()[:10])

    code, inspect, _ = run([
        "docker", "inspect", APP,
        "--format", "{{.Config.Image}}\t{{index .Config.Labels \"com.docker.compose.project.working_dir\"}}\t"
                    "{{index .Config.Labels \"com.docker.compose.project.config_files\"}}\t{{.State.Status}}",
    ])
    if code == 0 and inspect:
        parts = inspect.split("\t")
        emit("app", image=parts[0], working_dir=parts[1] if len(parts) > 1 else "",
             config_files=parts[2] if len(parts) > 2 else "", status=parts[3] if len(parts) > 3 else "")

    _, mounts, _ = run(["docker", "inspect", APP, "--format", "{{range .Mounts}}{{.Type}}:{{.Name}}:{{.Destination}} {{end}}"])
    emit("mounts", value=mounts)

    _, volumes, _ = run(["docker", "volume", "ls", "--format", "{{.Name}}"])
    emit("volumes", entries=[v for v in volumes.splitlines() if "nvr" in v or "nvidia" in v])

    working_dir = ""
    for line in releases.splitlines():
        candidate = "/opt/nvidia-router-releases/" + line
        code, listing, _ = run(["ls", "-la", candidate])
        if code == 0:
            emit("release_listing", path=candidate, entries=listing.splitlines()[:20])
            working_dir = candidate
            break

    if working_dir:
        code, out, _ = run(["ls", "-la", working_dir + "/backups"])
        emit("backups", exists=(code == 0), entries=out.splitlines()[:8] if code == 0 else [])

    _, images, _ = run(["docker", "images", "--format", "{{.Repository}}:{{.Tag}}\t{{.CreatedSince}}"])
    emit("images", entries=[i for i in images.splitlines() if "nvidia-router" in i][:8])

    _, disk, _ = run(["df", "-h", "/opt", "/var/lib/docker"])
    emit("disk", entries=disk.splitlines())

    code, out, _ = run(["docker", "exec", APP, "printenv", "NVIDIA_ROUTER_DATA_DIR"])
    emit("data_dir", value=out if code == 0 else "")
    return 0


raise SystemExit(main())
