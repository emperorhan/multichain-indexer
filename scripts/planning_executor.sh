#!/usr/bin/env bash
set -euo pipefail

if ! command -v codex >/dev/null 2>&1; then
  echo "codex CLI is required on the runner." >&2
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
ISSUE_BODY_FILE="${AGENT_ISSUE_BODY_FILE:-}"

if [ -z "${ISSUE_NUMBER}" ] || [ -z "${ISSUE_BODY_FILE}" ] || [ ! -f "${ISSUE_BODY_FILE}" ]; then
  echo "planning issue context is missing." >&2
  exit 3
fi

PLANNING_CODEX_MODEL="${PLANNING_CODEX_MODEL:-gpt-5.3-codex}"
PLANNING_CODEX_SANDBOX="${PLANNING_CODEX_SANDBOX:-workspace-write}"
PLANNING_CODEX_APPROVAL="${PLANNING_CODEX_APPROVAL:-never}"
PLANNING_CODEX_SEARCH="${PLANNING_CODEX_SEARCH:-true}"
PLANNER_FANOUT_ENABLED="${PLANNER_FANOUT_ENABLED:-true}"
PLANNER_FANOUT_CMD="${PLANNER_FANOUT_CMD:-scripts/planner_fanout.sh}"
PLANNER_FANOUT_FILE="${PLANNER_FANOUT_FILE:-.agent/planner-fanout-${ISSUE_NUMBER}.json}"

supports_codex_search() {
  codex --help 2>/dev/null | grep -q -- "--search"
}

PLAN_GUIDE="$(cat PROMPT_plan.md 2>/dev/null || true)"

PROMPT="$(cat <<EOF
${PLAN_GUIDE}

GitHub planning issue:
- number: #${ISSUE_NUMBER}
- title: ${ISSUE_TITLE}
- url: ${ISSUE_URL}
- body file: ${ISSUE_BODY_FILE}

Tasks:
1. Update IMPLEMENTATION_PLAN.md with clear milestone-level plan.
2. Update or create docs under specs/ as needed.
3. If major owner decision is required, leave explicit decision placeholders for GitHub issue escalation.
4. If decomposition is useful, write JSON fanout tasks to ${PLANNER_FANOUT_FILE}.
5. Fanout JSON schema:
   {
     "tasks": [
       {
         "title": "short task title",
         "role": "developer|qa|planner",
         "summary": "objective and expected output",
         "labels": ["type/task", "area/pipeline", "priority/p1"],
         "acceptance": ["criterion 1", "criterion 2"],
         "chains": ["solana-devnet", "base-sepolia"]
       }
     ]
   }
6. Do not implement production code in this run.
EOF
)"

cmd=(
  codex
  --ask-for-approval "${PLANNING_CODEX_APPROVAL}"
)

if [ "${PLANNING_CODEX_SEARCH}" = "true" ] && supports_codex_search; then
  cmd+=(--search)
elif [ "${PLANNING_CODEX_SEARCH}" = "true" ]; then
  echo "[planning-executor] codex --search unsupported on this runner; continuing without search." >&2
fi

cmd+=(
  exec
  --model "${PLANNING_CODEX_MODEL}"
  --sandbox "${PLANNING_CODEX_SANDBOX}"
  --cd "$(pwd)"
)

"${CODEX_SAFETY_GUARD_CMD}" "${cmd[@]}"
"${cmd[@]}" "${PROMPT}"

if [ "${PLANNER_FANOUT_ENABLED}" = "true" ]; then
  export PLANNER_SOURCE_ISSUE_NUMBER="${ISSUE_NUMBER}"
  export PLANNER_SOURCE_ISSUE_TITLE="${ISSUE_TITLE}"
  export PLANNER_SOURCE_ISSUE_URL="${ISSUE_URL}"
  export PLANNER_FANOUT_FILE
  bash -lc "${PLANNER_FANOUT_CMD}"
fi
