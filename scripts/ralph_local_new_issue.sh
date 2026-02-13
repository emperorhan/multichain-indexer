#!/usr/bin/env bash
set -euo pipefail

# Create a local markdown issue for Ralph local queue.
# Usage:
#   scripts/ralph_local_new_issue.sh <role> <title> [depends_on] [priority] [complexity]

ROLE="${1:-}"
TITLE="${2:-}"
DEPENDS_ON="${3:-}"
PRIORITY="${4:-p1}"
COMPLEXITY="${5:-medium}"
RALPH_ROOT="${RALPH_ROOT:-.ralph}"
ISSUES_DIR="${RALPH_ROOT}/issues"

if [ -z "${ROLE}" ] || [ -z "${TITLE}" ]; then
  echo "Usage: scripts/ralph_local_new_issue.sh <role> <title> [depends_on] [priority] [complexity]" >&2
  exit 1
fi

case "${ROLE}" in
  planner|developer|qa) ;;
  *)
    echo "role must be one of: planner, developer, qa" >&2
    exit 2
    ;;
esac

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
---

## Objective
- TODO

## In Scope
- TODO

## Out of Scope
- TODO

## Acceptance Criteria
- [ ] TODO
EOF

echo "${issue_path}"
