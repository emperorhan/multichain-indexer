#!/usr/bin/env bash
set -euo pipefail

require_cmd() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "missing command: $1" >&2
    exit 1
  fi
}

require_cmd gh
require_cmd jq

REPO="${GITHUB_REPOSITORY:-}"
if [ -z "${REPO}" ]; then
  REPO="$(gh repo view --json nameWithOwner -q .nameWithOwner)"
fi

AUTONOMY_DRY_RUN="${AUTONOMY_DRY_RUN:-false}"
PLANNER_FANOUT_ENABLED="${PLANNER_FANOUT_ENABLED:-true}"
PLANNER_FANOUT_MAX_ISSUES="${PLANNER_FANOUT_MAX_ISSUES:-8}"
PLANNER_SOURCE_ISSUE_NUMBER="${PLANNER_SOURCE_ISSUE_NUMBER:-${AGENT_ISSUE_NUMBER:-}}"
PLANNER_SOURCE_ISSUE_TITLE="${PLANNER_SOURCE_ISSUE_TITLE:-${AGENT_ISSUE_TITLE:-}}"
PLANNER_SOURCE_ISSUE_URL="${PLANNER_SOURCE_ISSUE_URL:-${AGENT_ISSUE_URL:-}}"
PLANNER_FANOUT_FILE="${PLANNER_FANOUT_FILE:-.agent/planner-fanout-${PLANNER_SOURCE_ISSUE_NUMBER}.json}"

log() {
  printf '[planner-fanout] %s\n' "$*"
}

should_dry_run() {
  [ "${AUTONOMY_DRY_RUN}" = "true" ]
}

trim() {
  sed -E 's/^[[:space:]]+//; s/[[:space:]]+$//' <<<"$1"
}

slugify() {
  tr '[:upper:]' '[:lower:]' <<<"$1" \
    | sed -E 's/[^a-z0-9]+/-/g; s/^-+//; s/-+$//; s/-+/-/g' \
    | cut -c1-48
}

add_label() {
  local label="$1"
  local existing
  [ -n "${label}" ] || return 0
  for existing in "${LABELS[@]:-}"; do
    if [ "${existing}" = "${label}" ]; then
      return 0
    fi
  done
  LABELS+=("${label}")
}

fanout_exists() {
  local marker="$1"
  local count
  count="$(gh issue list \
    --repo "${REPO}" \
    --state all \
    --search "\"[${marker}]\" in:body" \
    --limit 1 \
    --json number \
    --jq 'length')"
  [ "${count}" -gt 0 ]
}

normalize_role() {
  local role="$1"
  role="$(trim "$(tr '[:upper:]' '[:lower:]' <<<"${role}")")"
  case "${role}" in
    developer|qa|planner) echo "${role}" ;;
    *) echo "" ;;
  esac
}

build_body() {
  local marker="$1"
  local summary="$2"
  local acceptance_lines="$3"
  local notes="$4"
  local chain_lines="$5"

  cat <<EOF
### Objective
${summary}

### Source Plan
- planner issue: ${PLANNER_SOURCE_ISSUE_URL}
- planner title: ${PLANNER_SOURCE_ISSUE_TITLE}

### Chain Scope
${chain_lines}

### Acceptance Criteria
${acceptance_lines}

### Notes
${notes}

[${marker}]
EOF
}

if [ "${PLANNER_FANOUT_ENABLED}" != "true" ]; then
  log "fanout disabled by PLANNER_FANOUT_ENABLED=${PLANNER_FANOUT_ENABLED}"
  exit 0
fi

if [ -z "${PLANNER_SOURCE_ISSUE_NUMBER}" ]; then
  echo "planner issue context is missing. expected PLANNER_SOURCE_ISSUE_NUMBER or AGENT_ISSUE_NUMBER." >&2
  exit 2
fi

if [ ! -f "${PLANNER_FANOUT_FILE}" ]; then
  log "fanout file not found: ${PLANNER_FANOUT_FILE} (skipping)"
  exit 0
fi

