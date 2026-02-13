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
AGENT_MAX_ISSUES_PER_RUN="${AGENT_MAX_ISSUES_PER_RUN:-1}"
AUTONOMY_DRY_RUN="${AUTONOMY_DRY_RUN:-false}"
AGENT_EXEC_CMD="${AGENT_EXEC_CMD:-}"

log() {
  printf '[agent-loop] %s\n' "$*"
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
    --json number,title,body,url,createdAt,labels | jq -c '
      def labels: [.labels[].name];
      def prio:
        if labels | index("priority/p0") then 0
        elif labels | index("priority/p1") then 1
        elif labels | index("priority/p2") then 2
        elif labels | index("priority/p3") then 3
        else 9 end;
      map(select(
        (labels | index("blocked") | not) and
        (labels | index("decision-needed") | not) and
        (labels | index("in-progress") | not) and
        (labels | index("agent/needs-config") | not)
      ))
      | sort_by(prio, .createdAt)
      | .[0]
    '
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
  git checkout -B "${branch}" "origin/${BASE_BRANCH}"
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

  if [ -z "${AGENT_EXEC_CMD}" ]; then
    handle_missing_executor "${issue_number}"
    return 1
  fi

  export AGENT_ISSUE_NUMBER="${issue_number}"
  export AGENT_ISSUE_TITLE="${issue_title}"
  export AGENT_ISSUE_URL="${issue_url}"

  mkdir -p .agent
  printf '%s\n' "${issue_body}" > ".agent/issue-${issue_number}.md"
  export AGENT_ISSUE_BODY_FILE=".agent/issue-${issue_number}.md"

  log "running executor command for #${issue_number}"
  if should_dry_run; then
    return 0
  fi

  bash -lc "${AGENT_EXEC_CMD}"
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
  local issue_number issue_title issue_body issue_url branch pr_url commit_message

  issue_number="$(jq -r '.number' <<<"${issue_json}")"
  issue_title="$(jq -r '.title' <<<"${issue_json}")"
  issue_body="$(jq -r '.body // ""' <<<"${issue_json}")"
  issue_url="$(jq -r '.url' <<<"${issue_json}")"

  claim_issue "${issue_number}" "${issue_url}"
  branch="$(create_branch_for_issue "${issue_number}" "${issue_title}")"

  if ! run_executor "${issue_number}" "${issue_title}" "${issue_body}" "${issue_url}"; then
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

  log "repo=${REPO} base=${BASE_BRANCH} max=${AGENT_MAX_ISSUES_PER_RUN} dry_run=${AUTONOMY_DRY_RUN}"
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

