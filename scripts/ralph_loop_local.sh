#!/usr/bin/env bash
set -euo pipefail

# Local Ralph loop runner (branch-scoped iterative build loop).
#
# Usage:
#   echo "Fix issue #123 ..." > .agent/ralph_task.md
#   MAX_LOOPS=6 scripts/ralph_loop_local.sh

require_cmd() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "missing command: $1" >&2
    exit 1
  fi
}

require_cmd codex
require_cmd git

TASK_FILE="${RALPH_TASK_FILE:-.agent/ralph_task.md}"
PROMPT_BUILD_FILE="${PROMPT_BUILD_FILE:-PROMPT_build.md}"
MAX_LOOPS="${MAX_LOOPS:-6}"
NOOP_LIMIT="${RALPH_NOOP_LIMIT:-2}"
STOP_AT="${RALPH_STOP_AT:-}"
VALIDATE_CMD="${RALPH_VALIDATE_CMD:-make test && make test-sidecar && make lint}"
PUSH_EACH_LOOP="${RALPH_PUSH_EACH_LOOP:-false}"

CODEX_MODEL_FAST="${AGENT_CODEX_MODEL_FAST:-gpt-5.3-codex-spark}"
CODEX_SANDBOX="${AGENT_CODEX_SANDBOX:-workspace-write}"
CODEX_APPROVAL="${AGENT_CODEX_APPROVAL:-never}"
CODEX_SEARCH="${AGENT_CODEX_SEARCH:-true}"

supports_codex_search() {
  codex --help 2>/dev/null | grep -q -- "--search"
}

if [ ! -f "${TASK_FILE}" ]; then
  echo "task file not found: ${TASK_FILE}" >&2
  exit 2
fi

branch="$(git rev-parse --abbrev-ref HEAD)"
if [ "${branch}" = "main" ]; then
  echo "refusing to run on main branch. use a feature branch." >&2
  exit 3
fi

mkdir -p .agent
noop_count=0

for ((loop = 1; loop <= MAX_LOOPS; loop++)); do
  if [ -n "${STOP_AT}" ] && [ "${loop}" -gt "${STOP_AT}" ]; then
    echo "[ralph-loop] stop_at reached (${STOP_AT})"
    break
  fi

  prompt="$(cat <<EOF
$(cat "${PROMPT_BUILD_FILE}" 2>/dev/null || true)

Current loop: ${loop}/${MAX_LOOPS}
Branch: ${branch}

Task file (${TASK_FILE}):
$(cat "${TASK_FILE}")
EOF
)"

  cmd=(
    codex
    --ask-for-approval "${CODEX_APPROVAL}"
  )

  if [ "${CODEX_SEARCH}" = "true" ] && supports_codex_search; then
    cmd+=(--search)
  elif [ "${CODEX_SEARCH}" = "true" ]; then
    echo "[ralph-loop] codex --search unsupported on this runner; continuing without search."
  fi

  cmd+=(
    exec
    --model "${CODEX_MODEL_FAST}"
    --sandbox "${CODEX_SANDBOX}"
    --cd "$(pwd)"
  )

  "${cmd[@]}" "${prompt}"

  if [ -z "$(git status --porcelain)" ]; then
    noop_count=$((noop_count + 1))
    echo "[ralph-loop] no changes (noop ${noop_count}/${NOOP_LIMIT})"
    if [ "${noop_count}" -ge "${NOOP_LIMIT}" ]; then
      echo "[ralph-loop] stopping due to repeated no-op loops"
      break
    fi
    continue
  fi

  noop_count=0
  if ! bash -lc "${VALIDATE_CMD}"; then
    echo "[ralph-loop] validation failed, stopping loop"
    break
  fi

  git add -A
  git commit -m "ralph(loop): iteration ${loop}" >/dev/null
  echo "[ralph-loop] committed iteration ${loop}"

  if [ "${PUSH_EACH_LOOP}" = "true" ]; then
    git push
  fi
done
