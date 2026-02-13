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
require_cmd git

REPO="${GITHUB_REPOSITORY:-}"
if [ -z "${REPO}" ]; then
  REPO="$(gh repo view --json nameWithOwner -q .nameWithOwner)"
fi

BASE_BRANCH="${BASE_BRANCH:-main}"
WORK_BRANCH_PREFIX="${WORK_BRANCH_PREFIX:-agent-issue}"
DECISION_REMINDER_HOURS="${DECISION_REMINDER_HOURS:-24}"
AGENT_MAX_ISSUES_PER_RUN="${AGENT_MAX_ISSUES_PER_RUN:-2}"
AGENT_MAX_AUTO_RETRIES="${AGENT_MAX_AUTO_RETRIES:-2}"
AUTONOMY_DRY_RUN="${AUTONOMY_DRY_RUN:-false}"
AGENT_EXEC_CMD="${AGENT_EXEC_CMD:-}"
PLANNING_EXEC_CMD="${PLANNING_EXEC_CMD:-scripts/planning_executor.sh}"
AGENT_INCLUDE_LABELS="${AGENT_INCLUDE_LABELS:-}"
AGENT_EXCLUDE_LABELS="${AGENT_EXCLUDE_LABELS:-}"
AGENT_QUEUE_NAME="${AGENT_QUEUE_NAME:-default}"

log() {
  printf '[agent-loop] %s\n' "$*" >&2
}

should_dry_run() {
  [ "${AUTONOMY_DRY_RUN}" = "true" ]
}

issue_has_label() {
  local issue_json="$1"
  local label_name="$2"
  jq -e --arg name "${label_name}" '.labels[].name | select(. == $name)' >/dev/null <<<"${issue_json}"
}

pick_next_issue() {
  gh issue list \
    --repo "${REPO}" \
    --state open \
    --label autonomous \
    --label ready \
    --limit 200 \
    --json number,title,body,url,createdAt,labels | jq -c \
    --arg include "${AGENT_INCLUDE_LABELS}" \
    --arg exclude "${AGENT_EXCLUDE_LABELS}" '
      def csv($s):
        ($s
          | split(",")
          | map(gsub("^\\s+|\\s+$"; ""))
          | map(select(length > 0)));
      def labels: [.labels[].name];
      def prio:
        if labels | index("priority/p0") then 0
        elif labels | index("priority/p1") then 1
        elif labels | index("priority/p2") then 2
        elif labels | index("priority/p3") then 3
        else 9 end;
      ($include | csv) as $required_labels
      | ($exclude | csv) as $excluded_labels
      map(select(
        (labels | index("blocked") | not) and
        (labels | index("decision-needed") | not) and
        (labels | index("decision/major") | not) and
        (labels | index("needs-opinion") | not) and
        (labels | index("in-progress") | not) and
        (labels | index("agent/needs-config") | not) and
        (
          ($required_labels | length) == 0 or
          all($required_labels[]; labels | index(.) != null)
        ) and
        (
          ($excluded_labels | length) == 0 or
          all($excluded_labels[]; labels | index(.) == null)
        )
      ))
      | sort_by(prio, .createdAt)
      | .[0]
    '
}

count_failure_comments() {
  local issue_number="$1"
  gh issue view "${issue_number}" \
    --repo "${REPO}" \
    --json comments \
    --jq '[.comments[] | select(.body | contains("[agent-loop] Executor failed"))] | length'
}

request_owner_decision() {
  local issue_number="$1"
  local issue_url="$2"
  local reason="$3"

  log "requesting owner decision for #${issue_number}: ${reason}"
  if should_dry_run; then
    return 0
  fi

  gh issue edit "${issue_number}" \
    --repo "${REPO}" \
    --remove-label in-progress \
    --remove-label ready \
    --add-label blocked \
    --add-label decision-needed \
    --add-label decision/major \
    --add-label needs-opinion >/dev/null

  gh issue comment "${issue_number}" \
    --repo "${REPO}" \
    --body "[agent-loop] Owner decision required before autonomous execution can continue.

- issue: ${issue_url}
- reason: ${reason}

Reply on this issue with one option:
1. Continue autonomously with current scope
2. Continue autonomously with narrowed scope
3. Stop autonomous execution for this issue"
}

