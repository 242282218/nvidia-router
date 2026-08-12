import paramiko
import time

KEY = r'D:\PROJECT_ZZZZZZZZZ\服务器管理\hangzhou2-2\ssh_host_key'
k = paramiko.Ed25519Key.from_private_key_file(KEY)
c = paramiko.SSHClient()
c.set_missing_host_key_policy(paramiko.AutoAddPolicy())
c.connect('114.55.25.190', username='root', port=22, pkey=k, timeout=30)
for i in range(18):
    stdin, stdout, stderr = c.exec_command(
        'docker inspect nvidia-router-app-1 --format "{{.State.Health.Status}}" 2>/dev/null || echo starting', timeout=30)
    status = stdout.read().decode().strip()
    print(i, status, flush=True)
    if status == 'healthy':
        break
    time.sleep(5)
c.close()
