#!/usr/bin/env bash
set -euo pipefail

trim_ws() {
  local value="$1"
  value="${value#"${value%%[![:space:]]*}"}"
  value="${value%"${value##*[![:space:]]}"}"
  printf '%s' "${value}"
}

split_csv() {
  local raw="$1"
  local -n out_ref="$2"
  local -a raw_items=()
  local item
  out_ref=()
  IFS=',' read -r -a raw_items <<<"${raw}"
  for item in "${raw_items[@]}"; do
    item="$(trim_ws "${item}")"
    [ -n "${item}" ] && out_ref+=("${item}")
  done
}

first_matching_label() {
  local issue_labels_csv="$1"
  local candidate_labels_csv="$2"
  local issue_labels=()
  local candidates=()
  local candidate issue_label

  split_csv "${issue_labels_csv}" issue_labels
  split_csv "${candidate_labels_csv}" candidates

  for candidate in "${candidates[@]}"; do
    for issue_label in "${issue_labels[@]}"; do
      if [ "${issue_label}" = "${candidate}" ]; then
        echo "${candidate}"
        return 0
      fi
    done
  done

  return 1
}

OUTPUT_MODE="model"
ISSUE_LABELS="${AGENT_ISSUE_LABELS:-${ISSUE_LABELS:-}}"

while [ $# -gt 0 ]; do
  case "$1" in
    --model)
      OUTPUT_MODE="model"
      ;;
    --profile)
      OUTPUT_MODE="profile"
      ;;
    --reason)
      OUTPUT_MODE="reason"
      ;;
    --labels)
      shift
      if [ $# -eq 0 ]; then
        echo "--labels requires a value" >&2
        exit 2
      fi
      ISSUE_LABELS="$1"
      ;;
    *)
      echo "unknown argument: $1" >&2
      exit 2
      ;;
  esac
  shift
done

AGENT_CODEX_MODEL_OVERRIDE="${AGENT_CODEX_MODEL:-}"
AGENT_CODEX_MODEL_FAST="${AGENT_CODEX_MODEL_FAST:-gpt-5.3-codex-spark}"
AGENT_CODEX_MODEL_COMPLEX="${AGENT_CODEX_MODEL_COMPLEX:-gpt-5.3-codex}"
AGENT_CODEX_COMPLEX_LABELS="${AGENT_CODEX_COMPLEX_LABELS:-risk/high,priority/p0,sev0,sev1,decision/major,area/storage,type/bug}"
AGENT_CODEX_FAST_LABELS="${AGENT_CODEX_FAST_LABELS:-}"

SELECTED_MODEL="${AGENT_CODEX_MODEL_FAST}"
SELECTED_PROFILE="fast"
SELECTED_REASON="default-fast-path"

if [ -n "${AGENT_CODEX_MODEL_OVERRIDE}" ]; then
  SELECTED_MODEL="${AGENT_CODEX_MODEL_OVERRIDE}"
  SELECTED_PROFILE="override"
  SELECTED_REASON="forced-by-AGENT_CODEX_MODEL"
else
  if matched="$(first_matching_label "${ISSUE_LABELS}" "${AGENT_CODEX_COMPLEX_LABELS}" || true)"; then
    if [ -n "${matched}" ]; then
      SELECTED_MODEL="${AGENT_CODEX_MODEL_COMPLEX}"
      SELECTED_PROFILE="complex"
      SELECTED_REASON="matched-complex-label:${matched}"
    fi
  fi

  if [ "${SELECTED_PROFILE}" = "fast" ] && [ -n "${AGENT_CODEX_FAST_LABELS}" ]; then
    if matched="$(first_matching_label "${ISSUE_LABELS}" "${AGENT_CODEX_FAST_LABELS}" || true)"; then
      if [ -n "${matched}" ]; then
        SELECTED_REASON="matched-fast-label:${matched}"
      fi
    fi
  fi
fi

case "${OUTPUT_MODE}" in
  model)
    echo "${SELECTED_MODEL}"
    ;;
  profile)
    echo "${SELECTED_PROFILE}"
    ;;
  reason)
    echo "${SELECTED_REASON}"
    ;;
  *)
    echo "invalid output mode: ${OUTPUT_MODE}" >&2
    exit 2
    ;;
esac
