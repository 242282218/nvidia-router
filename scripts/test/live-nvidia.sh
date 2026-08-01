#!/usr/bin/env bash

set -euo pipefail

umask 077

# Keep live smoke tests on the preferred model unless the caller overrides it.
export NVIDIA_ROUTER_LIVE_CHAT_MODEL="${NVIDIA_ROUTER_LIVE_CHAT_MODEL:-nvidia/minimaxai/minimax-m3}"
export NVIDIA_ROUTER_LIVE_RESPONSES_MODEL="${NVIDIA_ROUTER_LIVE_RESPONSES_MODEL:-$NVIDIA_ROUTER_LIVE_CHAT_MODEL}"

admin_session=''
temporary_access_id=''
temporary_access_key=''
temporary_nvidia_key_id=''
temporary_nvidia_key_owned=0
nvidia_key_id=''
live_log=''

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

validate_live_results() {
  local log_path="$1"
  python3 - "$log_path" <<'PY'
import re
import sys

required = {
    "Models", "ChatNonstream", "ChatStream", "ResponsesNonstream",
    "ResponsesStream", "Embedding", "ASR", "TTS",
}
records = {}
pattern = re.compile(r"^case=([^ ]+) status=(PASS|FAIL|SKIP) duration=([0-9]+(?:\.[0-9]+)?(?:ns|us|µs|ms|s|m|h))$")

try:
    with open(sys.argv[1], "r", encoding="utf-8", errors="replace") as stream:
        for line in stream:
            marker = line.find("case=")
            if marker < 0:
                continue
            match = pattern.fullmatch(line[marker:].rstrip("\r\n"))
            if match is None:
                raise ValueError("malformed case result")
            name, status, duration = match.groups()
            if name not in required or name in records:
                raise ValueError("unknown or duplicate case result")
            if name in required - {"ASR", "TTS"} and status != "PASS":
                raise ValueError("required case did not pass")
            if name in {"ASR", "TTS"} and status not in {"PASS", "SKIP"}:
                raise ValueError("optional audio case has invalid status")
            records[name] = (status, duration)
except (OSError, ValueError):
    raise SystemExit(1)

if set(records) != required:
    raise SystemExit(1)

for name in ("Models", "ChatNonstream", "ChatStream", "ResponsesNonstream", "ResponsesStream", "Embedding", "ASR", "TTS"):
    status, duration = records[name]
    print(f"case={name} status={status} duration={duration}")

if records["ASR"][0] == "SKIP" or records["TTS"][0] == "SKIP":
    raise SystemExit(2)
PY
}

live_result_status() {
  local parser_status="$1"
  local go_status="$2"

  if (( go_status != 0 || parser_status == 1 )); then
    return 1
  fi
  return "$parser_status"
}

