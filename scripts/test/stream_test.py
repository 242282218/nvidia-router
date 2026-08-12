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
JAR=/tmp/nvr-s.txt
rm -f "$JAR"
curl -sf -H "$ORIGIN" -c "$JAR" -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"Temp_Admin_20260810"}' "$BASE/admin/api/auth/login" >/dev/null
AK=$(curl -sf -H "$ORIGIN" -b "$JAR" -H 'Content-Type: application/json' \
  -d '{"name":"verify-round3"}' "$BASE/admin/api/access-keys" | python3 -c 'import json,sys; print(json.load(sys.stdin)["key"])')
echo "== stream chat =="
R=$(curl -s --max-time 120 -N -H "Authorization: Bearer $AK" -H 'Content-Type: application/json' \
  -d '{"model":"deepseek-ai/deepseek-v4-flash","messages":[{"role":"user","content":"Count 1 to 3, one per line."}],"max_tokens":40,"stream":true}' \
  "$BASE/v1/chat/completions")
echo "$R" | head -c 400; echo
echo "$R" | grep -q "data: \[DONE\]" && echo "STREAM_PASS" || echo "STREAM_UNEXPECTED"
echo "$R" | grep -q '"error"' && echo "STREAM_HAS_ERROR"
KID=$(curl -sf -H "$ORIGIN" -b "$JAR" "$BASE/admin/api/access-keys" | python3 -c "import json,sys; print(next(str(k['id']) for k in json.load(sys.stdin)['data'] if k['name']=='verify-round3'))")
curl -sf -H "$ORIGIN" -b "$JAR" -X DELETE "$BASE/admin/api/access-keys/$KID" >/dev/null
rm -f "$JAR"
echo done
'''
print('>>> stream test', flush=True)
stdin, stdout, stderr = c.exec_command(script, timeout=200)
print(stdout.read().decode(errors='replace'), flush=True)
err = stderr.read().decode(errors='replace')
if err.strip():
    print('[stderr] ' + err, flush=True)
c.close()
