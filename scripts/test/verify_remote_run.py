import paramiko
import sys

KEY = r'D:\PROJECT_ZZZZZZZZZ\服务器管理\hangzhou2-2\ssh_host_key'

k = paramiko.Ed25519Key.from_private_key_file(KEY)
c = paramiko.SSHClient()
c.set_missing_host_key_policy(paramiko.AutoAddPolicy())
c.connect('114.55.25.190', username='root', port=22, pkey=k, timeout=30)

sftp = c.open_sftp()
sftp.put(r'D:\PROJECT_ZZZZZZZZZ\nvida反代\scripts\test\verify_remote.sh', '/tmp/verify_remote.sh')
sftp.chmod('/tmp/verify_remote.sh', 0o755)

cmd = 'bash /tmp/verify_remote.sh 2>&1'
print('>>> ' + cmd, flush=True)
stdin, stdout, stderr = c.exec_command(cmd, timeout=300)
out = stdout.read().decode(errors='replace')
err = stderr.read().decode(errors='replace')
code = stdout.channel.recv_exit_status()
if out.strip():
    print(out, flush=True)
if err.strip():
    print('[stderr] ' + err, flush=True)
print('[exit %d]' % code, flush=True)
c.close()