comment_decision_reminders() {
  local issues now_epoch age_limit issue_number issue_title issue_updated issue_url issue_epoch age_hours

  issues="$(gh issue list \
    --repo "${REPO}" \
    --state open \
    --label decision-needed \
    --limit 200 \
    --json number,title,updatedAt,url)"

  now_epoch="$(date +%s)"
  age_limit="$((DECISION_REMINDER_HOURS * 3600))"

  while IFS= read -r line; do
    [ -z "${line}" ] && continue

    issue_number="$(jq -r '.number' <<<"${line}")"
    issue_title="$(jq -r '.title' <<<"${line}")"
    issue_updated="$(jq -r '.updatedAt' <<<"${line}")"
    issue_url="$(jq -r '.url' <<<"${line}")"
    issue_epoch="$(date -d "${issue_updated}" +%s)"
    age_hours="$(((now_epoch - issue_epoch) / 3600))"

    if [ $((now_epoch - issue_epoch)) -lt "${age_limit}" ]; then
      continue
    fi

    log "decision-needed reminder on #${issue_number} (${age_hours}h stale)"
    if should_dry_run; then
      continue
    fi

    gh issue comment "${issue_number}" \
      --repo "${REPO}" \
      --body "[agent-reminder] This decision has been waiting for ${age_hours}h: ${issue_url}

Please choose an option so autonomous execution can continue."
  done < <(jq -c '.[]' <<<"${issues}")
}

claim_issue() {
  local issue_number="$1"
  local issue_url="$2"
  local run_url="${GITHUB_SERVER_URL:-https://github.com}/${REPO}/actions/runs/${GITHUB_RUN_ID:-manual}"

  log "claiming #${issue_number}"
  if should_dry_run; then
    return 0
  fi

  gh issue edit "${issue_number}" \
    --repo "${REPO}" \
    --add-label in-progress \
    --remove-label ready \
    --remove-label blocked \
    --remove-label decision-needed \
    --remove-label needs-opinion \
    --remove-label agent/needs-config >/dev/null

  gh issue comment "${issue_number}" \
    --repo "${REPO}" \
    --body "[agent-loop] Claimed for autonomous execution.

- issue: ${issue_url}
- run: ${run_url}"
}

create_branch_for_issue() {
  local issue_number="$1"
  local issue_title="$2"
  local slug branch

  slug="$(tr '[:upper:]' '[:lower:]' <<<"${issue_title}" \
    | sed -E 's/[^a-z0-9]+/-/g; s/^-+//; s/-+$//; s/-+/-/g' \
    | cut -c1-40)"
  [ -z "${slug}" ] && slug="work"
  branch="${WORK_BRANCH_PREFIX}-${issue_number}-${slug}"

  log "preparing branch ${branch}"
  if should_dry_run; then
    echo "${branch}"
    return 0
  fi

  git fetch origin "${BASE_BRANCH}"
  git checkout -B "${branch}" "origin/${BASE_BRANCH}" >/dev/null
  echo "${branch}"
}

