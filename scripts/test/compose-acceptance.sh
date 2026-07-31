#!/usr/bin/env bash
set -Eeuo pipefail

umask 077

project="nvr-acceptance-$$"
override_file=''
cookie_jar=''
initial_login_file=''
change_password_file=''
access_create_file=''
access_response_file=''
list_response_file=''
new_password=''
base_url=''

compose() {
  docker compose -p "$project" -f docker-compose.yml -f "$override_file" "$@"
}

remove_temp_file() {
  local path="$1"
  if [[ -n "$path" ]]; then
    rm -f -- "$path" || true
  fi
}

write_override_file() {
  local path="$1"

  printf '%s\n' \
    'services:' \
    '  app:' \
    '    ports: !override' \
    '      - "127.0.0.1::3756"' >"$path"
}

refresh_base_url() {
  local port_output
  local host_port

  if ! port_output="$(compose port app 3756)"; then
    printf 'Compose did not return the temporary port.\n' >&2
    return 1
  fi
  port_output="${port_output%$'\r'}"
  if [[ ! "$port_output" =~ ^127\.0\.0\.1:([1-9][0-9]{0,4})$ ]]; then
    printf 'Compose did not expose one valid loopback port.\n' >&2
    return 1
  fi
  host_port="${BASH_REMATCH[1]}"
  if (( host_port > 65535 )); then
    printf 'Compose exposed an invalid temporary port.\n' >&2
    return 1
  fi
  base_url="http://127.0.0.1:${host_port}"
}

validate_compose_config() {
  if ! compose config --format json | python3 -c '
import json
import sys

try:
    ports = json.load(sys.stdin)["services"]["app"]["ports"]
    if len(ports) != 1:
        raise ValueError
    port = ports[0]
    if (
        port["target"] != 3756
        or port.get("host_ip") != "127.0.0.1"
        or port.get("published") not in (None, "", "0", 0)
    ):
        raise ValueError
except (KeyError, TypeError, ValueError):
    raise SystemExit(1)
'; then
    printf 'Compose override did not isolate the temporary port.\n' >&2
    return 1
  fi
}

run_self_test() {
  local fake_port_output='127.0.0.1:41001'
  local test_override

  docker() {
    printf '%s\n' "$fake_port_output"
  }

  refresh_base_url
  [[ "$base_url" == 'http://127.0.0.1:41001' ]]
  fake_port_output='127.0.0.1:41002'
  refresh_base_url
  [[ "$base_url" == 'http://127.0.0.1:41002' ]]

  fake_port_output=$'127.0.0.1:41003\n127.0.0.1:41004'
  if refresh_base_url 2>/dev/null; then
    printf 'Multiple Compose port bindings were accepted.\n' >&2
    return 1
  fi

  test_override="$(mktemp "${TMPDIR:-/tmp}/nvidia-router-compose-self-test.XXXXXX")"
  write_override_file "$test_override"
  if ! grep -qx '    ports: !override' "$test_override"; then
    printf 'Compose override did not replace the base ports.\n' >&2
    rm -f -- "$test_override"
    return 1
  fi
  rm -f -- "$test_override"
  printf 'Compose acceptance self-test passed.\n'
}

if [[ "${NVIDIA_ROUTER_COMPOSE_SELF_TEST:-0}" == '1' ]]; then
  run_self_test
  exit
fi

cleanup() {
  local status=$?

  trap - EXIT INT TERM
  if (( status != 0 )) && [[ -n "$override_file" ]]; then
    compose ps >&2 || true
  fi
  if [[ -n "$override_file" ]]; then
    compose down -v --remove-orphans >/dev/null 2>&1 || true
  fi

  remove_temp_file "$override_file"
  remove_temp_file "$cookie_jar"
  remove_temp_file "$initial_login_file"
  remove_temp_file "$change_password_file"
  remove_temp_file "$access_create_file"
  remove_temp_file "$access_response_file"
  remove_temp_file "$list_response_file"
  unset NVIDIA_ROUTER_MASTER_KEY new_password
  exit "$status"
}

trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

