#!/usr/bin/env bash

set -u

status=0

pass() {
  printf 'PASS: %s\n' "$1"
}

fail() {
  printf 'FAIL: %s\n' "$1" >&2
  status=1
}

check_command() {
  local command_name="$1"
  local install_command="$2"

  if command -v "$command_name" >/dev/null 2>&1; then
    pass "$command_name"
  else
    fail "$command_name is required. Install with: $install_command"
  fi
}

check_node_version() {
  local version="$1"
  local install_command="$2"
  local major
  local remainder
  local minor

  major="${version%%.*}"
  remainder="${version#*.}"
  minor="${remainder%%.*}"
  if [[ "$major" =~ ^[0-9]+$ && "$minor" =~ ^[0-9]+$ ]] &&
    (( (major == 20 && minor >= 19) || major > 22 || (major == 22 && minor >= 12) )); then
    pass "Node.js $version"
  else
    fail "Node.js $version is unsupported; require ^20.19.0 || >=22.12.0. Install with: $install_command"
  fi
}

check_command git 'winget install --id Git.Git --version 2.52.0 --exact'
check_command pnpm 'corepack enable && corepack prepare pnpm@10.28.2 --activate'

if command -v go >/dev/null 2>&1; then
  go_version="${go_version:-$(go env GOVERSION)}"
  go_version="${go_version#go}"
  go_major="${go_version%%.*}"
  go_remainder="${go_version#*.}"
  go_minor="${go_remainder%%.*}"
  if [[ "$go_major" =~ ^[0-9]+$ && "$go_minor" =~ ^[0-9]+$ ]] &&
    (( go_major > 1 || (go_major == 1 && go_minor >= 24) )); then
    pass "Go $go_version"
  else
    fail "Go $go_version is unsupported; require 1.24+. Install with: winget install --id GoLang.Go --version 1.26.3 --exact"
  fi
else
  fail 'Go is required. Install with: winget install --id GoLang.Go --version 1.26.3 --exact'
fi

if command -v node >/dev/null 2>&1; then
  node_version="$(node --version)"
  check_node_version "${node_version#v}" 'winget install --id OpenJS.NodeJS --version 24.12.0 --exact'
else
  fail 'Node.js is required. Install with: winget install --id OpenJS.NodeJS --version 24.12.0 --exact'
fi

golangci_lint="$(command -v golangci-lint || true)"
if [[ -z "$golangci_lint" ]] && command -v go >/dev/null 2>&1; then
  golangci_lint="$(go env GOPATH)/bin/golangci-lint"
fi

if [[ -x "$golangci_lint" ]]; then
  pass "golangci-lint $($golangci_lint version --short 2>/dev/null || $golangci_lint --version)"
else
  fail 'golangci-lint is required. Install with: go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.4.0'
fi

exit "$status"
