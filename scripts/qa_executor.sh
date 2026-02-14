#!/usr/bin/env bash
set -euo pipefail

ISSUE_NUMBER="${AGENT_ISSUE_NUMBER:-}"
ISSUE_TITLE="${AGENT_ISSUE_TITLE:-}"
ISSUE_URL="${AGENT_ISSUE_URL:-}"
ISSUE_BODY_FILE="${AGENT_ISSUE_BODY_FILE:-}"
COUNTEREXAMPLE_CMD="${QA_COUNTEREXAMPLE_CMD:-scripts/qa_counterexample_checks.sh}"
INVARIANTS_CMD="${RALPH_INVARIANTS_CMD:-scripts/ralph_invariants.sh}"

if [ -z "${ISSUE_NUMBER}" ] || [ -z "${ISSUE_BODY_FILE}" ] || [ ! -f "${ISSUE_BODY_FILE}" ]; then
  echo "qa issue context is missing. expected AGENT_ISSUE_NUMBER and AGENT_ISSUE_BODY_FILE." >&2
  exit 3
fi

if [ ! -x "${COUNTEREXAMPLE_CMD}" ]; then
  echo "qa counterexample command is missing or not executable: ${COUNTEREXAMPLE_CMD}" >&2
  exit 3
fi
if [ ! -x "${INVARIANTS_CMD}" ]; then
  echo "invariants script is missing or not executable: ${INVARIANTS_CMD}" >&2
  exit 3
fi

mkdir -p .agent
REPORT_FILE=".agent/qa-report-${ISSUE_NUMBER}.md"

QA_CHAIN="$(awk -F= '/^QA_CHAIN=/{print $2; exit}' "${ISSUE_BODY_FILE}" | tr -d '[:space:]')"
if [ -z "${QA_CHAIN}" ]; then
  QA_CHAIN="solana-devnet"
fi
QA_CHAIN="$(tr '[:upper:]' '[:lower:]' <<<"${QA_CHAIN}")"

QA_WATCHED_ADDRESSES="$(awk -F= '/^QA_WATCHED_ADDRESSES=/{print $2; exit}' "${ISSUE_BODY_FILE}" | tr -d '[:space:]')"
DECLARED_INVARIANTS="$(awk -F': ' '$1=="invariants"{print $2; exit}' "${ISSUE_BODY_FILE}" | tr -d '[:space:]')"
if [ -z "${QA_WATCHED_ADDRESSES}" ]; then
  cat >"${REPORT_FILE}" <<EOF
## QA Report (Issue #${ISSUE_NUMBER})

- title: ${ISSUE_TITLE}
- issue: ${ISSUE_URL}
- chain: ${QA_CHAIN}
- result: failed
- reason: QA_WATCHED_ADDRESSES not found in issue body
EOF
  exit 4
fi

if [ -n "${DECLARED_INVARIANTS}" ]; then
  if ! "${INVARIANTS_CMD}" validate "${DECLARED_INVARIANTS}" >/dev/null 2>&1; then
    cat >"${REPORT_FILE}" <<EOF
## QA Report (Issue #${ISSUE_NUMBER})

- title: ${ISSUE_TITLE}
- issue: ${ISSUE_URL}
- chain: ${QA_CHAIN}
- result: failed
- reason: invalid invariants declaration in issue header
- invariants: ${DECLARED_INVARIANTS}
EOF
    exit 7
  fi
fi

address_is_valid_solana() {
  local addr="$1"
  [[ "${addr}" =~ ^[1-9A-HJ-NP-Za-km-z]{32,44}$ ]]
}

address_is_valid_evm() {
  local addr="$1"
  [[ "${addr}" =~ ^0x[0-9a-fA-F]{40}$ ]]
}

validate_address_for_chain() {
  local chain="$1"
  local addr="$2"
  case "${chain}" in
    solana|solana-devnet)
      address_is_valid_solana "${addr}"
      ;;
    base|base-sepolia|evm)
      address_is_valid_evm "${addr}"
      ;;
    *)
      return 2
      ;;
  esac
}

invalid_count=0
unsupported_chain=false
IFS=',' read -r -a QA_ADDRS <<<"${QA_WATCHED_ADDRESSES}"
for addr in "${QA_ADDRS[@]}"; do
  if validate_address_for_chain "${QA_CHAIN}" "${addr}"; then
    rc=0
  else
    rc=$?
  fi
  if [ "${rc}" -eq 2 ]; then
    unsupported_chain=true
    break
  fi
  if [ "${rc}" -ne 0 ]; then
    invalid_count=$((invalid_count + 1))
  fi
done

if [ "${unsupported_chain}" = "true" ]; then
  cat >"${REPORT_FILE}" <<EOF
## QA Report (Issue #${ISSUE_NUMBER})

- title: ${ISSUE_TITLE}
- issue: ${ISSUE_URL}
- chain: ${QA_CHAIN}
- result: failed
- reason: unsupported QA_CHAIN value (expected solana-devnet or base-sepolia)
EOF
  exit 5
fi

if [ "${invalid_count}" -gt 0 ]; then
  cat >"${REPORT_FILE}" <<EOF
## QA Report (Issue #${ISSUE_NUMBER})

- title: ${ISSUE_TITLE}
- issue: ${ISSUE_URL}
- chain: ${QA_CHAIN}
- result: failed
- reason: found ${invalid_count} invalid address(es) for chain ${QA_CHAIN} in QA_WATCHED_ADDRESSES
- addresses: ${QA_WATCHED_ADDRESSES}
EOF
  exit 6
fi

export WATCHED_ADDRESSES="${QA_WATCHED_ADDRESSES}"
export QA_CHAIN="${QA_CHAIN}"

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
- chain: ${QA_CHAIN}
- addresses: ${QA_WATCHED_ADDRESSES}
- invariants: ${DECLARED_INVARIANTS:-not-declared}
- result: running

## Checks
EOF

all_passed=true
run_step "Config parse test" ".agent/qa-config-${ISSUE_NUMBER}.log" go test ./internal/config -run TestLoadFromEnv -count=1 || all_passed=false
run_step "Counterexample invariant checks" ".agent/qa-counterexample-${ISSUE_NUMBER}.log" bash -lc "${COUNTEREXAMPLE_CMD}" || all_passed=false
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