handle_missing_executor() {
  local issue_number="$1"
  log "AGENT_EXEC_CMD is empty; cannot execute coding step for #${issue_number}"
  if should_dry_run; then
    return 0
  fi

  gh issue edit "${issue_number}" \
    --repo "${REPO}" \
    --remove-label in-progress \
    --add-label agent/needs-config >/dev/null

  gh issue comment "${issue_number}" \
    --repo "${REPO}" \
    --body "[agent-loop] Executor command is not configured.

Set repository variable \`AGENT_EXEC_CMD\` to the command that performs implementation/testing, then move this issue back to \`ready\`."
}

run_executor() {
  local issue_number="$1"
  local issue_title="$2"
  local issue_body="$3"
  local issue_url="$4"
  local issue_labels="$5"
  local exec_cmd="${AGENT_EXEC_CMD}"

  if [ -z "${AGENT_EXEC_CMD}" ]; then
    handle_missing_executor "${issue_number}"
    return 90
  fi

  if [[ "${issue_labels}" == *"role/planner"* ]]; then
    exec_cmd="${PLANNING_EXEC_CMD}"
  fi

  export AGENT_ISSUE_NUMBER="${issue_number}"
  export AGENT_ISSUE_TITLE="${issue_title}"
  export AGENT_ISSUE_URL="${issue_url}"
  export AGENT_ISSUE_LABELS="${issue_labels}"

  mkdir -p .agent
  printf '%s\n' "${issue_body}" > ".agent/issue-${issue_number}.md"
  export AGENT_ISSUE_BODY_FILE=".agent/issue-${issue_number}.md"

  log "running executor command for #${issue_number}"
  if should_dry_run; then
    return 0
  fi

  local exit_code=0
  bash -lc "${exec_cmd}" || exit_code=$?
  return "${exit_code}"
}

handle_executor_failure() {
  local issue_number="$1"
  local issue_url="$2"
  local exit_code="$3"
  local failure_count next_attempt

  if should_dry_run; then
    return 0
  fi

  failure_count="$(count_failure_comments "${issue_number}")"
  next_attempt=$((failure_count + 1))

  if [ "${next_attempt}" -lt "${AGENT_MAX_AUTO_RETRIES}" ]; then
    gh issue edit "${issue_number}" \
      --repo "${REPO}" \
      --remove-label in-progress \
      --add-label ready >/dev/null

    gh issue comment "${issue_number}" \
      --repo "${REPO}" \
      --body "[agent-loop] Executor failed (exit=${exit_code}) on attempt ${next_attempt}/${AGENT_MAX_AUTO_RETRIES}.

The issue was re-queued automatically with label \`ready\`."
    return 0
  fi

  request_owner_decision "${issue_number}" "${issue_url}" \
    "Executor failed ${next_attempt} times (exit=${exit_code}). Auto-retry budget exhausted."
}

open_or_update_pr() {
  local issue_number="$1"
  local issue_title="$2"
  local branch="$3"
  local pr_number pr_url

  pr_number="$(gh pr list \
    --repo "${REPO}" \
    --head "${branch}" \
    --state open \
    --json number \
    --jq '.[0].number // empty')"

  if [ -n "${pr_number}" ]; then
    pr_url="$(gh pr view "${pr_number}" --repo "${REPO}" --json url --jq .url)"
    echo "${pr_url}"
    return 0
  fi

  pr_url="$(gh pr create \
    --repo "${REPO}" \
    --draft \
    --base "${BASE_BRANCH}" \
    --head "${branch}" \
    --title "chore(agent): ${issue_title} (#${issue_number})" \
    --body "## Summary
- Autonomous work for #${issue_number}

## Notes
- Generated by scheduled agent loop
- Review risk and rollout plan before merge

Closes #${issue_number}" | tail -n1)"

  echo "${pr_url}"
}

finalize_issue_with_pr() {
  local issue_number="$1"
  local pr_url="$2"
  if should_dry_run; then
    return 0
  fi

  gh issue edit "${issue_number}" \
    --repo "${REPO}" \
    --remove-label in-progress \
    --add-label ready-for-review >/dev/null

  gh issue comment "${issue_number}" \
    --repo "${REPO}" \
    --body "[agent-loop] Draft PR created: ${pr_url}"
}

handle_no_changes() {
  local issue_number="$1"
  if should_dry_run; then
    return 0
  fi

  gh issue edit "${issue_number}" \
    --repo "${REPO}" \
    --remove-label in-progress \
    --add-label blocked >/dev/null

  gh issue comment "${issue_number}" \
    --repo "${REPO}" \
    --body "[agent-loop] No file changes were produced by executor. Marked as \`blocked\` for manual follow-up."
}

process_issue() {
  local issue_json="$1"
  local issue_number issue_title issue_body issue_url issue_labels branch pr_url commit_message

  issue_number="$(jq -r '.number' <<<"${issue_json}")"
  issue_title="$(jq -r '.title' <<<"${issue_json}")"
  issue_body="$(jq -r '.body // ""' <<<"${issue_json}")"
  issue_url="$(jq -r '.url' <<<"${issue_json}")"
  issue_labels="$(jq -r '[.labels[].name] | join(",")' <<<"${issue_json}")"

  if issue_has_label "${issue_json}" "risk/high"; then
    request_owner_decision "${issue_number}" "${issue_url}" \
      "Issue is marked \`risk/high\`, so autonomous execution is paused by policy."
    return 0
  fi

  claim_issue "${issue_number}" "${issue_url}"
  branch="$(create_branch_for_issue "${issue_number}" "${issue_title}")"

  local executor_status=0
  run_executor "${issue_number}" "${issue_title}" "${issue_body}" "${issue_url}" "${issue_labels}" || executor_status=$?

  if [ "${executor_status}" -ne 0 ]; then
    if [ "${executor_status}" -eq 90 ]; then
      return 0
    fi
    handle_executor_failure "${issue_number}" "${issue_url}" "${executor_status}"
    return 0
  fi

  if [ -z "$(git status --porcelain)" ]; then
    log "no changes after executor for #${issue_number}"
    handle_no_changes "${issue_number}"
    return 0
  fi

  if should_dry_run; then
    log "dry-run mode: skipping commit/push/pr for #${issue_number}"
    return 0
  fi

  git add -A
  commit_message="chore(agent): implement #${issue_number} ${issue_title}"
  git commit -m "${commit_message}" >/dev/null
  git push -u origin "${branch}" >/dev/null

  pr_url="$(open_or_update_pr "${issue_number}" "${issue_title}" "${branch}")"
  finalize_issue_with_pr "${issue_number}" "${pr_url}"
  log "issue #${issue_number} -> ${pr_url}"
}

main() {
  local issue_json processed=0

  log "repo=${REPO} queue=${AGENT_QUEUE_NAME} include=${AGENT_INCLUDE_LABELS:-<none>} exclude=${AGENT_EXCLUDE_LABELS:-<none>} base=${BASE_BRANCH} max=${AGENT_MAX_ISSUES_PER_RUN} retries=${AGENT_MAX_AUTO_RETRIES} dry_run=${AUTONOMY_DRY_RUN}"
  comment_decision_reminders

  while [ "${processed}" -lt "${AGENT_MAX_ISSUES_PER_RUN}" ]; do
    issue_json="$(pick_next_issue)"

    if [ -z "${issue_json}" ] || [ "${issue_json}" = "null" ]; then
      log "no eligible autonomous issues in queue"
      break
    fi

    process_issue "${issue_json}"
    processed=$((processed + 1))
  done
}

main "$@"
