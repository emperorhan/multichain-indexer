#!/usr/bin/env bash
set -euo pipefail

# Create a local markdown issue for Ralph local queue.
# Usage:
#   scripts/ralph_local_new_issue.sh <role> <title> [depends_on] [priority] [complexity] [risk_class] [max_diff_scope] [invariants]

ROLE="${1:-}"
TITLE="${2:-}"
DEPENDS_ON="${3:-}"
PRIORITY="${4:-p1}"
COMPLEXITY="${5:-medium}"
RISK_CLASS="${6:-}"
MAX_DIFF_SCOPE="${7:-25}"
INVARIANTS="${8:-canonical_event_id_unique,replay_idempotent,cursor_monotonic}"
RALPH_ROOT="${RALPH_ROOT:-.ralph}"
ISSUES_DIR="${RALPH_ROOT}/issues"

if [ -z "${ROLE}" ] || [ -z "${TITLE}" ]; then
  echo "Usage: scripts/ralph_local_new_issue.sh <role> <title> [depends_on] [priority] [complexity] [risk_class] [max_diff_scope] [invariants]" >&2
  exit 1
fi

case "${ROLE}" in
  planner|developer|qa) ;;
  *)
    echo "role must be one of: planner, developer, qa" >&2
    exit 2
    ;;
esac

if [ -z "${RISK_CLASS}" ]; then
  case "${COMPLEXITY}" in
    high|critical) RISK_CLASS="high" ;;
    medium) RISK_CLASS="medium" ;;
    *) RISK_CLASS="low" ;;
  esac
fi

case "${RISK_CLASS}" in
  low|medium|high|critical) ;;
  *)
    echo "risk_class must be one of: low, medium, high, critical" >&2
    exit 2
    ;;
esac

if ! [[ "${MAX_DIFF_SCOPE}" =~ ^[0-9]+$ ]] || [ "${MAX_DIFF_SCOPE}" -le 0 ]; then
  echo "max_diff_scope must be a positive integer" >&2
  exit 2
fi

mkdir -p "${ISSUES_DIR}" "${RALPH_ROOT}/in-progress" "${RALPH_ROOT}/done" "${RALPH_ROOT}/blocked"

max_num="0"
while IFS= read -r file; do
  num="$(sed -n 's#.*I-\([0-9][0-9][0-9][0-9]\).*#\1#p' <<<"${file}" | head -n1)"
  [ -n "${num}" ] || continue
  if [ $((10#${num})) -gt $((10#${max_num})) ]; then
    max_num="${num}"
  fi
done < <(find "${RALPH_ROOT}" -maxdepth 2 -type f -name 'I-*.md' 2>/dev/null | sort)

next_num="$(printf '%04d' "$((10#${max_num} + 1))")"
issue_id="I-${next_num}"
issue_path="${ISSUES_DIR}/${issue_id}.md"

cat >"${issue_path}" <<EOF
id: ${issue_id}
role: ${ROLE}
status: ready
priority: ${PRIORITY}
depends_on: ${DEPENDS_ON}
title: ${TITLE}
complexity: ${COMPLEXITY}
risk_class: ${RISK_CLASS}
max_diff_scope: ${MAX_DIFF_SCOPE}
allowed_paths: cmd/,internal/,pkg/,proto/,sidecar/,scripts/,docs/,specs/,PROMPT_build.md,PROMPT_plan.md,IMPLEMENTATION_PLAN.md,.ralph/
denied_paths: .github/workflows/,deployments/,.git/
acceptance_tests: make test,make test-sidecar,make lint
invariants: ${INVARIANTS}
non_goals: replace-with-non-goals
evidence_required: true
---

## Objective
- TODO

## In Scope
- TODO

## Out of Scope
- TODO

## Acceptance Criteria
- [ ] TODO

## Non Goals
- TODO

## Constraints
- Keep changes within `allowed_paths` and `max_diff_scope`.
- Do not bypass declared invariants.

## Evidence Checklist
- [ ] Root cause or design intent is documented.
- [ ] Invariant impact is documented.
- [ ] Validation outputs are linked.
EOF

echo "${issue_path}"
