import paramiko
import os
import sys

KEY = r'D:\PROJECT_ZZZZZZZZZ\服务器管理\hangzhou2-2\ssh_host_key'
TAR = os.path.join(os.environ.get('TEMP', '/tmp'), 'round3.tar')
RELEASE = '/opt/nvidia-router-releases/20260812-round3'
OLD = '/opt/nvidia-router-releases/20260811-proxy-optimize'

k = paramiko.Ed25519Key.from_private_key_file(KEY)
c = paramiko.SSHClient()
c.set_missing_host_key_policy(paramiko.AutoAddPolicy())
c.connect('114.55.25.190', username='root', port=22, pkey=k, timeout=30)


def run(cmd, timeout=1800):
    print('>>> ' + cmd, flush=True)
    stdin, stdout, stderr = c.exec_command(cmd, timeout=timeout)
    out = stdout.read().decode(errors='replace')
    err = stderr.read().decode(errors='replace')
    code = stdout.channel.recv_exit_status()
    if out.strip():
        print(out, flush=True)
    if err.strip():
        print('[stderr] ' + err, flush=True)
    print('[exit %d]' % code, flush=True)
    if code != 0:
        print('ABORT', flush=True)
        sys.exit(1)
    return out


print('== sync source ==', flush=True)
sftp = c.open_sftp()
sftp.put(TAR, '/tmp/round3.tar')
run(f'cd {RELEASE} && tar xf /tmp/round3.tar && rm -f /tmp/round3.tar')
# Restore machine-specific config files (tar overwrote them).
run(f'cp {OLD}/docker-compose.yml {OLD}/docker-compose.public.yml {OLD}/docker-compose.remote.yml {OLD}/Dockerfile {RELEASE}/')

print('== rebuild ==', flush=True)
base = (f'cd {RELEASE} && docker compose -p nvidia-router -f docker-compose.yml '
        f'-f docker-compose.public.yml -f docker-compose.remote.yml')
run(base + ' build --build-arg GOPROXY=https://goproxy.cn,direct app', timeout=1800)

print('== up ==', flush=True)
run(base + ' up -d app', timeout=300)
c.close()
print('== redeploy done ==', flush=True)
