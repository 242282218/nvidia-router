#!/usr/bin/env bash

set -euo pipefail

admin_session=''
temporary_access_id=''
temporary_access_key=''

report() {
  local case_name="$1"
  local status="$2"
  local started="$3"
  printf 'case=%s status=%s duration=%ss\n' "$case_name" "$status" "$((SECONDS - started))"
}

admin_request() {
  local method="$1"
  local path="$2"
  local payload="$3"

  printf '%s' "$payload" | curl --silent \
    --request "$method" \
    --url "${NVIDIA_ROUTER_LIVE_BASE_URL}${path}" \
    --header "Origin: ${admin_origin}" \
    --header 'Content-Type: application/json' \
    --header @<(printf 'Cookie: nvr_admin_session=%s\n' "$admin_session") \
    --data-binary @- \
    --write-out $'\n%{http_code}'
}

response_status() {
  local response="$1"
  printf '%s' "${response##*$'\n'}"
}

response_body() {
  local response="$1"
  printf '%s' "${response%$'\n'*}"
}

cleanup() {
  local exit_code=$?
  local started
  local response
  local status

  trap - EXIT
  if [[ -n "$temporary_access_id" && -n "$admin_session" ]]; then
    started=$SECONDS
    if response="$(admin_request DELETE "/admin/api/access-keys/${temporary_access_id}" '')"; then
      status="$(response_status "$response")"
      if [[ "$status" == '204' ]]; then
        report 'RevokeTemporaryAccessKey' 'PASS' "$started"
      else
        report 'RevokeTemporaryAccessKey' 'FAIL' "$started"
        (( exit_code == 0 )) && exit_code=1
      fi
    else
      report 'RevokeTemporaryAccessKey' 'FAIL' "$started"
      (( exit_code == 0 )) && exit_code=1
    fi
  fi

  if [[ -n "$admin_session" ]]; then
    started=$SECONDS
    if response="$(admin_request POST '/admin/api/auth/logout' '')"; then
      status="$(response_status "$response")"
      if [[ "$status" == '204' ]]; then
        report 'AdminLogout' 'PASS' "$started"
      else
        report 'AdminLogout' 'FAIL' "$started"
        (( exit_code == 0 )) && exit_code=1
      fi
    else
      report 'AdminLogout' 'FAIL' "$started"
      (( exit_code == 0 )) && exit_code=1
    fi
  fi

  unset temporary_access_key NVIDIA_ROUTER_LIVE_ACCESS_KEY admin_session
  exit "$exit_code"
}

trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

for dependency in git curl python3 go; do
  started=$SECONDS
  if command -v "$dependency" >/dev/null 2>&1; then
    report "Dependency-${dependency}" 'PASS' "$started"
  else
    report "Dependency-${dependency}" 'FAIL' "$started"
    exit 1
  fi
done

started=$SECONDS
if [[ -z "${NVIDIA_ROUTER_LIVE_BASE_URL:-}" ||
      -z "${NVIDIA_ROUTER_ADMIN_PASSWORD:-}" ||
      -z "${NVIDIA_ROUTER_LIVE_CHAT_MODEL:-}" ||
      -z "${NVIDIA_ROUTER_LIVE_EMBEDDING_MODEL:-}" ]]; then
  report 'Configuration' 'FAIL' "$started"
  exit 1
fi
NVIDIA_ROUTER_ADMIN_USERNAME="${NVIDIA_ROUTER_ADMIN_USERNAME:-admin}"
NVIDIA_ROUTER_LIVE_BASE_URL="${NVIDIA_ROUTER_LIVE_BASE_URL%/}"
if ! admin_origin="$(printf '%s' "$NVIDIA_ROUTER_LIVE_BASE_URL" | python3 -c '
import sys
from urllib.parse import urlsplit
value = urlsplit(sys.stdin.read())
if value.scheme not in ("http", "https") or not value.netloc or value.username or value.password:
    raise SystemExit(1)
print(f"{value.scheme}://{value.netloc}", end="")
')"; then
  report 'Configuration' 'FAIL' "$started"
  exit 1
fi
report 'Configuration' 'PASS' "$started"

repo_root="$(git rev-parse --show-toplevel)"
cd "$repo_root"

started=$SECONDS
health_status="$(curl --silent --output /dev/null --write-out '%{http_code}' "${NVIDIA_ROUTER_LIVE_BASE_URL}/health/live" || true)"
if [[ "$health_status" != '200' ]]; then
  report 'RouterHealth' 'FAIL' "$started"
  exit 1
fi
report 'RouterHealth' 'PASS' "$started"

started=$SECONDS
login_payload="$(python3 -c '
import json
import os
print(json.dumps({"username": os.environ["NVIDIA_ROUTER_ADMIN_USERNAME"], "password": os.environ["NVIDIA_ROUTER_ADMIN_PASSWORD"]}), end="")
')"
if ! login_output="$(printf '%s' "$login_payload" | curl --silent \
  --request POST \
  --url "${NVIDIA_ROUTER_LIVE_BASE_URL}/admin/api/auth/login" \
  --header "Origin: ${admin_origin}" \
  --header 'Content-Type: application/json' \
  --data-binary @- \
  --dump-header - \
  --output /dev/null \
  --write-out $'\n%{http_code}')"; then
  report 'AdminLogin' 'FAIL' "$started"
  exit 1
fi
login_status="$(response_status "$login_output")"
admin_session="$(printf '%s' "$login_output" | python3 -c '
import sys
for line in sys.stdin.read().splitlines():
    if not line.lower().startswith("set-cookie:"):
        continue
    cookie = line.split(":", 1)[1].strip().split(";", 1)[0]
    name, separator, value = cookie.partition("=")
    if separator and name == "nvr_admin_session":
        print(value, end="")
        break