for dependency in docker curl openssl python3 mktemp chmod; do
  if ! command -v "$dependency" >/dev/null 2>&1; then
    printf 'Missing dependency: %s\n' "$dependency" >&2
    exit 1
  fi
done

repo_root="$(git rev-parse --show-toplevel)"
cd "$repo_root"

override_file="$(mktemp "${TMPDIR:-/tmp}/nvidia-router-compose.XXXXXX")"
chmod 600 "$override_file"
write_override_file "$override_file"

NVIDIA_ROUTER_MASTER_KEY="$(openssl rand -base64 32 | tr '+/' '-_' | tr -d '=')"
export NVIDIA_ROUTER_MASTER_KEY
validate_compose_config
compose build >/dev/null
compose up -d --wait >/dev/null
refresh_base_url

if ! curl --fail --silent --show-error --max-time 5 "$base_url/health/live" >/dev/null; then
  printf 'Live health check failed.\n' >&2
  exit 1
fi
ready_status="$(curl --silent --show-error --max-time 5 --output /dev/null --write-out '%{http_code}' "$base_url/health/ready")"
if [[ "$ready_status" == '200' ]]; then
  printf 'Ready unexpectedly passed before the initial password change.\n' >&2
  exit 1
fi

container_id="$(compose ps -q app)"
if [[ -z "$container_id" ]]; then
  printf 'Compose did not return an app container.\n' >&2
  exit 1
fi
docker inspect "$container_id" | python3 -c '
import json
import sys

try:
    item = json.load(sys.stdin)[0]
    config = item["Config"]
    host = item["HostConfig"]
    if (
        config["User"] != "10001:10001"
        or host["ReadonlyRootfs"] is not True
        or "ALL" not in host["CapDrop"]
        or "no-new-privileges:true" not in host["SecurityOpt"]
        or config["Healthcheck"] is None
        or config["StopTimeout"] != 600
    ):
        raise ValueError
except (KeyError, IndexError, TypeError, ValueError):
    raise SystemExit(1)
'

cookie_jar="$(mktemp "${TMPDIR:-/tmp}/nvidia-router-cookie.XXXXXX")"
initial_login_file="$(mktemp "${TMPDIR:-/tmp}/nvidia-router-login.XXXXXX")"
change_password_file="$(mktemp "${TMPDIR:-/tmp}/nvidia-router-change.XXXXXX")"
access_create_file="$(mktemp "${TMPDIR:-/tmp}/nvidia-router-access.XXXXXX")"
access_response_file="$(mktemp "${TMPDIR:-/tmp}/nvidia-router-access-response.XXXXXX")"
chmod 600 "$cookie_jar" "$initial_login_file" "$change_password_file" "$access_create_file" "$access_response_file"

printf '%s' '{"username":"admin","password":"admin"}' >"$initial_login_file"
login_status="$(curl --silent --show-error --max-time 10 --output /dev/null --write-out '%{http_code}' \
  --request POST --url "$base_url/admin/api/auth/login" \
  --header "Origin: $base_url" --header 'Content-Type: application/json' \
  --cookie-jar "$cookie_jar" --data-binary "@$initial_login_file")"
if [[ "$login_status" != '200' ]]; then
  printf 'Initial administrator login failed.\n' >&2
  exit 1
fi

new_password="Nvr$(openssl rand -hex 16)Password"
NVR_ACCEPTANCE_CURRENT_PASSWORD='admin' \
NVR_ACCEPTANCE_NEW_PASSWORD="$new_password" \
python3 - "$change_password_file" <<'PY'
import json
import os
import sys

with open(sys.argv[1], "w", encoding="utf-8") as stream:
    json.dump({
        "current_password": os.environ["NVR_ACCEPTANCE_CURRENT_PASSWORD"],
        "new_password": os.environ["NVR_ACCEPTANCE_NEW_PASSWORD"],
    }, stream, separators=(",", ":"))
PY
change_status="$(curl --silent --show-error --max-time 10 --output /dev/null --write-out '%{http_code}' \
  --request POST --url "$base_url/admin/api/auth/change-password" \
  --header "Origin: $base_url" --header 'Content-Type: application/json' \
  --cookie "$cookie_jar" --cookie-jar "$cookie_jar" --data-binary "@$change_password_file")"