run_live_parser_self_test() {
  local fixture
  local summary
  local result
  local expected
  local actual

  fixture="$(mktemp "${TMPDIR:-/tmp}/nvidia-router-live-fixture.XXXXXX")"
  summary="$(mktemp "${TMPDIR:-/tmp}/nvidia-router-live-summary.XXXXXX")"
  chmod 600 "$fixture" "$summary"
  trap 'rm -f -- "$fixture" "$summary"' RETURN

  assert_result() {
    expected="$1"
    result="$2"
    printf '%s\n' "$result" >"$fixture"
    set +e
    validate_live_results "$fixture" >"$summary"
    actual=$?
    set -e
    if [[ "$actual" != "$expected" ]] || grep -Eq 'reason=|request|response|secret' "$summary"; then
      return 1
    fi
  }

  local all_pass='case=Models status=PASS duration=1ms
case=ChatNonstream status=PASS duration=1ms
case=ChatStream status=PASS duration=1ms
case=ResponsesNonstream status=PASS duration=1ms
case=ResponsesStream status=PASS duration=1ms
case=Embedding status=PASS duration=1ms
case=ASR status=PASS duration=1ms
case=TTS status=PASS duration=1ms'
  local all_skip='case=Models status=SKIP duration=1ms
case=ChatNonstream status=SKIP duration=1ms
case=ChatStream status=SKIP duration=1ms
case=ResponsesNonstream status=SKIP duration=1ms
case=ResponsesStream status=SKIP duration=1ms
case=Embedding status=SKIP duration=1ms
case=ASR status=SKIP duration=1ms
case=TTS status=SKIP duration=1ms'

  # The old exit-code-only rule incorrectly accepted this zero-exit fake output.
  if ! assert_result 1 "$all_skip"; then
    return 1
  fi
  if ! assert_result 0 "$all_pass"; then
    return 1
  fi
  if ! assert_result 2 "${all_pass/ASR status=PASS/ASR status=SKIP}"; then
    return 1
  fi
  if ! assert_result 1 "${all_pass/Embedding status=PASS/Embedding status=SKIP}"; then
    return 1
  fi
  if ! assert_result 1 "${all_pass/ChatStream status=PASS/ChatStream status=FAIL}"; then
    return 1
  fi
  if ! assert_result 1 "${all_pass/$'case=Models status=PASS duration=1ms\n'/case=Models status=PASS duration=1ms\ncase=Models status=PASS duration=1ms\n}"; then
    return 1
  fi
  if ! assert_result 1 "${all_pass/case=Embedding status=PASS/case=Embedding status=UNKNOWN}"; then
    return 1
  fi
  if ! assert_result 1 "${all_pass/case=TTS status=PASS/case=Other status=PASS}"; then
    return 1
  fi
  if ! assert_result 1 "${all_pass/$'case=Embedding status=PASS duration=1ms\n'/}"; then
    return 1
  fi
  if ! assert_result 2 "${all_pass/TTS status=PASS/TTS status=SKIP}"; then
    return 1
  fi
  if ! assert_result 1 'case=Configuration status=SKIP duration=0s reason=missing-config'; then
    return 1
  fi
  if ! assert_result 0 "$all_pass"; then
    return 1
  fi
  if live_result_status 0 1; then
    return 1
  fi

  printf 'case=LiveParserSelfTest status=PASS duration=0s\n'
}

validate_primary_models() {
  local body="$1"
  printf '%s' "$body" | \
    NVIDIA_ROUTER_LIVE_RESPONSES_MODEL="$responses_model" \
    python3 -c '
import json
import os
import sys

try:
    models = json.load(sys.stdin).get("data", [])
    targets = {
        "chat": os.environ.get("NVIDIA_ROUTER_LIVE_CHAT_MODEL", "").strip(),
        "responses": os.environ.get("NVIDIA_ROUTER_LIVE_RESPONSES_MODEL", "").strip(),
        "embedding": os.environ.get("NVIDIA_ROUTER_LIVE_EMBEDDING_MODEL", "").strip(),
    }
    for target in targets.values():
        matches = [item for item in models if item.get("public_id") == target]
        if len(matches) != 1 or matches[0].get("enabled") is not True:
            raise SystemExit(1)
except (ValueError, AttributeError, TypeError):
    raise SystemExit(1)
'
}

parse_audio_models() {
  local body="$1"
  printf '%s' "$body" | \
    NVIDIA_ROUTER_LIVE_ASR_MODEL="${NVIDIA_ROUTER_LIVE_ASR_MODEL:-}" \
    NVIDIA_ROUTER_LIVE_TTS_MODEL="${NVIDIA_ROUTER_LIVE_TTS_MODEL:-}" \
    python3 -c '
import json
import os
import sys

try:
    models = json.load(sys.stdin).get("data", [])
    for environment_name, expected_kind in (
        ("NVIDIA_ROUTER_LIVE_ASR_MODEL", "asr"),
        ("NVIDIA_ROUTER_LIVE_TTS_MODEL", "tts"),
    ):
        public_id = os.environ.get(environment_name, "").strip()
        if not public_id:
            continue
        matches = [
            item for item in models
            if item.get("public_id") == public_id and item.get("kind") == expected_kind
        ]
        if len(matches) != 1 or not isinstance(matches[0].get("id"), int) or matches[0]["id"] <= 0:
            raise SystemExit(1)
        item = matches[0]
        print(item["id"])
        print("1" if item.get("enabled") is True else "0")
        print(item.get("capability_verified_at") or "")
except (ValueError, AttributeError, TypeError):
    raise SystemExit(1)
'
}

