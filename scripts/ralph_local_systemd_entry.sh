#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${ROOT_DIR}"

export PATH="${HOME}/.local/bin:${HOME}/.nvm/versions/node/v24.13.0/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"
export RALPH_LOCAL_TRUST_MODE="${RALPH_LOCAL_TRUST_MODE:-false}"
export RALPH_LOCAL_SANDBOX="${RALPH_LOCAL_SANDBOX:-workspace-write}"
export RALPH_REQUIRE_CHATGPT_AUTH="${RALPH_REQUIRE_CHATGPT_AUTH:-true}"
export RALPH_CONNECTIVITY_PREFLIGHT="${RALPH_CONNECTIVITY_PREFLIGHT:-true}"
export RALPH_VALIDATE_CMD="${RALPH_VALIDATE_CMD:-make test && make test-sidecar && make lint}"
export GOCACHE="${GOCACHE:-/tmp/go-build}"
export GOLANGCI_LINT_CACHE="${GOLANGCI_LINT_CACHE:-/tmp/golangci-lint-cache}"

mkdir -p /tmp/go-build /tmp/golangci-lint-cache .ralph/logs

scripts/ralph_local_control.sh on >/dev/null

exec scripts/ralph_local_supervisor.sh
