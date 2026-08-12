import paramiko

KEY = r'D:\PROJECT_ZZZZZZZZZ\服务器管理\hangzhou2-2\ssh_host_key'
k = paramiko.Ed25519Key.from_private_key_file(KEY)
c = paramiko.SSHClient()
c.set_missing_host_key_policy(paramiko.AutoAddPolicy())
c.connect('114.55.25.190', username='root', port=22, pkey=k, timeout=30)

script = r'''
set -u
BASE="http://127.0.0.1:3756"
ORIGIN="Origin: http://127.0.0.1:3756"
JAR=/tmp/nvr-v2.txt
rm -f "$JAR"
curl -sf -H "$ORIGIN" -c "$JAR" -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"Temp_Admin_20260810"}' "$BASE/admin/api/auth/login" >/dev/null
AK=$(curl -sf -H "$ORIGIN" -b "$JAR" -H 'Content-Type: application/json' \
  -d '{"name":"verify-round3"}' "$BASE/admin/api/access-keys" | python3 -c 'import json,sys; print(json.load(sys.stdin)["key"])')
echo "access key: ${AK:0:8}..."
MODEL="deepseek-ai/deepseek-v4-flash"
for i in 1 2 3 4 5 6; do
  R=$(curl -s --max-time 100 -H "Authorization: Bearer $AK" -H 'Content-Type: application/json' \
    -d "{\"model\":\"$MODEL\",\"messages\":[{\"role\":\"user\",\"content\":\"Reply with exactly: OK\"}],\"max_tokens\":16}" \
    "$BASE/v1/chat/completions")
  if echo "$R" | grep -q '"finish_reason":"stop"'; then
    echo "attempt $i: PASS -> $(echo "$R" | head -c 200)"
  else
    echo "attempt $i: FAIL -> $(echo "$R" | head -c 200)"
  fi
done
KID=$(curl -sf -H "$ORIGIN" -b "$JAR" "$BASE/admin/api/access-keys" | python3 -c "import json,sys; print(next(str(k['id']) for k in json.load(sys.stdin)['data'] if k['name']=='verify-round3'))")
curl -sf -H "$ORIGIN" -b "$JAR" -X DELETE "$BASE/admin/api/access-keys/$KID" >/dev/null
rm -f "$JAR"
'''
cmd = "bash -c " + repr(script)
print('>>> chat retry loop', flush=True)
stdin, stdout, stderr = c.exec_command(script, timeout=700)
print(stdout.read().decode(errors='replace'), flush=True)
err = stderr.read().decode(errors='replace')
if err.strip():
    print('[stderr] ' + err, flush=True)
c.close()
