#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${ROOT_DIR}"

detect_codex_bin_dir() {
  local codex_path
  codex_path="$(command -v codex 2>/dev/null || true)"
  if [ -n "${codex_path}" ]; then
    dirname "${codex_path}"
    return 0
  fi
  return 1
}

detect_latest_nvm_node_bin() {
  local latest
  latest="$(ls -d "${HOME}"/.nvm/versions/node/*/bin 2>/dev/null | sort -V | tail -n1 || true)"
  [ -n "${latest}" ] || return 1
  echo "${latest}"
}

build_path() {
  local codex_bin="" nvm_bin=""
  codex_bin="$(detect_codex_bin_dir || true)"
  nvm_bin="$(detect_latest_nvm_node_bin || true)"

  cat <<EOF
/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin:${HOME}/.local/bin:${codex_bin}:${nvm_bin}
EOF
}

export PATH="$(build_path)"
export RALPH_LOCAL_TRUST_MODE="${RALPH_LOCAL_TRUST_MODE:-false}"
export RALPH_LOCAL_SANDBOX="${RALPH_LOCAL_SANDBOX:-workspace-write}"
export RALPH_REQUIRE_CHATGPT_AUTH="${RALPH_REQUIRE_CHATGPT_AUTH:-true}"
export RALPH_CONNECTIVITY_PREFLIGHT="${RALPH_CONNECTIVITY_PREFLIGHT:-true}"
export RALPH_VALIDATE_CMD="${RALPH_VALIDATE_CMD:-make test && make test-sidecar && make lint}"
export RALPH_AUTO_PUBLISH_ENABLED="${RALPH_AUTO_PUBLISH_ENABLED:-true}"
export RALPH_AUTO_PUBLISH_MIN_COMMITS="${RALPH_AUTO_PUBLISH_MIN_COMMITS:-1}"
export RALPH_BRANCH_STRATEGY="${RALPH_BRANCH_STRATEGY:-main}"
export RALPH_STRICT_CONTRACT_GATE="${RALPH_STRICT_CONTRACT_GATE:-true}"
export RALPH_RISK_SCORE_COMPLEX_THRESHOLD="${RALPH_RISK_SCORE_COMPLEX_THRESHOLD:-7}"
export GOCACHE="${GOCACHE:-/tmp/go-build}"
export GOLANGCI_LINT_CACHE="${GOLANGCI_LINT_CACHE:-/tmp/golangci-lint-cache}"

mkdir -p /tmp/go-build /tmp/golangci-lint-cache .ralph/logs

if ! command -v codex >/dev/null 2>&1; then
  echo "ralph-local systemd entry: codex command not found in PATH=${PATH}" >&2
  exit 127
fi
if ! command -v git >/dev/null 2>&1; then
  echo "ralph-local systemd entry: git command not found in PATH=${PATH}" >&2
  exit 127
fi

scripts/ralph_local_control.sh on >/dev/null

exec scripts/ralph_local_supervisor.sh