if ! jq -e '.tasks and (.tasks | type == "array")' "${PLANNER_FANOUT_FILE}" >/dev/null; then
  echo "invalid fanout file: expected JSON object with tasks array (${PLANNER_FANOUT_FILE})" >&2
  exit 3
fi

task_count="$(jq '.tasks | length' "${PLANNER_FANOUT_FILE}")"
if [ "${task_count}" -eq 0 ]; then
  log "fanout file has zero tasks: ${PLANNER_FANOUT_FILE}"
  exit 0
fi

created=0
processed=0

while IFS= read -r task_json; do
  processed=$((processed + 1))
  if [ "${created}" -ge "${PLANNER_FANOUT_MAX_ISSUES}" ]; then
    log "reached max fanout issues (${PLANNER_FANOUT_MAX_ISSUES}); stopping"
    break
  fi

  task_title="$(jq -r '.title // ""' <<<"${task_json}")"
  task_title="$(trim "${task_title}")"
  if [ -z "${task_title}" ]; then
    log "task ${processed} skipped: missing title"
    continue
  fi

  task_role="$(jq -r '.role // "developer"' <<<"${task_json}")"
  task_role="$(normalize_role "${task_role}")"
  if [ -z "${task_role}" ]; then
    log "task ${processed} skipped: unsupported role"
    continue
  fi

  summary="$(jq -r '.summary // .objective // ""' <<<"${task_json}")"
  summary="$(trim "${summary}")"
  [ -n "${summary}" ] || summary="Implement planned work item: ${task_title}"

  acceptance_lines="$(jq -r '
    (.acceptance_criteria // .acceptance // []) as $a
    | if ($a | length) == 0 then "- [ ] Define explicit acceptance criteria"
      else ($a | map("- [ ] " + .) | join("\n"))
      end
  ' <<<"${task_json}")"

  notes="$(jq -r '.notes // ""' <<<"${task_json}")"
  notes="$(trim "${notes}")"
  [ -n "${notes}" ] || notes="Follow IMPLEMENTATION_PLAN.md and related specs."

  chain_lines="$(jq -r '
    if (.chains // [] | length) > 0 then
      (.chains | map("- " + .) | join("\n"))
    elif (.chain // "") != "" then
      "- " + .chain
    else
      "- Inherit chain scope from planner issue"
    end
  ' <<<"${task_json}")"

  title_prefix="[Plan #${PLANNER_SOURCE_ISSUE_NUMBER}]"
  child_title="${title_prefix} ${task_title}"
  marker="planner-fanout-${PLANNER_SOURCE_ISSUE_NUMBER}-$(slugify "${task_role}-${task_title}")"

  if fanout_exists "${marker}"; then
    log "task ${processed} skipped: already exists (${marker})"
    continue
  fi

  LABELS=()
  add_label "type/task"
  add_label "priority/p1"
  add_label "role/${task_role}"

  if [ "${task_role}" = "qa" ]; then
    add_label "qa-ready"
  else
    add_label "autonomous"
    add_label "ready"
  fi

  while IFS= read -r extra_label; do
    extra_label="$(trim "${extra_label}")"
    case "${extra_label}" in
      type/*|area/*|priority/*|release/*|sev0|sev1|sev2|sev3|risk/high)
        add_label "${extra_label}"
        ;;
    esac
  done < <(jq -r '.labels // [] | .[]' <<<"${task_json}")

  body="$(build_body "${marker}" "${summary}" "${acceptance_lines}" "${notes}" "${chain_lines}")"

  if should_dry_run; then
    log "dry-run create: ${child_title} labels=$(IFS=,; echo "${LABELS[*]}")"
    created=$((created + 1))
    continue
  fi

  cmd=(
    gh issue create
    --repo "${REPO}"
    --title "${child_title}"
    --body "${body}"
  )
  for label in "${LABELS[@]}"; do
    cmd+=(--label "${label}")
  done

  "${cmd[@]}" >/dev/null
  created=$((created + 1))
  log "created child issue: ${child_title}"
done < <(jq -c '.tasks[]' "${PLANNER_FANOUT_FILE}")

log "processed=${processed} created=${created} file=${PLANNER_FANOUT_FILE}"