if [[ "$change_status" != '200' ]]; then
  printf 'Initial administrator password change failed.\n' >&2
  exit 1
fi
rm -f -- "$initial_login_file" "$change_password_file"
initial_login_file=''
change_password_file=''

marker="compose-acceptance-${project}"
python3 - "$access_create_file" "$marker" <<'PY'
import json
import sys

with open(sys.argv[1], "w", encoding="utf-8") as stream:
    json.dump({"name": sys.argv[2]}, stream, separators=(",", ":"))
PY
create_status="$(curl --silent --show-error --max-time 10 --output "$access_response_file" --write-out '%{http_code}' \
  --request POST --url "$base_url/admin/api/access-keys" \
  --header "Origin: $base_url" --header 'Content-Type: application/json' \
  --cookie "$cookie_jar" --data-binary "@$access_create_file")"
if [[ "$create_status" != '201' ]]; then
  printf 'Access Key creation failed.\n' >&2
  exit 1
fi
access_id="$(python3 - "$access_response_file" "$marker" <<'PY'
import json
import sys

try:
    with open(sys.argv[1], encoding="utf-8") as stream:
        value = json.load(stream)
    if value.get("name") != sys.argv[2] or not isinstance(value.get("id"), int) or value["id"] <= 0:
        raise SystemExit(1)
    print(value["id"], end="")
except (OSError, TypeError, ValueError, KeyError):
    raise SystemExit(1)
PY
)" || {
  printf 'Access Key creation response was invalid.\n' >&2
  exit 1
}
rm -f -- "$access_create_file" "$access_response_file"
access_create_file=''
access_response_file=''

compose down --remove-orphans >/dev/null
compose up -d --wait >/dev/null
refresh_base_url
if ! curl --fail --silent --show-error --max-time 5 "$base_url/health/live" >/dev/null; then
  printf 'Live health check failed after restart.\n' >&2
  exit 1
fi

rm -f -- "$cookie_jar"
cookie_jar="$(mktemp "${TMPDIR:-/tmp}/nvidia-router-cookie.XXXXXX")"
initial_login_file="$(mktemp "${TMPDIR:-/tmp}/nvidia-router-login.XXXXXX")"
chmod 600 "$cookie_jar" "$initial_login_file"
printf '%s' "{\"username\":\"admin\",\"password\":\"$new_password\"}" >"$initial_login_file"
login_status="$(curl --silent --show-error --max-time 10 --output /dev/null --write-out '%{http_code}' \
  --request POST --url "$base_url/admin/api/auth/login" \
  --header "Origin: $base_url" --header 'Content-Type: application/json' \
  --cookie-jar "$cookie_jar" --data-binary "@$initial_login_file")"
if [[ "$login_status" != '200' ]]; then
  printf 'Administrator re-login failed after restart.\n' >&2
  exit 1
fi

list_response_file="$(mktemp "${TMPDIR:-/tmp}/nvidia-router-access-list.XXXXXX")"
chmod 600 "$list_response_file"
list_status="$(curl --silent --show-error --max-time 10 --output "$list_response_file" --write-out '%{http_code}' \
  --url "$base_url/admin/api/access-keys" --header "Origin: $base_url" --cookie "$cookie_jar")"
if [[ "$list_status" != '200' ]] || ! python3 - "$list_response_file" "$marker" "$access_id" <<'PY'
import json
import sys

try:
    with open(sys.argv[1], encoding="utf-8") as stream:
        items = json.load(stream).get("data", [])
    matches = [item for item in items if item.get("name") == sys.argv[2] and item.get("id") == int(sys.argv[3])]
    if len(matches) != 1:
        raise SystemExit(1)
except (OSError, TypeError, ValueError, KeyError):
    raise SystemExit(1)
PY
then
  printf 'Access Key marker was not preserved across restart.\n' >&2
  exit 1
fi
rm -f -- "$list_response_file" "$initial_login_file"
initial_login_file=''
unset new_password

printf 'Compose acceptance passed.\n'
