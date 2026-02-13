#!/usr/bin/env bash
set -euo pipefail

if ! command -v codex >/dev/null 2>&1; then
  echo "codex CLI is required for QA triage." >&2
  exit 2
fi

CODEX_SAFETY_GUARD_CMD="${CODEX_SAFETY_GUARD_CMD:-scripts/codex_safety_guard.sh}"
if [ ! -x "${CODEX_SAFETY_GUARD_CMD}" ]; then
  echo "codex safety guard script is missing or not executable: ${CODEX_SAFETY_GUARD_CMD}" >&2
  exit 2
fi

ISSUE_NUMBER="${AGENT_ISSUE_NUMBER:-}"
ISSUE_TITLE="${AGENT_ISSUE_TITLE:-}"
ISSUE_URL="${AGENT_ISSUE_URL:-}"
REPORT_FILE="${QA_REPORT_FILE:-}"

if [ -z "${ISSUE_NUMBER}" ] || [ -z "${REPORT_FILE}" ] || [ ! -f "${REPORT_FILE}" ]; then
  echo "qa triage context missing. expected AGENT_ISSUE_NUMBER and QA_REPORT_FILE." >&2
  exit 3
fi

QA_TRIAGE_CODEX_MODEL="${QA_TRIAGE_CODEX_MODEL:-gpt-5.3-codex}"
QA_TRIAGE_CODEX_SANDBOX="${QA_TRIAGE_CODEX_SANDBOX:-workspace-write}"
QA_TRIAGE_CODEX_APPROVAL="${QA_TRIAGE_CODEX_APPROVAL:-never}"
QA_TRIAGE_CODEX_SEARCH="${QA_TRIAGE_CODEX_SEARCH:-false}"

supports_codex_search() {
  codex --help 2>/dev/null | grep -q -- "--search"
}

TRIAGE_FILE=".agent/qa-triage-${ISSUE_NUMBER}.md"
PROMPT="$(cat <<EOF
You are a QA triage assistant for a Solana on-chain indexer repository.

Source QA issue:
- number: #${ISSUE_NUMBER}
- title: ${ISSUE_TITLE}
- url: ${ISSUE_URL}

Failure report file:
- path: ${REPORT_FILE}

Task:
1. Read the report.
2. Infer likely root cause category (code bug / flaky test / workflow infra / env mismatch).
3. Provide concise reproduction hints.
4. Provide a prioritized fix hypothesis list (max 3).
5. Output in markdown with sections:
   - Triage Summary
   - Likely Root Cause
   - Reproduction Hints
   - Fix Hypotheses
EOF
)"

cmd=(
  codex
  --ask-for-approval "${QA_TRIAGE_CODEX_APPROVAL}"
)

if [ "${QA_TRIAGE_CODEX_SEARCH}" = "true" ] && supports_codex_search; then
  cmd+=(--search)
elif [ "${QA_TRIAGE_CODEX_SEARCH}" = "true" ]; then
  echo "[qa-triage] codex --search unsupported on this runner; continuing without search." >&2
fi

cmd+=(
  exec
  --model "${QA_TRIAGE_CODEX_MODEL}"
  --sandbox "${QA_TRIAGE_CODEX_SANDBOX}"
  --cd "$(pwd)"
)

"${CODEX_SAFETY_GUARD_CMD}" "${cmd[@]}"
"${cmd[@]}" "${PROMPT}" > "${TRIAGE_FILE}"
echo "${TRIAGE_FILE}"