')"
unset login_payload login_output
if [[ "$login_status" != '200' || -z "$admin_session" ]]; then
  report 'AdminLogin' 'FAIL' "$started"
  exit 1
fi
report 'AdminLogin' 'PASS' "$started"

started=$SECONDS
if ! nvidia_response="$(admin_request GET '/admin/api/nvidia-keys' '')"; then
  report 'NVIDIAKeyAvailability' 'FAIL' "$started"
  exit 1
fi
nvidia_status="$(response_status "$nvidia_response")"
nvidia_body="$(response_body "$nvidia_response")"
if [[ "$nvidia_status" != '200' ]] || ! printf '%s' "$nvidia_body" | python3 -c '
import json
import sys
try:
    data = json.load(sys.stdin).get("data", [])
    raise SystemExit(0 if any(item.get("enabled") and not item.get("auth_invalid") for item in data) else 1)
except (ValueError, AttributeError, TypeError):
    raise SystemExit(1)
'; then
  unset nvidia_response nvidia_body
  report 'NVIDIAKeyAvailability' 'FAIL' "$started"
  exit 1
fi
unset nvidia_response nvidia_body NVIDIA_ROUTER_LIVE_KEY
report 'NVIDIAKeyAvailability' 'PASS' "$started"

started=$SECONDS
unset NVIDIA_ROUTER_LIVE_ASR_CAPABILITY_VERIFIED_AT NVIDIA_ROUTER_LIVE_TTS_CAPABILITY_VERIFIED_AT
model_response=''
model_body=''
if ! model_response="$(admin_request GET '/admin/api/models' '')"; then
  report 'ModelCapabilityGate' 'FAIL' "$started"
  exit 1
fi
model_status="$(response_status "$model_response")"
model_body="$(response_body "$model_response")"
capability_values="$(printf '%s' "$model_body" | python3 -c '
import json
import os
import sys
try:
    models = json.load(sys.stdin).get("data", [])
except (ValueError, AttributeError, TypeError):
    raise SystemExit(1)
required = {
    "NVIDIA_ROUTER_LIVE_ASR_MODEL": "NVIDIA_ROUTER_LIVE_ASR_CAPABILITY_VERIFIED_AT",
    "NVIDIA_ROUTER_LIVE_TTS_MODEL": "NVIDIA_ROUTER_LIVE_TTS_CAPABILITY_VERIFIED_AT",
}
if not all(os.environ.get(name, "").strip() == "" for name in required):
    for model_env, timestamp_env in required.items():
        model_id = os.environ.get(model_env, "").strip()
        if not model_id:
            continue
        match = next((item for item in models if item.get("public_id") == model_id and item.get("enabled")), None)
        if match is not None and match.get("capability_verified_at"):
            print(timestamp_env + "=" + match["capability_verified_at"])
' 2>/dev/null)" || {
  unset model_response model_body capability_values
  report 'ModelCapabilityGate' 'FAIL' "$started"
  exit 1
}
if [[ "$model_status" != '200' ]]; then
  unset model_response model_body capability_values
  report 'ModelCapabilityGate' 'FAIL' "$started"
  exit 1
fi
while IFS='=' read -r capability_name capability_value; do
  [[ -n "$capability_name" && -n "$capability_value" ]] && export "$capability_name=$capability_value"
done <<< "$capability_values"
unset model_response model_body capability_values
report 'ModelCapabilityGate' 'PASS' "$started"

started=$SECONDS
if [[ -n "${NVIDIA_ROUTER_LIVE_ACCESS_KEY:-}" ]]; then
  report 'UseExistingAccessKey' 'PASS' "$started"
else
  access_name="live-nvidia-$(date -u +%Y%m%dT%H%M%SZ)-$$"
  access_payload="$(printf '%s' "$access_name" | python3 -c '
import json
import sys
print(json.dumps({"name": sys.stdin.read()}), end="")
')"
  if ! access_response="$(admin_request POST '/admin/api/access-keys' "$access_payload")"; then
    report 'CreateTemporaryAccessKey' 'FAIL' "$started"
    exit 1
  fi
  access_status="$(response_status "$access_response")"
  access_body="$(response_body "$access_response")"
  mapfile -t access_values < <(printf '%s' "$access_body" | python3 -c '
import json
import sys
try:
    value = json.load(sys.stdin)
    print(value.get("id", ""))
    print(value.get("key", ""))
except (ValueError, AttributeError):
    pass
')
  unset access_response access_body access_payload
  if [[ "$access_status" != '201' || ${#access_values[@]} -ne 2 || -z "${access_values[0]}" || -z "${access_values[1]}" ]]; then
    report 'CreateTemporaryAccessKey' 'FAIL' "$started"
    exit 1
  fi
  temporary_access_id="${access_values[0]}"
  temporary_access_key="${access_values[1]}"
  unset access_values
  export NVIDIA_ROUTER_LIVE_ACCESS_KEY="$temporary_access_key"
  report 'CreateTemporaryAccessKey' 'PASS' "$started"
fi

started=$SECONDS
set +e
go test -tags=live ./tests/live -v
live_status=$?
set -e
unset NVIDIA_ROUTER_LIVE_ACCESS_KEY temporary_access_key
if (( live_status == 0 )); then
  report 'LiveGoTest' 'PASS' "$started"
else
  report 'LiveGoTest' 'FAIL' "$started"
  exit "$live_status"
fi