verify_audio_model() {
  local model_id="$1"
  local payload
  local response
  local status
  local body
  local timestamp

  if ! payload="$(printf '%s' "$nvidia_key_id" | python3 -c '
import json
import sys
value = int(sys.stdin.read().strip())
print(json.dumps({"key_id": value}, separators=(",", ":")), end="")
')"; then
    return 1
  fi
  if ! response="$(admin_request POST "/admin/api/models/${model_id}/test" "$payload")"; then
    unset payload
    return 1
  fi
  status="$(response_status "$response")"
  body="$(response_body "$response")"
  timestamp="$(printf '%s' "$body" | python3 -c '
import json
import sys
try:
    value = json.load(sys.stdin).get("capability_verified_at")
    if not isinstance(value, str) or not value.strip():
        raise SystemExit(1)
    print(value, end="")
except (ValueError, AttributeError, TypeError):
    raise SystemExit(1)
' 2>/dev/null)" || timestamp=''
  unset payload response body
  if [[ "$status" != '200' || -z "$timestamp" ]]; then
    return 1
  fi
  printf '%s' "$timestamp"
}

enable_audio_model() {
  local model_id="$1"
  local payload
  local response
  local status

  payload="$(python3 -c 'import json; print(json.dumps({"enabled": True}, separators=(",", ":")), end="")')"
  if ! response="$(admin_request PATCH "/admin/api/models/${model_id}" "$payload")"; then
    unset payload
    return 1
  fi
  status="$(response_status "$response")"
  unset payload response
  [[ "$status" == '200' ]]
}

if [[ "${NVIDIA_ROUTER_LIVE_SELF_TEST:-0}" == '1' ]]; then
  run_live_parser_self_test
  exit $?
fi

cleanup() {
  local exit_code=$?
  local started
  local response
  local status

  trap - EXIT INT TERM

  if [[ -n "$temporary_access_id" && -n "$admin_session" ]]; then
    started=$SECONDS
    if response="$(admin_request DELETE "/admin/api/access-keys/${temporary_access_id}" '')"; then
      status="$(response_status "$response")"
      if [[ "$status" == '204' ]]; then
        report 'RevokeTemporaryAccessKey' 'PASS' "$started"
      else
        report 'RevokeTemporaryAccessKey' 'FAIL' "$started"
        exit_code=1
      fi
    else
      report 'RevokeTemporaryAccessKey' 'FAIL' "$started"
      exit_code=1
    fi
    unset response
  fi

  if [[ "$temporary_nvidia_key_owned" == '1' && -n "$temporary_nvidia_key_id" && -n "$admin_session" ]]; then
    started=$SECONDS
    if response="$(admin_request DELETE "/admin/api/nvidia-keys/${temporary_nvidia_key_id}" '')"; then
      status="$(response_status "$response")"
      if [[ "$status" == '204' ]]; then
        report 'DeleteTemporaryNVIDIAKey' 'PASS' "$started"
      else
        report 'DeleteTemporaryNVIDIAKey' 'FAIL' "$started"
        exit_code=1
      fi
    else
      report 'DeleteTemporaryNVIDIAKey' 'FAIL' "$started"
      exit_code=1
    fi
    unset response
  fi

  if [[ -n "$admin_session" ]]; then
    started=$SECONDS
    if response="$(admin_request POST '/admin/api/auth/logout' '')"; then
      status="$(response_status "$response")"
      if [[ "$status" == '204' ]]; then
        report 'AdminLogout' 'PASS' "$started"
      else
        report 'AdminLogout' 'FAIL' "$started"
        exit_code=1
      fi
    else
      report 'AdminLogout' 'FAIL' "$started"
      exit_code=1
    fi
    unset response
  fi

  if [[ -n "$live_log" ]]; then
    if ! rm -f -- "$live_log"; then
      exit_code=1
    fi
    live_log=''
  fi

  unset NVIDIA_ROUTER_ADMIN_PASSWORD NVIDIA_ROUTER_LIVE_KEY NVIDIA_ROUTER_LIVE_ACCESS_KEY \
    temporary_access_key temporary_nvidia_key_id admin_session nvidia_import_payload \
    login_payload
  exit "$exit_code"
}

trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

for dependency in git curl python3 go mktemp chmod grep; do
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
      -z "${NVIDIA_ROUTER_LIVE_KEY:-}" ||
      -z "${NVIDIA_ROUTER_LIVE_CHAT_MODEL:-}" ||
      -z "${NVIDIA_ROUTER_LIVE_EMBEDDING_MODEL:-}" ]]; then
  report 'Configuration' 'FAIL' "$started"
  exit 1
fi
NVIDIA_ROUTER_ADMIN_USERNAME="${NVIDIA_ROUTER_ADMIN_USERNAME:-admin}"
NVIDIA_ROUTER_LIVE_BASE_URL="${NVIDIA_ROUTER_LIVE_BASE_URL%/}"
responses_model="${NVIDIA_ROUTER_LIVE_RESPONSES_MODEL:-${NVIDIA_ROUTER_LIVE_CHAT_MODEL}}"
audio_requested=0
if [[ -n "${NVIDIA_ROUTER_LIVE_ASR_MODEL:-}" || -n "${NVIDIA_ROUTER_LIVE_TTS_MODEL:-}" ]]; then
  audio_requested=1
fi
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
  unset login_payload login_output NVIDIA_ROUTER_ADMIN_PASSWORD
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
unset login_payload login_output NVIDIA_ROUTER_ADMIN_PASSWORD
if [[ "$login_status" != '200' || -z "$admin_session" ]]; then
  report 'AdminLogin' 'FAIL' "$started"
  exit 1
fi
report 'AdminLogin' 'PASS' "$started"

started=$SECONDS
if ! nvidia_import_payload="$(python3 -c '
import json
import os
print(json.dumps({"key": os.environ["NVIDIA_ROUTER_LIVE_KEY"]}, separators=(",", ":")), end="")
')"; then
  unset NVIDIA_ROUTER_LIVE_KEY nvidia_import_payload
  report 'ImportNVIDIAKey' 'FAIL' "$started"
  exit 1
fi
if ! nvidia_import_response="$(admin_request POST '/admin/api/nvidia-keys' "$nvidia_import_payload")"; then
  unset NVIDIA_ROUTER_LIVE_KEY nvidia_import_payload
  report 'ImportNVIDIAKey' 'FAIL' "$started"
  exit 1
