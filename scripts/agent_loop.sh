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
AGENT_IN_PROGRESS_TIMEOUT_HOURS="${AGENT_IN_PROGRESS_TIMEOUT_HOURS:-6}"
AGENT_AUTO_CLEANUP_ENABLED="${AGENT_AUTO_CLEANUP_ENABLED:-true}"
AGENT_DEPRECATE_DUPLICATES_ENABLED="${AGENT_DEPRECATE_DUPLICATES_ENABLED:-true}"
AUTONOMY_DRY_RUN="${AUTONOMY_DRY_RUN:-false}"
AGENT_EXEC_CMD="${AGENT_EXEC_CMD:-}"
PLANNING_EXEC_CMD="${PLANNING_EXEC_CMD:-scripts/planning_executor.sh}"
AGENT_INCLUDE_LABELS="${AGENT_INCLUDE_LABELS:-}"
AGENT_EXCLUDE_LABELS="${AGENT_EXCLUDE_LABELS:-}"
AGENT_QUEUE_NAME="${AGENT_QUEUE_NAME:-default}"
RALPH_SELF_HEAL_ENABLED="${RALPH_SELF_HEAL_ENABLED:-true}"
ACTIONS_PERMISSION_HEAL_ATTEMPTED="false"
LAST_PR_CREATE_ERROR=""

log() {
  printf '[agent-loop] %s\n' "$*" >&2
}

should_dry_run() {
  [ "${AUTONOMY_DRY_RUN}" = "true" ]
}

ensure_actions_pr_permissions() {
  local workflow_permissions default_permissions can_approve

  if [ "${RALPH_SELF_HEAL_ENABLED}" != "true" ]; then
    return 1
  fi

  if should_dry_run; then
    return 1
  fi

  if [ "${ACTIONS_PERMISSION_HEAL_ATTEMPTED}" = "true" ]; then
    return 1
  fi
  ACTIONS_PERMISSION_HEAL_ATTEMPTED="true"

  if ! workflow_permissions="$(gh api "repos/${REPO}/actions/permissions/workflow" 2>/dev/null)"; then
    log "self-heal: unable to read workflow permissions"
    return 1
  fi

  default_permissions="$(jq -r '.default_workflow_permissions // empty' <<<"${workflow_permissions}")"
  can_approve="$(jq -r '.can_approve_pull_request_reviews // false' <<<"${workflow_permissions}")"

  if [ "${default_permissions}" = "write" ] && [ "${can_approve}" = "true" ]; then
    log "self-heal: workflow permissions already healthy"
    return 0
  fi

  log "self-heal: updating workflow permissions to write + can_approve_pull_request_reviews=true"
  if gh api -X PUT "repos/${REPO}/actions/permissions/workflow" \
    -f default_workflow_permissions=write \
    -F can_approve_pull_request_reviews=true >/dev/null; then
    log "self-heal: workflow permissions updated"
    return 0
  fi

  log "self-heal: failed to update workflow permissions"
  return 1
}

