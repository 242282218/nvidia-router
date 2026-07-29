#!/usr/bin/env bash

set -eu

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
script="$script_dir/check-dev-env.sh"
temp_dir="$(mktemp -d)"
trap 'rm -rf "$temp_dir"' EXIT

for command_name in git pnpm; do
  cat >"$temp_dir/$command_name" <<'EOF'
#!/usr/bin/env bash
exit 0
EOF
  chmod +x "$temp_dir/$command_name"
done

go_path="$temp_dir/go-path"
mkdir -p "$go_path/bin"

cat >"$temp_dir/go" <<EOF
#!/usr/bin/env bash
if [[ "\$1" == "env" && "\$2" == "GOVERSION" ]]; then
  printf 'go1.26.3\\n'
elif [[ "\$1" == "env" && "\$2" == "GOPATH" ]]; then
  printf '%s\\n' '$go_path'
fi
EOF
chmod +x "$temp_dir/go"

cat >"$go_path/bin/golangci-lint" <<'EOF'
#!/usr/bin/env bash
if [[ "$1" == "version" && "$2" == "--short" ]]; then
  printf '2.4.0\n'
fi
EOF
chmod +x "$go_path/bin/golangci-lint"

cat >"$temp_dir/node" <<'EOF'
#!/usr/bin/env bash
printf 'v%s\n' "$NODE_VERSION"
EOF
chmod +x "$temp_dir/node"

assert_supported() {
  if ! NODE_VERSION="$1" PATH="$temp_dir:$PATH" bash "$script" >/dev/null; then
    printf 'expected Node.js %s to be supported\n' "$1" >&2
    exit 1
  fi
}

assert_unsupported() {
  if NODE_VERSION="$1" PATH="$temp_dir:$PATH" bash "$script" >/dev/null 2>&1; then
    printf 'expected Node.js %s to be unsupported\n' "$1" >&2
    exit 1
  fi
}

assert_unsupported 20.18.0
assert_supported 20.19.0
assert_unsupported 22.11.0
assert_supported 22.12.0
assert_supported 24.0.0
