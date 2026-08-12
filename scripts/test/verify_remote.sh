#!/usr/bin/env bash
# End-to-end smoke test for the newly deployed router (20260812-round3).
set -u
BASE="http://127.0.0.1:3756"
ORIGIN="Origin: http://127.0.0.1:3756"
ADMIN_PASS="${NVIDIA_ROUTER_INITIAL_ADMIN_PASSWORD:-Temp_Admin_20260810}"
COOKIE_JAR="/tmp/nvr-verify-cookies.txt"
rm -f "$COOKIE_JAR"

echo "== 3. admin login =="
LOGIN=$(curl -sf --max-time 10 -H "$ORIGIN" -c "$COOKIE_JAR" -H 'Content-Type: application/json' \
  -d "{\"username\":\"admin\",\"password\":\"$ADMIN_PASS\"}" "$BASE/admin/api/auth/login")
echo "login: $LOGIN"
echo "$LOGIN" | grep -q '"authenticated":true' || { echo "FAIL: login"; exit 1; }

echo "== 4. runtime summary =="
curl -sf --max-time 10 -H "$ORIGIN" -b "$COOKIE_JAR" "$BASE/admin/api/runtime/summary" | head -c 400; echo

echo "== 5. proxy pool status (new fields) =="
curl -sf --max-time 10 -H "$ORIGIN" -b "$COOKIE_JAR" "$BASE/admin/api/proxy-pool/status" | head -c 500; echo

echo "== 6. create access key =="
AK=$(curl -sf --max-time 10 -H "$ORIGIN" -b "$COOKIE_JAR" -H 'Content-Type: application/json' \
  -d '{"name":"verify-round3"}' "$BASE/admin/api/access-keys")
echo "created: $(echo "$AK" | head -c 200)"
# The create response carries the plaintext at the top level (no data wrapper).
ACCESS_KEY=$(echo "$AK" | python3 -c 'import json,sys; print(json.load(sys.stdin)["key"])' 2>/dev/null)
if [ -z "$ACCESS_KEY" ]; then echo "FAIL: no access key plaintext"; exit 1; fi
echo "access key obtained: ${ACCESS_KEY:0:8}..."

echo "== 7. /v1/models =="
MODELS=$(curl -sf --max-time 15 -H "Authorization: Bearer $ACCESS_KEY" "$BASE/v1/models")
echo "$MODELS" | head -c 300; echo

echo "== 8. /v1/chat/completions (non-stream, through static proxy) =="
MODEL=$(echo "$MODELS" | python3 -c '
import json, sys
data = json.load(sys.stdin)
ids = [m["id"] for m in data.get("data", []) if m.get("id")]
print(ids[0] if ids else "")
' 2>/dev/null)
echo "using model: $MODEL"
if [ -z "$MODEL" ]; then echo "FAIL: no model id"; exit 1; fi
CHAT=$(curl -s --max-time 90 -H "Authorization: Bearer $ACCESS_KEY" -H 'Content-Type: application/json' \
  -d "{\"model\":\"$MODEL\",\"messages\":[{\"role\":\"user\",\"content\":\"Reply with exactly: OK\"}],\"max_tokens\":16}" \
  "$BASE/v1/chat/completions")
echo "chat response: $(echo "$CHAT" | head -c 600)"
echo "$CHAT" | grep -q '"finish_reason":"stop"' && echo "CHAT_PASS" || echo "CHAT_UNEXPECTED"
echo "$CHAT" | grep -q '"error"' && echo "CHAT_HAS_ERROR"

echo "== 9. revoke verify keys (cleanup) =="
# Revoke the key created in this run plus any orphaned ones from aborted runs.
for NAME in verify-round3 verify-round3-2; do
  KID=$(curl -sf --max-time 10 -H "$ORIGIN" -b "$COOKIE_JAR" "$BASE/admin/api/access-keys" \
    | python3 -c "import json,sys; print(next((str(k['id']) for k in json.load(sys.stdin)['data'] if k['name']=='$NAME'), ''))" 2>/dev/null)
  if [ -n "$KID" ]; then
    curl -sf --max-time 10 -H "$ORIGIN" -b "$COOKIE_JAR" -X DELETE "$BASE/admin/api/access-keys/$KID" >/dev/null && echo "revoked $NAME (#$KID)"
  fi
done
rm -f "$COOKIE_JAR"
echo "== done =="
