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
QA_MAX_ISSUES_PER_RUN="${QA_MAX_ISSUES_PER_RUN:-1}"
QA_EXEC_CMD="${QA_EXEC_CMD:-scripts/qa_executor.sh}"

processed=0

log() {
  printf '[qa-loop] %s\n' "$*"
}

should_dry_run() {
  [ "${AUTONOMY_DRY_RUN}" = "true" ]
}

pick_next_issue() {
  gh issue list \
    --repo "${REPO}" \
    --state open \
    --label qa-ready \
    --limit 100 \
    --json number,title,body,url,createdAt,labels | jq -c '
      def labels: [.labels[].name];
      map(select(
        (labels | index("qa-in-progress") | not) and
        (labels | index("blocked") | not) and
        (labels | index("decision-needed") | not) and
        (labels | index("needs-opinion") | not)
      ))
      | sort_by(.createdAt)
      | .[0]
    '
}

claim_issue() {
  local issue_number="$1"
  local issue_url="$2"
  local run_url="${GITHUB_SERVER_URL:-https://github.com}/${REPO}/actions/runs/${GITHUB_RUN_ID:-manual}"

  if should_dry_run; then
    return 0
  fi

  gh issue edit "${issue_number}" \
    --repo "${REPO}" \
    --add-label qa-in-progress \
    --remove-label qa-ready \
    --remove-label qa/passed \
    --remove-label qa/failed >/dev/null

  gh issue comment "${issue_number}" \
    --repo "${REPO}" \
    --body "[qa-loop] Claimed for QA validation.

- issue: ${issue_url}
- run: ${run_url}"
}

run_qa_executor() {
  local issue_number="$1"
  local issue_title="$2"
  local issue_body="$3"
  local issue_url="$4"

  export AGENT_ISSUE_NUMBER="${issue_number}"
  export AGENT_ISSUE_TITLE="${issue_title}"
  export AGENT_ISSUE_URL="${issue_url}"

  mkdir -p .agent
  printf '%s\n' "${issue_body}" > ".agent/issue-${issue_number}.md"
  export AGENT_ISSUE_BODY_FILE=".agent/issue-${issue_number}.md"

  if should_dry_run; then
    return 0
  fi

  local exit_code=0
  bash -lc "${QA_EXEC_CMD}" || exit_code=$?
  return "${exit_code}"
}

create_dev_bug_issue_once() {
  local source_issue_number="$1"
  local source_issue_title="$2"
  local source_issue_url="$3"
  local report_file="$4"
  local marker title body

  marker="qa-source-${source_issue_number}"
  if [ "$(gh issue list --repo "${REPO}" --state open --search "\"[${marker}]\" in:body" --limit 1 --json number --jq 'length')" -gt 0 ]; then
    return 0
  fi

  title="[Auto][QA] Defect from QA validation #${source_issue_number}"
  body="### Summary
QA validation failed for manager-provided whitelist set.

### Source
- QA issue: ${source_issue_url}
- title: ${source_issue_title}

### Failure Report
$(cat "${report_file}")

### Expected Outcome
- fix root cause and make QA validation pass for the same address set

[${marker}]"

  gh issue create \
    --repo "${REPO}" \
    --title "${title}" \
    --body "${body}" \
    --label "type/bug" \
    --label "area/pipeline" \
    --label "priority/p1" \
    --label "role/developer" \
    --label "role/qa" \
    --label "autonomous" \
    --label "ready" \
    --label "agent/discovered" >/dev/null
}

mark_passed() {
  local issue_number="$1"
  local report_file="$2"

  if should_dry_run; then
    return 0
  fi

  gh issue edit "${issue_number}" \
    --repo "${REPO}" \
    --remove-label qa-in-progress \
    --add-label qa/passed >/dev/null

  gh issue comment "${issue_number}" \
    --repo "${REPO}" \
    --body "[qa-loop] QA passed.

$(cat "${report_file}")"

  gh issue close "${issue_number}" --repo "${REPO}" >/dev/null
}

mark_failed() {
  local issue_number="$1"
  local issue_title="$2"
  local issue_url="$3"
  local report_file="$4"

  if should_dry_run; then
    return 0
  fi

  gh issue edit "${issue_number}" \
    --repo "${REPO}" \
    --remove-label qa-in-progress \
    --add-label qa/failed >/dev/null

  gh issue comment "${issue_number}" \
    --repo "${REPO}" \
    --body "[qa-loop] QA failed.

$(cat "${report_file}")"

  create_dev_bug_issue_once "${issue_number}" "${issue_title}" "${issue_url}" "${report_file}"
}

process_issue() {
  local issue_json="$1"
  local issue_number issue_title issue_body issue_url report_file

  issue_number="$(jq -r '.number' <<<"${issue_json}")"
  issue_title="$(jq -r '.title' <<<"${issue_json}")"
  issue_body="$(jq -r '.body // ""' <<<"${issue_json}")"
  issue_url="$(jq -r '.url' <<<"${issue_json}")"
  report_file=".agent/qa-report-${issue_number}.md"

  claim_issue "${issue_number}" "${issue_url}"

  local qa_status=0
  run_qa_executor "${issue_number}" "${issue_title}" "${issue_body}" "${issue_url}" || qa_status=$?

  if [ "${qa_status}" -eq 0 ]; then
    [ -f "${report_file}" ] || printf 'QA passed (no report file generated).\n' > "${report_file}"
    mark_passed "${issue_number}" "${report_file}"
    return 0
  fi

  [ -f "${report_file}" ] || printf 'QA failed (no report file generated).\n' > "${report_file}"
  mark_failed "${issue_number}" "${issue_title}" "${issue_url}" "${report_file}"
}

main() {
  local issue_json
  log "repo=${REPO} max=${QA_MAX_ISSUES_PER_RUN} dry_run=${AUTONOMY_DRY_RUN}"

  while [ "${processed}" -lt "${QA_MAX_ISSUES_PER_RUN}" ]; do
    issue_json="$(pick_next_issue)"
    if [ -z "${issue_json}" ] || [ "${issue_json}" = "null" ]; then
      log "no eligible qa-ready issues"
      break
    fi

    process_issue "${issue_json}"
    processed=$((processed + 1))
  done
}

main "$@"