parse_iso8601_epoch() {
  local value="$1"
  local epoch

  epoch="$(date -d "${value}" +%s 2>/dev/null)" && {
    echo "${epoch}"
    return 0
  }

  # BSD/macOS compatibility fallback.
  for fmt in "%Y-%m-%dT%H:%M:%SZ" "%Y-%m-%dT%H:%M:%S%z" "%Y-%m-%dT%H:%M:%S.%N%z"; do
    epoch="$(date -u -j -f "${fmt}" "${value}" +%s 2>/dev/null || true)"
    if [ -n "${epoch}" ]; then
      echo "${epoch}"
      return 0
    fi
  done

  return 1
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
      def label_names: [.labels[].name];
      def priority_rank:
        if label_names | index("priority/p0") then 0
        elif label_names | index("priority/p1") then 1
        elif label_names | index("priority/p2") then 2
        elif label_names | index("priority/p3") then 3
        else 9 end;
      (csv($include)) as $required_labels
      | (csv($exclude)) as $excluded_labels
      | map(
          . as $issue
          | ($issue | label_names) as $labels
          | select(
              ($labels | index("blocked") | not) and
              ($labels | index("decision-needed") | not) and
              ($labels | index("decision/major") | not) and
              ($labels | index("needs-opinion") | not) and
              ($labels | index("in-progress") | not) and
              ($labels | index("agent/needs-config") | not) and
              (
                ($required_labels | length) == 0 or
                all($required_labels[]; . as $req | $labels | index($req) != null)
              ) and
              (
                ($excluded_labels | length) == 0 or
                all($excluded_labels[]; . as $excluded | $labels | index($excluded) == null)
              )
            )
        )
      | sort_by(priority_rank, .createdAt)
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
    if ! issue_epoch="$(parse_iso8601_epoch "${issue_updated}")"; then
      log "skipping reminder for #${issue_number}: unable to parse updatedAt timestamp"
      continue
    fi
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

requeue_stale_in_progress_issues() {
  local issues now_epoch stale_limit issue_number issue_updated issue_url issue_epoch age_hours labels

  issues="$(gh issue list \
    --repo "${REPO}" \
    --state open \
    --label autonomous \
    --label in-progress \
    --limit 200 \
    --json number,title,updatedAt,url,labels)"

  now_epoch="$(date +%s)"
  stale_limit="$((AGENT_IN_PROGRESS_TIMEOUT_HOURS * 3600))"

  while IFS= read -r line; do
    [ -z "${line}" ] && continue

    issue_number="$(jq -r '.number' <<<"${line}")"
    issue_updated="$(jq -r '.updatedAt' <<<"${line}")"
    issue_url="$(jq -r '.url' <<<"${line}")"
    labels="$(jq -r '[.labels[].name] | join(",")' <<<"${line}")"

    if [[ "${labels}" == *"decision-needed"* ]] || [[ "${labels}" == *"needs-opinion"* ]]; then
      continue
    fi

    if ! issue_epoch="$(parse_iso8601_epoch "${issue_updated}")"; then
      log "stale-claim check skip #${issue_number}: cannot parse updatedAt"
      continue
    fi

    if [ $((now_epoch - issue_epoch)) -lt "${stale_limit}" ]; then
      continue
    fi

    age_hours="$(((now_epoch - issue_epoch) / 3600))"
    log "recovering stale in-progress issue #${issue_number} (${age_hours}h): ${issue_url}"

    if should_dry_run; then
      continue
    fi

    gh issue edit "${issue_number}" \
      --repo "${REPO}" \
      --remove-label in-progress \
      --add-label ready >/dev/null

    gh issue comment "${issue_number}" \
      --repo "${REPO}" \
      --body "[agent-loop] Recovered stale in-progress claim after ${age_hours}h without updates.

This issue has been re-queued with label \`ready\`."
  done < <(jq -c '.[]' <<<"${issues}")
}

list_linked_pr_numbers() {
  local issue_number="$1"
  gh issue view "${issue_number}" \
    --repo "${REPO}" \
    --json closedByPullRequestsReferences \
    --jq '.closedByPullRequestsReferences[].number' 2>/dev/null || true
}

find_merged_linked_pr_url() {
  local issue_number="$1"
  local pr_number pr_url

  while IFS= read -r pr_number; do
    [ -z "${pr_number}" ] && continue
    pr_url="$(gh pr view "${pr_number}" \
      --repo "${REPO}" \
      --json mergedAt,url \
      --jq 'if .mergedAt != null then .url else empty end' 2>/dev/null || true)"
    if [ -n "${pr_url}" ]; then
      echo "${pr_url}"
      return 0
    fi
  done < <(list_linked_pr_numbers "${issue_number}")

  return 1
}

issue_has_open_linked_pr() {
  local issue_number="$1"
  local pr_number pr_state

  while IFS= read -r pr_number; do
    [ -z "${pr_number}" ] && continue
    pr_state="$(gh pr view "${pr_number}" \
      --repo "${REPO}" \
      --json state,mergedAt \
      --jq 'if .state == "OPEN" then "open" elif .mergedAt != null then "merged" else "closed" end' 2>/dev/null || true)"
    if [ "${pr_state}" = "open" ]; then
      return 0
    fi
  done < <(list_linked_pr_numbers "${issue_number}")

  return 1
}

auto_close_resolved_issues() {
  local issues line issue_number issue_title issue_url merged_pr_url labels

  issues="$(gh issue list \
    --repo "${REPO}" \
    --state open \
    --label autonomous \
    --limit 200 \
    --json number,title,url,labels)"

  while IFS= read -r line; do
    [ -z "${line}" ] && continue

    issue_number="$(jq -r '.number' <<<"${line}")"
    issue_title="$(jq -r '.title' <<<"${line}")"
    issue_url="$(jq -r '.url' <<<"${line}")"
    labels="$(jq -r '[.labels[].name] | join(",")' <<<"${line}")"
    if [[ "${labels}" == *"in-progress"* ]]; then
      continue
    fi
    merged_pr_url="$(find_merged_linked_pr_url "${issue_number}" || true)"
    if [ -z "${merged_pr_url}" ]; then
      continue
    fi

    log "auto-closing resolved issue #${issue_number}: ${issue_title}"
    if should_dry_run; then
      continue
    fi

    gh issue close "${issue_number}" \
      --repo "${REPO}" \
      --comment "[agent-loop] Auto-closed as resolved because linked PR was merged.

- issue: ${issue_url}
- merged-pr: ${merged_pr_url}" >/dev/null
  done < <(jq -c '.[]' <<<"${issues}")
}

auto_close_explicitly_deprecated_issues() {
  local issues line issue_number issue_title issue_url labels

  issues="$(gh issue list \
    --repo "${REPO}" \
    --state open \
    --label autonomous \
    --limit 200 \
    --json number,title,url,labels)"

  while IFS= read -r line; do
    [ -z "${line}" ] && continue

    if ! jq -e '[.labels[].name] | any(. == "deprecated" or . == "state/deprecated" or . == "status/deprecated")' >/dev/null <<<"${line}"; then
      continue
    fi

    issue_number="$(jq -r '.number' <<<"${line}")"
    issue_title="$(jq -r '.title' <<<"${line}")"
    issue_url="$(jq -r '.url' <<<"${line}")"
    labels="$(jq -r '[.labels[].name] | join(",")' <<<"${line}")"

    if [[ "${labels}" == *"in-progress"* ]] || issue_has_open_linked_pr "${issue_number}"; then
      continue
    fi

    log "auto-closing explicitly deprecated issue #${issue_number}: ${issue_title}"
    if should_dry_run; then
      continue
    fi

    gh issue close "${issue_number}" \
      --repo "${REPO}" \
      --comment "[agent-loop] Auto-closed because issue is explicitly marked deprecated.

- issue: ${issue_url}" >/dev/null
  done < <(jq -c '.[]' <<<"${issues}")
}

auto_deprecate_duplicate_discovered_issues() {
  local groups group keep_number keep_created keep_priority
  local line issue_number issue_title issue_url issue_created labels priority
  local has_active_label has_open_pr

  groups="$(gh issue list \
    --repo "${REPO}" \
    --state open \
    --label autonomous \
    --label agent/discovered \
    --limit 200 \
    --json number,title,url,createdAt,labels | jq -c '
      def normalized_title:
        .title
        | ascii_downcase
        | gsub("\\s+"; " ")
        | gsub("^\\s+|\\s+$"; "");
      map(. + {title_key: normalized_title})
      | group_by(.title_key)
      | map(select(length > 1))
      | .[]
    ')"

  [ -n "${groups}" ] || return 0

  while IFS= read -r group; do
    [ -z "${group}" ] && continue

    keep_number=""
    keep_created=""
    keep_priority=-1

    while IFS= read -r line; do
      [ -z "${line}" ] && continue

      issue_number="$(jq -r '.number' <<<"${line}")"
      issue_created="$(jq -r '.createdAt' <<<"${line}")"
      labels="$(jq -r '[.labels[].name] | join(",")' <<<"${line}")"

      has_active_label="false"
      if [[ "${labels}" == *"in-progress"* ]] || [[ "${labels}" == *"decision-needed"* ]] || [[ "${labels}" == *"needs-opinion"* ]]; then
        has_active_label="true"
      fi

      has_open_pr="false"
      if issue_has_open_linked_pr "${issue_number}"; then
        has_open_pr="true"
      fi

      priority=0
      if [ "${has_active_label}" = "true" ] || [ "${has_open_pr}" = "true" ]; then
        priority=1
      fi

      if [ -z "${keep_number}" ] || [ "${priority}" -gt "${keep_priority}" ] || { [ "${priority}" -eq "${keep_priority}" ] && [[ "${issue_created}" > "${keep_created}" ]]; }; then
        keep_number="${issue_number}"
        keep_created="${issue_created}"
        keep_priority="${priority}"
      fi
    done < <(jq -c '.[]' <<<"${group}")

    while IFS= read -r line; do
      [ -z "${line}" ] && continue

      issue_number="$(jq -r '.number' <<<"${line}")"
      issue_title="$(jq -r '.title' <<<"${line}")"
      issue_url="$(jq -r '.url' <<<"${line}")"
      labels="$(jq -r '[.labels[].name] | join(",")' <<<"${line}")"

      if [ "${issue_number}" = "${keep_number}" ]; then
        continue
      fi

      has_active_label="false"
      if [[ "${labels}" == *"in-progress"* ]] || [[ "${labels}" == *"decision-needed"* ]] || [[ "${labels}" == *"needs-opinion"* ]]; then
        has_active_label="true"
      fi
      if [ "${has_active_label}" = "true" ]; then
        continue
      fi

      if issue_has_open_linked_pr "${issue_number}"; then
        continue
      fi

      log "auto-closing deprecated duplicate issue #${issue_number} -> keep #${keep_number}"
      if should_dry_run; then
        continue
      fi

      gh issue close "${issue_number}" \
        --repo "${REPO}" \
        --comment "[agent-loop] Auto-closed as deprecated duplicate.

- duplicate-of: #${keep_number}
- issue: ${issue_url}
- title: ${issue_title}" >/dev/null
    done < <(jq -c '.[]' <<<"${group}")
  done <<<"${groups}"
}

run_issue_cleanup() {
  if [ "${AGENT_AUTO_CLEANUP_ENABLED}" != "true" ]; then
    return 0
  fi

  auto_close_resolved_issues
  auto_close_explicitly_deprecated_issues

  if [ "${AGENT_DEPRECATE_DUPLICATES_ENABLED}" = "true" ]; then
    auto_deprecate_duplicate_discovered_issues
  fi
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
  local pr_number pr_url create_output

  LAST_PR_CREATE_ERROR=""

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

  if ! create_output="$(gh pr create \
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

Closes #${issue_number}" 2>&1)"; then
    LAST_PR_CREATE_ERROR="${create_output}"

    if [[ "${create_output}" == *"GitHub Actions is not permitted to create or approve pull requests (createPullRequest)"* ]]; then
      log "self-heal: detected PR creation permission failure, attempting permission repair"
      if ensure_actions_pr_permissions; then
        if create_output="$(gh pr create \
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

Closes #${issue_number}" 2>&1)"; then
          LAST_PR_CREATE_ERROR=""
        else
          LAST_PR_CREATE_ERROR="${create_output}"
        fi
      fi
    fi

    if [ -n "${LAST_PR_CREATE_ERROR}" ]; then
      echo "${LAST_PR_CREATE_ERROR}" >&2
      return 1
    fi
  fi

  pr_url="$(tail -n1 <<<"${create_output}")"
  if [ -z "${pr_url}" ]; then
    echo "failed to parse PR URL for branch ${branch}" >&2
    return 1
  fi

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

handle_pr_creation_failure() {
  local issue_number="$1"
  local issue_url="$2"
  local branch="$3"
  local error_line

  error_line="$(printf '%s' "${LAST_PR_CREATE_ERROR:-unknown}" | head -n1)"

  if should_dry_run; then
    return 0
  fi

  gh issue edit "${issue_number}" \
    --repo "${REPO}" \
    --remove-label in-progress \
    --add-label ready >/dev/null

  gh issue comment "${issue_number}" \
    --repo "${REPO}" \
    --body "[agent-loop] Draft PR creation failed and issue was re-queued.

- issue: ${issue_url}
- branch: ${branch}
- error: \`${error_line}\`

Please check workflow logs for the PR creation error details."
}

handle_push_failure() {
  local issue_number="$1"
  local issue_url="$2"
  local branch="$3"
  local operation="$4"
  local push_error="$5"
  local error_line

  error_line="$(printf '%s' "${push_error}" | head -n1)"

  if should_dry_run; then
    return 0
  fi

  gh issue edit "${issue_number}" \
    --repo "${REPO}" \
    --remove-label in-progress \
    --add-label ready >/dev/null

  gh issue comment "${issue_number}" \
    --repo "${REPO}" \
    --body "[agent-loop] Git ${operation} failed and issue was re-queued.

- issue: ${issue_url}
- branch: ${branch}
- error: \`${error_line}\`

Please check workflow logs for the git operation error details."
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
  if ! git_commit_output="$(git commit -m "${commit_message}" 2>&1)"; then
    handle_push_failure "${issue_number}" "${issue_url}" "${branch}" "commit" "${git_commit_output}"
    return 0
  fi

  if ! git_push_output="$(git push -u origin "${branch}" 2>&1)"; then
    handle_push_failure "${issue_number}" "${issue_url}" "${branch}" "push" "${git_push_output}"
    return 0
  fi

  if ! pr_url="$(open_or_update_pr "${issue_number}" "${issue_title}" "${branch}")"; then
    handle_pr_creation_failure "${issue_number}" "${issue_url}" "${branch}"
    return 0
  fi
  finalize_issue_with_pr "${issue_number}" "${pr_url}"
  log "issue #${issue_number} -> ${pr_url}"
}

main() {
  local issue_json processed=0

  log "repo=${REPO} queue=${AGENT_QUEUE_NAME} include=${AGENT_INCLUDE_LABELS:-<none>} exclude=${AGENT_EXCLUDE_LABELS:-<none>} base=${BASE_BRANCH} max=${AGENT_MAX_ISSUES_PER_RUN} retries=${AGENT_MAX_AUTO_RETRIES} stale_timeout_h=${AGENT_IN_PROGRESS_TIMEOUT_HOURS} cleanup=${AGENT_AUTO_CLEANUP_ENABLED} dedupe=${AGENT_DEPRECATE_DUPLICATES_ENABLED} dry_run=${AUTONOMY_DRY_RUN} self_heal=${RALPH_SELF_HEAL_ENABLED}"
  if [ "${GITHUB_ACTIONS:-}" = "true" ] && [ "${RALPH_SELF_HEAL_ENABLED}" = "true" ]; then
    ensure_actions_pr_permissions || true
  fi
  run_issue_cleanup
  requeue_stale_in_progress_issues
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
