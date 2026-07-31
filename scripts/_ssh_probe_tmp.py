import sys

import paramiko

HOST = "149.71.241.250"
USER = "root"
PASSWORD = "REDACTED_CREDENTIAL"
REMOTE_DIR = "/opt/nvidia-router"


def run(client, command):
    stdin, stdout, stderr = client.exec_command(command, timeout=30)
    out = stdout.read().decode("utf-8", "replace").strip()
    code = stdout.channel.recv_exit_status()
    return code, out


def main():
    client = paramiko.SSHClient()
    client.set_missing_host_key_policy(paramiko.AutoAddPolicy())
    client.connect(HOST, username=USER, password=PASSWORD, timeout=20)
    try:
        for command, label in [
            (f"ls {REMOTE_DIR}/internal/protocol/", "protocol dir"),
            (f"ls {REMOTE_DIR}/internal/protocol/audio 2>&1", "audio dir"),
            (f"ls {REMOTE_DIR}/internal/ | head -30", "internal dir"),
        ]:
            code, out = run(client, command)
            print(f"=== {label} (exit {code}) ===")
            print(out)
    finally:
        client.close()


if __name__ == "__main__":
    sys.exit(main())
