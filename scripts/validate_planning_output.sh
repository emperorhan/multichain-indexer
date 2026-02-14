#!/usr/bin/env bash
set -euo pipefail

# Validate planner JSON output schema.
# Usage:
#   scripts/validate_planning_output.sh <json_file>

if ! command -v jq >/dev/null 2>&1; then
  echo "jq is required" >&2
  exit 2
fi

FILE="${1:-}"
if [ -z "${FILE}" ] || [ ! -f "${FILE}" ]; then
  echo "planning output file not found: ${FILE}" >&2
  exit 2
fi

jq -e '
  def nonempty_string: type == "string" and (gsub("^\\s+|\\s+$"; "") | length > 0);
  def string_array: type == "array" and all(.[]; type == "string");
  def nonempty_string_array: string_array and (length > 0) and all(.[]; (gsub("^\\s+|\\s+$"; "") | length > 0));
  def bool_or_string_bool: (type == "boolean") or (type == "string" and (. == "true" or . == "false"));
  def risk_class_ok: type == "string" and (. == "low" or . == "medium" or . == "high" or . == "critical");

  (.roadmap | type == "object")
  and (.roadmap.version | nonempty_string)
  and (.roadmap.objective | nonempty_string)
  and (.roadmap.milestones | type == "array" and length > 0)
  and all(.roadmap.milestones[]; 
      (.id | nonempty_string)
      and (.title | nonempty_string)
      and (.outcome | nonempty_string)
      and (.dependencies | string_array)
      and (.acceptance | nonempty_string_array)
      and (.risks | string_array)
      and (.max_diff_scope | type == "number" and . > 0)
      and (.risk_class | risk_class_ok)
  )
  and (.contract_defaults | type == "object")
  and (.contract_defaults.risk_class | risk_class_ok)
  and (.contract_defaults.max_diff_scope | type == "number" and . > 0)
  and (.contract_defaults.acceptance_tests | nonempty_string)
  and (.contract_defaults.invariants | nonempty_string)
  and (.contract_defaults.evidence_required | bool_or_string_bool)
  and (.decisions | string_array)
' "${FILE}" >/dev/null

echo "planning output schema valid: ${FILE}"
