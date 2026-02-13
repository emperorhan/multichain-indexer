#!/usr/bin/env bash
set -euo pipefail

if ! command -v codex >/dev/null 2>&1; then
  echo "codex CLI is required on the runner." >&2
  exit 2
fi

ISSUE_NUMBER="${AGENT_ISSUE_NUMBER:-}"
ISSUE_TITLE="${AGENT_ISSUE_TITLE:-}"
ISSUE_URL="${AGENT_ISSUE_URL:-}"
ISSUE_BODY_FILE="${AGENT_ISSUE_BODY_FILE:-}"
ISSUE_LABELS="${AGENT_ISSUE_LABELS:-}"

if [ -z "${ISSUE_NUMBER}" ] || [ -z "${ISSUE_BODY_FILE}" ] || [ ! -f "${ISSUE_BODY_FILE}" ]; then
  echo "agent issue context is missing. expected AGENT_ISSUE_NUMBER and AGENT_ISSUE_BODY_FILE." >&2
  exit 3
fi

AGENT_CODEX_MODEL_OVERRIDE="${AGENT_CODEX_MODEL:-}"
AGENT_CODEX_MODEL_FAST="${AGENT_CODEX_MODEL_FAST:-gpt-5.3-codex-spark}"
AGENT_CODEX_MODEL_COMPLEX="${AGENT_CODEX_MODEL_COMPLEX:-gpt-5.3-codex}"
AGENT_CODEX_SANDBOX="${AGENT_CODEX_SANDBOX:-workspace-write}"
AGENT_CODEX_APPROVAL="${AGENT_CODEX_APPROVAL:-never}"
AGENT_CODEX_SEARCH="${AGENT_CODEX_SEARCH:-true}"

select_model() {
  if [ -n "${AGENT_CODEX_MODEL_OVERRIDE}" ]; then
    echo "${AGENT_CODEX_MODEL_OVERRIDE}"
    return 0
  fi

  # High-risk/high-priority issues use a stronger model; default path uses fast model.
  if [[ "${ISSUE_LABELS}" == *"risk/high"* ]] || [[ "${ISSUE_LABELS}" == *"priority/p0"* ]]; then
    echo "${AGENT_CODEX_MODEL_COMPLEX}"
    return 0
  fi

  echo "${AGENT_CODEX_MODEL_FAST}"
}

SELECTED_MODEL="$(select_model)"
echo "[agent-executor] issue=#${ISSUE_NUMBER} model=${SELECTED_MODEL}" >&2

PROMPT="$(cat <<EOF
You are the autonomous implementation executor for this repository.

GitHub issue:
- number: #${ISSUE_NUMBER}
- title: ${ISSUE_TITLE}
- url: ${ISSUE_URL}
- body file: ${ISSUE_BODY_FILE}

Instructions:
1. Read the issue body file and extract objective, in-scope, out-of-scope, risk, acceptance criteria.
2. Implement only in-scope changes.
3. Do not bypass risky or ambiguous decisions; if blocked by missing owner decision, exit non-zero.
4. Update docs/tests when behavior changes.
5. Run validation commands and leave the workspace in a committable state:
   - make test
   - make test-sidecar
   - make lint
EOF
)"

cmd=(
  codex
  --ask-for-approval "${AGENT_CODEX_APPROVAL}"
  exec
  --model "${SELECTED_MODEL}"
  --sandbox "${AGENT_CODEX_SANDBOX}"
  --cd "$(pwd)"
)

if [ "${AGENT_CODEX_SEARCH}" = "true" ]; then
  cmd+=(--search)
fi

"${cmd[@]}" "${PROMPT}"
