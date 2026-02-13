#!/usr/bin/env bash
set -euo pipefail

if ! command -v codex >/dev/null 2>&1; then
  echo "codex CLI is required on the runner." >&2
  exit 2
fi

MODEL_ROUTER_CMD="${MODEL_ROUTER_CMD:-scripts/codex_model_router.sh}"
CODEX_SAFETY_GUARD_CMD="${CODEX_SAFETY_GUARD_CMD:-scripts/codex_safety_guard.sh}"

if [ ! -x "${MODEL_ROUTER_CMD}" ]; then
  echo "model router script is missing or not executable: ${MODEL_ROUTER_CMD}" >&2
  exit 2
fi

if [ ! -x "${CODEX_SAFETY_GUARD_CMD}" ]; then
  echo "codex safety guard script is missing or not executable: ${CODEX_SAFETY_GUARD_CMD}" >&2
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
AGENT_CODEX_COMPLEX_LABELS="${AGENT_CODEX_COMPLEX_LABELS:-risk/high,priority/p0,sev0,sev1,decision/major,area/storage,type/bug}"

supports_codex_search() {
  codex --help 2>/dev/null | grep -q -- "--search"
}

SELECTED_MODEL="$(AGENT_ISSUE_LABELS="${ISSUE_LABELS}" \
  AGENT_CODEX_MODEL="${AGENT_CODEX_MODEL_OVERRIDE}" \
  AGENT_CODEX_MODEL_FAST="${AGENT_CODEX_MODEL_FAST}" \
  AGENT_CODEX_MODEL_COMPLEX="${AGENT_CODEX_MODEL_COMPLEX}" \
  AGENT_CODEX_COMPLEX_LABELS="${AGENT_CODEX_COMPLEX_LABELS}" \
  "${MODEL_ROUTER_CMD}" --model)"
SELECTED_PROFILE="$(AGENT_ISSUE_LABELS="${ISSUE_LABELS}" \
  AGENT_CODEX_MODEL="${AGENT_CODEX_MODEL_OVERRIDE}" \
  AGENT_CODEX_MODEL_FAST="${AGENT_CODEX_MODEL_FAST}" \
  AGENT_CODEX_MODEL_COMPLEX="${AGENT_CODEX_MODEL_COMPLEX}" \
  AGENT_CODEX_COMPLEX_LABELS="${AGENT_CODEX_COMPLEX_LABELS}" \
  "${MODEL_ROUTER_CMD}" --profile)"
SELECTED_REASON="$(AGENT_ISSUE_LABELS="${ISSUE_LABELS}" \
  AGENT_CODEX_MODEL="${AGENT_CODEX_MODEL_OVERRIDE}" \
  AGENT_CODEX_MODEL_FAST="${AGENT_CODEX_MODEL_FAST}" \
  AGENT_CODEX_MODEL_COMPLEX="${AGENT_CODEX_MODEL_COMPLEX}" \
  AGENT_CODEX_COMPLEX_LABELS="${AGENT_CODEX_COMPLEX_LABELS}" \
  "${MODEL_ROUTER_CMD}" --reason)"
echo "[agent-executor] issue=#${ISSUE_NUMBER} model=${SELECTED_MODEL} profile=${SELECTED_PROFILE} reason=${SELECTED_REASON}" >&2

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
)

if [ "${AGENT_CODEX_SEARCH}" = "true" ] && supports_codex_search; then
  cmd+=(--search)
elif [ "${AGENT_CODEX_SEARCH}" = "true" ]; then
  echo "[agent-executor] codex --search unsupported on this runner; continuing without search." >&2
fi

cmd+=(
  exec
  --model "${SELECTED_MODEL}"
  --sandbox "${AGENT_CODEX_SANDBOX}"
  --cd "$(pwd)"
)

"${CODEX_SAFETY_GUARD_CMD}" "${cmd[@]}"
"${cmd[@]}" "${PROMPT}"