fi
unset NVIDIA_ROUTER_LIVE_KEY nvidia_import_payload
nvidia_import_status="$(response_status "$nvidia_import_response")"
nvidia_import_body="$(response_body "$nvidia_import_response")"
mapfile -t import_values < <(printf '%s' "$nvidia_import_body" | python3 -c '
import json
import sys
try:
    value = json.load(sys.stdin)
    print(value.get("status", ""))
    print(value.get("masked", ""))
    key = value.get("key")
    print(key.get("id", "") if isinstance(key, dict) else "")
except (ValueError, AttributeError, TypeError):
    pass
' 2>/dev/null)
unset nvidia_import_response nvidia_import_body
import_status="${import_values[0]:-}"
import_masked="${import_values[1]:-}"
import_key_id="${import_values[2]:-}"
unset import_values
if [[ "$nvidia_import_status" != '201' || "$import_status" != 'imported' && "$import_status" != 'duplicate' ]]; then
  report 'ImportNVIDIAKey' 'FAIL' "$started"
  exit 1
fi
if [[ "$import_status" == 'imported' ]]; then
  if [[ ! "$import_key_id" =~ ^[1-9][0-9]*$ ]]; then
    report 'ImportNVIDIAKey' 'FAIL' "$started"
    exit 1
  fi
  temporary_nvidia_key_id="$import_key_id"
  temporary_nvidia_key_owned=1
else
  if [[ -z "$import_masked" ]]; then
    report 'ImportNVIDIAKey' 'FAIL' "$started"
    exit 1
  fi
fi
report 'ImportNVIDIAKey' 'PASS' "$started"

started=$SECONDS
if ! nvidia_response="$(admin_request GET '/admin/api/nvidia-keys' '')"; then
  report 'NVIDIAKeyAvailability' 'FAIL' "$started"
  if (( audio_requested == 1 )); then
    report 'ModelCapabilityGate' 'FAIL' "$started"
  fi
  exit 1
fi
nvidia_status="$(response_status "$nvidia_response")"
nvidia_body="$(response_body "$nvidia_response")"
if [[ "$import_status" == 'duplicate' ]]; then
  nvidia_key_id="$(printf '%s' "$nvidia_body" | NVIDIA_ROUTER_LIVE_KEY_MASKED="$import_masked" python3 -c '
import json
import os
import sys
try:
    items = json.load(sys.stdin).get("data", [])
    matches = [
        item for item in items
        if item.get("masked") == os.environ.get("NVIDIA_ROUTER_LIVE_KEY_MASKED")
        and item.get("enabled") is True
        and item.get("auth_invalid") is False
    ]
    if len(matches) != 1:
        raise SystemExit(1)
    print(matches[0].get("id", ""), end="")
except (ValueError, AttributeError, TypeError):
    raise SystemExit(1)
' 2>/dev/null)" || nvidia_key_id=''
else
  nvidia_key_id="$(printf '%s' "$nvidia_body" | NVIDIA_ROUTER_LIVE_KEY_ID="$temporary_nvidia_key_id" python3 -c '
import json
import os
import sys
try:
    wanted = int(os.environ["NVIDIA_ROUTER_LIVE_KEY_ID"])
    items = json.load(sys.stdin).get("data", [])
    matches = [
        item for item in items
        if item.get("id") == wanted
        and item.get("enabled") is True
        and item.get("auth_invalid") is False
    ]
    if len(matches) != 1:
        raise SystemExit(1)
    print(wanted, end="")
except (ValueError, AttributeError, TypeError):
    raise SystemExit(1)
' 2>/dev/null)" || nvidia_key_id=''
fi
unset nvidia_response nvidia_body import_masked import_key_id NVIDIA_ROUTER_LIVE_KEY_MASKED NVIDIA_ROUTER_LIVE_KEY_ID
if [[ "$nvidia_status" != '200' || ! "$nvidia_key_id" =~ ^[1-9][0-9]*$ ]]; then
  report 'NVIDIAKeyAvailability' 'FAIL' "$started"
  if (( audio_requested == 1 )); then
    report 'ModelCapabilityGate' 'FAIL' "$started"
  fi
  exit 1
fi
report 'NVIDIAKeyAvailability' 'PASS' "$started"

started=$SECONDS
if ! model_response="$(admin_request GET '/admin/api/models' '')"; then
  report 'ModelAvailability' 'FAIL' "$started"
  if (( audio_requested == 1 )); then
    report 'ModelCapabilityGate' 'FAIL' "$started"
  fi
  exit 1
fi
model_status="$(response_status "$model_response")"
model_body="$(response_body "$model_response")"
if [[ "$model_status" != '200' ]] || ! validate_primary_models "$model_body" 2>/dev/null; then
  unset model_response model_body
  report 'ModelAvailability' 'FAIL' "$started"
  if (( audio_requested == 1 )); then
    report 'ModelCapabilityGate' 'FAIL' "$started"
  fi
  exit 1
fi
report 'ModelAvailability' 'PASS' "$started"

unset NVIDIA_ROUTER_LIVE_ASR_CAPABILITY_VERIFIED_AT NVIDIA_ROUTER_LIVE_TTS_CAPABILITY_VERIFIED_AT
if (( audio_requested == 0 )); then
  unset model_response model_body
  started=$SECONDS
  report 'ModelCapabilityGate' 'SKIP' "$started"
else
  capability_started=$SECONDS
  mapfile -t audio_values < <(parse_audio_models "$model_body" 2>/dev/null)
  audio_index=0
  asr_id=''
  asr_enabled=''
  asr_verified=''
  tts_id=''
  tts_enabled=''
  tts_verified=''
  if [[ -n "${NVIDIA_ROUTER_LIVE_ASR_MODEL:-}" ]]; then
    asr_id="${audio_values[$audio_index]:-}"
    asr_enabled="${audio_values[$((audio_index + 1))]:-}"
    asr_verified="${audio_values[$((audio_index + 2))]:-}"
    audio_index=$((audio_index + 3))
  fi
  if [[ -n "${NVIDIA_ROUTER_LIVE_TTS_MODEL:-}" ]]; then
    tts_id="${audio_values[$audio_index]:-}"
    tts_enabled="${audio_values[$((audio_index + 1))]:-}"
    tts_verified="${audio_values[$((audio_index + 2))]:-}"
    audio_index=$((audio_index + 3))
  fi
  unset audio_values
  if [[ -n "${NVIDIA_ROUTER_LIVE_ASR_MODEL:-}" && -z "$asr_id" ]] ||
     [[ -n "${NVIDIA_ROUTER_LIVE_TTS_MODEL:-}" && -z "$tts_id" ]]; then
    unset model_response model_body
    report 'ModelCapabilityGate' 'FAIL' "$capability_started"
    exit 1
  fi

  if [[ -n "$asr_id" && -z "$asr_verified" ]]; then
    if ! asr_verified="$(verify_audio_model "$asr_id")"; then
      unset model_response model_body
      report 'ModelCapabilityGate' 'FAIL' "$capability_started"
      exit 1
    fi
  fi
  if [[ -n "$tts_id" && -z "$tts_verified" ]]; then
    if ! tts_verified="$(verify_audio_model "$tts_id")"; then
      unset model_response model_body
      report 'ModelCapabilityGate' 'FAIL' "$capability_started"
      exit 1
    fi
  fi
  if [[ "$asr_enabled" == '0' && -n "$asr_id" ]]; then
    if ! enable_audio_model "$asr_id"; then
      unset model_response model_body
      report 'ModelCapabilityGate' 'FAIL' "$capability_started"
      exit 1
    fi
  fi
  if [[ "$tts_enabled" == '0' && -n "$tts_id" ]]; then
    if ! enable_audio_model "$tts_id"; then
      unset model_response model_body
      report 'ModelCapabilityGate' 'FAIL' "$capability_started"
      exit 1
    fi
  fi

  if ! final_model_response="$(admin_request GET '/admin/api/models' '')"; then
    unset model_response model_body
    report 'ModelCapabilityGate' 'FAIL' "$capability_started"
    exit 1
  fi
  final_model_status="$(response_status "$final_model_response")"
  final_model_body="$(response_body "$final_model_response")"
  if [[ "$final_model_status" != '200' ]] || ! validate_primary_models "$final_model_body" 2>/dev/null; then
    unset model_response model_body final_model_response final_model_body
    report 'ModelCapabilityGate' 'FAIL' "$capability_started"
    exit 1
  fi
  mapfile -t final_audio_values < <(parse_audio_models "$final_model_body" 2>/dev/null)
  audio_index=0
  if [[ -n "${NVIDIA_ROUTER_LIVE_ASR_MODEL:-}" ]]; then
    final_asr_id="${final_audio_values[$audio_index]:-}"
    final_asr_enabled="${final_audio_values[$((audio_index + 1))]:-}"
    final_asr_verified="${final_audio_values[$((audio_index + 2))]:-}"
    audio_index=$((audio_index + 3))
    if [[ "$final_asr_id" != "$asr_id" || "$final_asr_enabled" != '1' || -z "$final_asr_verified" ]]; then
      unset model_response model_body final_model_response final_model_body final_audio_values
      report 'ModelCapabilityGate' 'FAIL' "$capability_started"
      exit 1
    fi
    export NVIDIA_ROUTER_LIVE_ASR_CAPABILITY_VERIFIED_AT="$final_asr_verified"
  fi
  if [[ -n "${NVIDIA_ROUTER_LIVE_TTS_MODEL:-}" ]]; then
    final_tts_id="${final_audio_values[$audio_index]:-}"
    final_tts_enabled="${final_audio_values[$((audio_index + 1))]:-}"
    final_tts_verified="${final_audio_values[$((audio_index + 2))]:-}"
    if [[ "$final_tts_id" != "$tts_id" || "$final_tts_enabled" != '1' || -z "$final_tts_verified" ]]; then
      unset model_response model_body final_model_response final_model_body final_audio_values
      report 'ModelCapabilityGate' 'FAIL' "$capability_started"
      exit 1
    fi
    export NVIDIA_ROUTER_LIVE_TTS_CAPABILITY_VERIFIED_AT="$final_tts_verified"
  fi
  unset model_response model_body final_model_response final_model_body final_audio_values
  report 'ModelCapabilityGate' 'PASS' "$capability_started"
fi

started=$SECONDS
access_name="live-nvidia-$(date -u +%Y%m%dT%H%M%SZ)-$$"
access_payload="$(printf '%s' "$access_name" | python3 -c '
import json
import sys
print(json.dumps({"name": sys.stdin.read()}, separators=(",", ":")), end="")
')"
if ! access_response="$(admin_request POST '/admin/api/access-keys' "$access_payload")"; then
  unset access_name access_payload access_response
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
except (ValueError, AttributeError, TypeError):
    pass
' 2>/dev/null)
unset access_name access_payload access_response access_body
if [[ "$access_status" != '201' || ${#access_values[@]} -ne 2 ||
      -z "${access_values[0]:-}" || -z "${access_values[1]:-}" ]]; then
  unset access_values
  report 'CreateTemporaryAccessKey' 'FAIL' "$started"
  exit 1
fi
temporary_access_id="${access_values[0]}"
temporary_access_key="${access_values[1]}"
unset access_values
if [[ ! "$temporary_access_id" =~ ^[1-9][0-9]*$ ]]; then
  report 'CreateTemporaryAccessKey' 'FAIL' "$started"
  exit 1
fi
export NVIDIA_ROUTER_LIVE_ACCESS_KEY="$temporary_access_key"
report 'CreateTemporaryAccessKey' 'PASS' "$started"

started=$SECONDS
live_log="$(mktemp "${TMPDIR:-/tmp}/nvidia-router-live.XXXXXX")"
chmod 600 -- "$live_log"
set +e
go test -tags=live ./tests/live -v >"$live_log" 2>&1
live_status=$?
set -e
set +e
validate_live_results "$live_log"
parser_status=$?
set -e
unset live_cases NVIDIA_ROUTER_LIVE_ACCESS_KEY temporary_access_key
rm -f -- "$live_log"
live_log=''
set +e
live_status_result=0
live_result_status "$parser_status" "$live_status"
live_status_result=$?
set -e
if (( live_status_result == 0 )); then
  report 'LiveGoTest' 'PASS' "$started"
elif (( live_status_result == 2 )); then
  report 'LiveGoTest' 'INCOMPLETE' "$started"
  exit 2
else
  report 'LiveGoTest' 'FAIL' "$started"
  exit 1
fi
