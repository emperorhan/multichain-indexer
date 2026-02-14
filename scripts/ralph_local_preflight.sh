#!/usr/bin/env bash
set -euo pipefail

# Preflight checks for portable Ralph local loop setup.
# Usage:
#   scripts/ralph_local_preflight.sh

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${ROOT_DIR}"

SERVICE_NAME="${RALPH_LOCAL_SERVICE_NAME:-ralph-local.service}"
REQUIRE_CHATGPT_AUTH="${RALPH_REQUIRE_CHATGPT_AUTH:-true}"
CONNECTIVITY_TIMEOUT_SEC="${RALPH_CONNECTIVITY_TIMEOUT_SEC:-20}"
MODEL="${PLANNING_CODEX_MODEL:-gpt-5.3-codex}"

ok=0
warn=0
fail=0

pass() { echo "[PASS] $*"; ok=$((ok + 1)); }
note() { echo "[WARN] $*"; warn=$((warn + 1)); }
nope() { echo "[FAIL] $*"; fail=$((fail + 1)); }

have_cmd() {
  command -v "$1" >/dev/null 2>&1
}

check_cmd() {
  local cmd="$1"
  local required="${2:-true}"
  if have_cmd "${cmd}"; then
    pass "command available: ${cmd} ($(command -v "${cmd}"))"
    return 0
  fi
  if [ "${required}" = "true" ]; then
    nope "required command missing: ${cmd}"
  else
    note "optional command missing: ${cmd}"
  fi
}

echo "## Ralph Preflight"
echo "- time_utc: $(date -u +'%Y-%m-%d %H:%M:%S UTC')"
echo "- root: ${ROOT_DIR}"
echo

check_cmd bash true
check_cmd git true
check_cmd codex true
check_cmd make true
check_cmd awk true
check_cmd flock true
check_cmd jq true
check_cmd systemctl true
check_cmd journalctl true
check_cmd node true
check_cmd npm true
check_cmd rg false

echo
systemd_check_output="$(systemctl --user is-active default.target 2>&1 || true)"
if [ "${systemd_check_output}" = "active" ] || [ "${systemd_check_output}" = "inactive" ] || [ "${systemd_check_output}" = "failed" ]; then
  pass "systemd user bus reachable"
elif printf '%s' "${systemd_check_output}" | grep -qi "Failed to connect to bus: Operation not permitted"; then
  note "systemd user bus check blocked by restricted environment; verify from host shell"
else
  nope "systemd user bus is not reachable (${systemd_check_output})"
fi

if [ -f "${HOME}/.config/systemd/user/${SERVICE_NAME}" ]; then
  pass "service file exists: ${HOME}/.config/systemd/user/${SERVICE_NAME}"
else
  note "service file missing: ${HOME}/.config/systemd/user/${SERVICE_NAME} (run scripts/install_ralph_local_service.sh)"
fi

if [ -x scripts/codex_auth_status.sh ]; then
  if [ "${REQUIRE_CHATGPT_AUTH}" = "true" ]; then
    if scripts/codex_auth_status.sh --require-chatgpt >/dev/null 2>&1; then
      pass "codex auth mode is chatgpt"
    else
      nope "codex auth mode check failed (expected chatgpt login)"
    fi
  else
    pass "chatgpt auth strict check disabled"
  fi
else
  nope "missing scripts/codex_auth_status.sh"
fi

echo
if [ -x scripts/ralph_local_systemd_entry.sh ]; then
  if bash -n scripts/ralph_local_systemd_entry.sh; then
    pass "systemd entry script syntax valid"
  else
    nope "systemd entry script syntax invalid"
  fi
else
  nope "missing scripts/ralph_local_systemd_entry.sh"
fi

if [ -x scripts/ralph_local_run.sh ]; then
  if bash -n scripts/ralph_local_run.sh; then
    pass "runner script syntax valid"
  else
    nope "runner script syntax invalid"
  fi
else
  nope "missing scripts/ralph_local_run.sh"
fi

if [ -x scripts/ralph_issue_contract.sh ]; then
  if scripts/ralph_issue_contract.sh validate .ralph/issue-template.md developer >/dev/null 2>&1; then
    pass "issue contract gate works on template"
  else
    note "issue contract gate check failed on .ralph/issue-template.md"
  fi
fi

if have_cmd codex; then
  smoke_log="$(mktemp /tmp/ralph-preflight-codex-XXXX.log)"
  set +e
  timeout "${CONNECTIVITY_TIMEOUT_SEC}" \
    codex --ask-for-approval never exec --model "${MODEL}" --sandbox workspace-write --cd "$(pwd)" \
    "Reply exactly: ok" >"${smoke_log}" 2>&1
  rc=$?
  set -e
  if [ "${rc}" -eq 0 ]; then
    pass "codex connectivity smoke passed"
  else
    note "codex connectivity smoke failed rc=${rc} (log=${smoke_log})"
  fi
fi

echo
echo "## Summary"
echo "- pass: ${ok}"
echo "- warn: ${warn}"
echo "- fail: ${fail}"

if [ "${fail}" -gt 0 ]; then
  exit 1
fi
