#!/usr/bin/env bash
set -euo pipefail

ISSUE_NUMBER="${AGENT_ISSUE_NUMBER:-}"
ISSUE_TITLE="${AGENT_ISSUE_TITLE:-}"
ISSUE_URL="${AGENT_ISSUE_URL:-}"
ISSUE_BODY_FILE="${AGENT_ISSUE_BODY_FILE:-}"

if [ -z "${ISSUE_NUMBER}" ] || [ -z "${ISSUE_BODY_FILE}" ] || [ ! -f "${ISSUE_BODY_FILE}" ]; then
  echo "qa issue context is missing. expected AGENT_ISSUE_NUMBER and AGENT_ISSUE_BODY_FILE." >&2
  exit 3
fi

mkdir -p .agent
REPORT_FILE=".agent/qa-report-${ISSUE_NUMBER}.md"

QA_WATCHED_ADDRESSES="$(awk -F= '/^QA_WATCHED_ADDRESSES=/{print $2; exit}' "${ISSUE_BODY_FILE}" | tr -d '[:space:]')"
if [ -z "${QA_WATCHED_ADDRESSES}" ]; then
  cat >"${REPORT_FILE}" <<EOF
## QA Report (Issue #${ISSUE_NUMBER})

- title: ${ISSUE_TITLE}
- issue: ${ISSUE_URL}
- result: failed
- reason: QA_WATCHED_ADDRESSES not found in issue body
EOF
  exit 4
fi

address_is_valid() {
  local addr="$1"
  [[ "${addr}" =~ ^[1-9A-HJ-NP-Za-km-z]{32,44}$ ]]
}

invalid_count=0
IFS=',' read -r -a QA_ADDRS <<<"${QA_WATCHED_ADDRESSES}"
for addr in "${QA_ADDRS[@]}"; do
  if ! address_is_valid "${addr}"; then
    invalid_count=$((invalid_count + 1))
  fi
done

if [ "${invalid_count}" -gt 0 ]; then
  cat >"${REPORT_FILE}" <<EOF
## QA Report (Issue #${ISSUE_NUMBER})

- title: ${ISSUE_TITLE}
- issue: ${ISSUE_URL}
- result: failed
- reason: found ${invalid_count} invalid Solana address(es) in QA_WATCHED_ADDRESSES
- addresses: ${QA_WATCHED_ADDRESSES}
EOF
  exit 5
fi

export WATCHED_ADDRESSES="${QA_WATCHED_ADDRESSES}"

run_step() {
  local step_name="$1"
  shift
  local log_file="$1"
  shift

  if "$@" >"${log_file}" 2>&1; then
    echo "- ${step_name}: pass" >>"${REPORT_FILE}"
    return 0
  fi

  echo "- ${step_name}: fail" >>"${REPORT_FILE}"
  echo "" >>"${REPORT_FILE}"
  echo "### ${step_name} failure tail" >>"${REPORT_FILE}"
  echo "\`\`\`" >>"${REPORT_FILE}"
  tail -n 40 "${log_file}" >>"${REPORT_FILE}" || true
  echo "\`\`\`" >>"${REPORT_FILE}"
  return 1
}

cat >"${REPORT_FILE}" <<EOF
## QA Report (Issue #${ISSUE_NUMBER})

- title: ${ISSUE_TITLE}
- issue: ${ISSUE_URL}
- addresses: ${QA_WATCHED_ADDRESSES}
- result: running

## Checks
EOF

all_passed=true
run_step "Config parse test" ".agent/qa-config-${ISSUE_NUMBER}.log" go test ./internal/config -run TestLoadFromEnv -count=1 || all_passed=false
run_step "Go test suite" ".agent/qa-go-${ISSUE_NUMBER}.log" make test || all_passed=false
run_step "Sidecar test suite" ".agent/qa-sidecar-${ISSUE_NUMBER}.log" make test-sidecar || all_passed=false
run_step "Lint suite" ".agent/qa-lint-${ISSUE_NUMBER}.log" make lint || all_passed=false

if [ "${all_passed}" = "true" ]; then
  {
    echo ""
    echo "## Result"
    echo "- status: qa/passed"
  } >>"${REPORT_FILE}"
  exit 0
fi

{
  echo ""
  echo "## Result"
  echo "- status: qa/failed"
} >>"${REPORT_FILE}"

exit 1
