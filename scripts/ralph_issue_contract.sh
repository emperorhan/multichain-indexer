#!/usr/bin/env bash
set -euo pipefail

# Validate Ralph local issue contract headers and semantic sections.
# Usage:
#   scripts/ralph_issue_contract.sh validate <issue_file> [role]

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
INVARIANTS_CMD="${RALPH_INVARIANTS_CMD:-${SCRIPT_DIR}/ralph_invariants.sh}"

if [ ! -x "${INVARIANTS_CMD}" ]; then
  echo "missing invariants script: ${INVARIANTS_CMD}" >&2
  exit 2
fi

meta_value() {
  local file="$1"
  local key="$2"
  awk -F': ' -v key="${key}" '
    $1 == key {
      sub($1": ", "")
      print
      exit
    }
  ' "${file}"
}

has_section() {
  local file="$1"
  local section="$2"
  awk -v section="${section}" '
    $0 == "## " section { found=1; exit }
    END { exit(found ? 0 : 1) }
  ' "${file}"
}

section_has_checkbox() {
  local file="$1"
  local section="$2"
  awk -v section="${section}" '
    BEGIN { in_section=0 }
    $0 ~ ("^##[[:space:]]+" section "$") { in_section=1; next }
    in_section && /^##[[:space:]]+/ { in_section=0 }
    in_section && /^[[:space:]]*-[[:space:]]*\[[ xX]\]/ { found=1 }
    END { exit(found ? 0 : 1) }
  ' "${file}"
}

validate_contract() {
  local file="$1"
  local role="${2:-developer}"
  local id issue_role title priority status complexity risk_class
  local max_diff_scope acceptance_tests invariants evidence_required
  local errors=0

  if [ ! -f "${file}" ]; then
    echo "issue file not found: ${file}" >&2
    return 1
  fi

  id="$(meta_value "${file}" "id")"
  issue_role="$(meta_value "${file}" "role")"
  title="$(meta_value "${file}" "title")"
  priority="$(meta_value "${file}" "priority")"
  status="$(meta_value "${file}" "status")"
  complexity="$(meta_value "${file}" "complexity")"
  risk_class="$(meta_value "${file}" "risk_class")"
  max_diff_scope="$(meta_value "${file}" "max_diff_scope")"
  acceptance_tests="$(meta_value "${file}" "acceptance_tests")"
  invariants="$(meta_value "${file}" "invariants")"
  evidence_required="$(meta_value "${file}" "evidence_required")"

  [ -n "${id}" ] || { echo "contract: missing id" >&2; errors=$((errors + 1)); }
  [ -n "${issue_role}" ] || { echo "contract: missing role" >&2; errors=$((errors + 1)); }
  [ -n "${title}" ] || { echo "contract: missing title" >&2; errors=$((errors + 1)); }
  [ -n "${priority}" ] || { echo "contract: missing priority" >&2; errors=$((errors + 1)); }
  [ -n "${status}" ] || { echo "contract: missing status" >&2; errors=$((errors + 1)); }
  [ -n "${complexity}" ] || { echo "contract: missing complexity" >&2; errors=$((errors + 1)); }
  [ -n "${risk_class}" ] || { echo "contract: missing risk_class" >&2; errors=$((errors + 1)); }
  [ -n "${max_diff_scope}" ] || { echo "contract: missing max_diff_scope" >&2; errors=$((errors + 1)); }
  [ -n "${acceptance_tests}" ] || { echo "contract: missing acceptance_tests" >&2; errors=$((errors + 1)); }
  [ -n "${invariants}" ] || { echo "contract: missing invariants" >&2; errors=$((errors + 1)); }
  [ -n "${evidence_required}" ] || { echo "contract: missing evidence_required" >&2; errors=$((errors + 1)); }

  case "${issue_role}" in
    planner|developer|qa) ;;
    *)
      echo "contract: unsupported role=${issue_role}" >&2
      errors=$((errors + 1))
      ;;
  esac

  case "${risk_class}" in
    low|medium|high|critical) ;;
    *)
      echo "contract: unsupported risk_class=${risk_class}" >&2
      errors=$((errors + 1))
      ;;
  esac

  if ! [[ "${max_diff_scope}" =~ ^[0-9]+$ ]] || [ "${max_diff_scope}" -le 0 ]; then
    echo "contract: max_diff_scope must be positive integer" >&2
    errors=$((errors + 1))
  fi

  case "${evidence_required}" in
    true|false) ;;
    *)
      echo "contract: evidence_required must be true|false" >&2
      errors=$((errors + 1))
      ;;
  esac

  if ! "${INVARIANTS_CMD}" validate "${invariants}" >/dev/null 2>&1; then
    echo "contract: invariants contain unknown ids" >&2
    errors=$((errors + 1))
  fi

  has_section "${file}" "Objective" || { echo "contract: missing section 'Objective'" >&2; errors=$((errors + 1)); }
  has_section "${file}" "In Scope" || { echo "contract: missing section 'In Scope'" >&2; errors=$((errors + 1)); }
  has_section "${file}" "Out of Scope" || { echo "contract: missing section 'Out of Scope'" >&2; errors=$((errors + 1)); }
  has_section "${file}" "Acceptance Criteria" || { echo "contract: missing section 'Acceptance Criteria'" >&2; errors=$((errors + 1)); }
  has_section "${file}" "Non Goals" || { echo "contract: missing section 'Non Goals'" >&2; errors=$((errors + 1)); }

  if ! section_has_checkbox "${file}" "Acceptance Criteria"; then
    echo "contract: 'Acceptance Criteria' must include checkbox items" >&2
    errors=$((errors + 1))
  fi

  if [ "${errors}" -gt 0 ]; then
    return 1
  fi
  return 0
}

cmd="${1:-validate}"
case "${cmd}" in
  validate)
    issue_file="${2:-}"
    role="${3:-developer}"
    if [ -z "${issue_file}" ]; then
      echo "Usage: $0 validate <issue_file> [role]" >&2
      exit 2
    fi
    validate_contract "${issue_file}" "${role}"
    ;;
  *)
    echo "Usage: $0 validate <issue_file> [role]" >&2
    exit 2
    ;;
esac
