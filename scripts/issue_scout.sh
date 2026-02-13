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
require_cmd rg
require_cmd sha256sum

REPO="${GITHUB_REPOSITORY:-}"
if [ -z "${REPO}" ]; then
  REPO="$(gh repo view --json nameWithOwner -q .nameWithOwner)"
fi

AUTONOMY_DRY_RUN="${AUTONOMY_DRY_RUN:-false}"
SCOUT_MAX_ISSUES_PER_RUN="${SCOUT_MAX_ISSUES_PER_RUN:-2}"
SCOUT_FAIL_WINDOW_HOURS="${SCOUT_FAIL_WINDOW_HOURS:-24}"
SCOUT_TODO_LIMIT="${SCOUT_TODO_LIMIT:-20}"
SCOUT_INCLUDE_TODO="${SCOUT_INCLUDE_TODO:-true}"
SCOUT_INCLUDE_CI_FAILURES="${SCOUT_INCLUDE_CI_FAILURES:-true}"
SCOUT_MARKER="${SCOUT_MARKER:-agent-scout}"

created_count=0

log() {
  printf '[issue-scout] %s\n' "$*"
}

should_dry_run() {
  [ "${AUTONOMY_DRY_RUN}" = "true" ]
}

limit_reached() {
  [ "${created_count}" -ge "${SCOUT_MAX_ISSUES_PER_RUN}" ]
}

hash_short() {
  local raw="$1"
  printf '%s' "${raw}" | sha256sum | awk '{print substr($1,1,12)}'
}

area_label_for_path() {
  local file_path="$1"
  case "${file_path}" in
    sidecar/*)
      echo "area/sidecar"
      ;;
    internal/pipeline/*|internal/chain/*)
      echo "area/pipeline"
      ;;
    internal/store/*)
      echo "area/storage"
      ;;
    *)
      echo "area/infra"
      ;;
  esac
}

fingerprint_exists() {
  local fingerprint="$1"
  local count
  count="$(gh issue list \
    --repo "${REPO}" \
    --state all \
    --limit 1 \
    --search "\"[${SCOUT_MARKER}:${fingerprint}]\" in:body" \
    --json number \
    --jq 'length')"
  [ "${count}" -gt 0 ]
}

create_issue() {
  local title="$1"
  local body="$2"
  shift 2
  local labels=("$@")

  if limit_reached; then
    return 0
  fi

  if should_dry_run; then
    log "dry-run create: ${title}"
    created_count=$((created_count + 1))
    return 0
  fi

  local args=()
  local label
  for label in "${labels[@]}"; do
    args+=("--label" "${label}")
  done

  gh issue create \
    --repo "${REPO}" \
    --title "${title}" \
    --body "${body}" \
    "${args[@]}" >/dev/null

  created_count=$((created_count + 1))
  log "created issue: ${title}"
}

scan_todo_markers() {
  local findings file line_no marker area fingerprint title body
  local -a scan_paths

  [ "${SCOUT_INCLUDE_TODO}" = "true" ] || return 0
  limit_reached && return 0

  scan_paths=()
  [ -d internal ] && scan_paths+=("internal")
  [ -d sidecar ] && scan_paths+=("sidecar")
  [ -d cmd ] && scan_paths+=("cmd")
  [ -d scripts ] && scan_paths+=("scripts")

  [ "${#scan_paths[@]}" -gt 0 ] || return 0

  findings="$(rg \
    --line-number \
    --no-heading \
    --color never \
    --glob '!**/node_modules/**' \
    --glob '!**/pkg/generated/**' \
    --glob '!**/vendor/**' \
    -S '(TODO|FIXME|HACK|XXX)' \
    "${scan_paths[@]}" || true)"

  [ -n "${findings}" ] || return 0

  while IFS= read -r entry; do
    [ -z "${entry}" ] && continue
    limit_reached && break

    file="${entry%%:*}"
    line_no="${entry#*:}"
    line_no="${line_no%%:*}"
    marker="${entry#*:*:}"

    [ -n "${file}" ] || continue
    [ -n "${line_no}" ] || continue
    [ -n "${marker}" ] || continue

    area="$(area_label_for_path "${file}")"
    fingerprint="todo-$(hash_short "${file}:${line_no}:${marker}")"

    if fingerprint_exists "${fingerprint}"; then
      continue
    fi

    title="[Auto][Scout] Resolve marker in ${file}:${line_no}"
    body="### Objective
Address the discovered debt marker and either implement the fix or remove the marker with clear rationale.

### Discovery
- source: automated issue scout (code marker scan)
- location: \`${file}:${line_no}\`
- marker: \`${marker}\`

### In Scope
- the referenced file and directly related tests/docs

### Out of Scope
- broad refactors unrelated to this marker
- schema/infrastructure changes unless necessary

### Risk Level
low

### Acceptance Criteria
- [ ] marker resolved or replaced by a concrete, tracked limitation
- [ ] tests/docs updated if behavior changes
- [ ] \`make test\`, \`make test-sidecar\`, \`make lint\` pass

[${SCOUT_MARKER}:${fingerprint}]"

    create_issue "${title}" "${body}" \
      "type/task" "autonomous" "ready" "priority/p2" "${area}" "agent/discovered" "role/developer"
  done < <(printf '%s\n' "${findings}" | head -n "${SCOUT_TODO_LIMIT}")
}

scan_recent_failed_runs() {
  local runs_json row now_epoch run_epoch age_hours
  local run_id workflow_name run_url head_branch event head_sha fingerprint title body

  [ "${SCOUT_INCLUDE_CI_FAILURES}" = "true" ] || return 0
  limit_reached && return 0

  runs_json="$(gh run list \
    --repo "${REPO}" \
    --status failure \
    --limit 30 \
    --json databaseId,workflowName,url,headBranch,event,headSha,createdAt)"

  [ "${runs_json}" != "[]" ] || return 0

  now_epoch="$(date +%s)"
  while IFS= read -r row; do
    [ -z "${row}" ] && continue
    limit_reached && break

    run_id="$(jq -r '.databaseId' <<<"${row}")"
    workflow_name="$(jq -r '.workflowName' <<<"${row}")"
    run_url="$(jq -r '.url' <<<"${row}")"
    head_branch="$(jq -r '.headBranch // "unknown"' <<<"${row}")"
    event="$(jq -r '.event // "unknown"' <<<"${row}")"
    head_sha="$(jq -r '.headSha // "unknown"' <<<"${row}")"
    run_epoch="$(date -d "$(jq -r '.createdAt' <<<"${row}")" +%s)"
    age_hours="$(((now_epoch - run_epoch) / 3600))"

    if [ "${age_hours}" -gt "${SCOUT_FAIL_WINDOW_HOURS}" ]; then
      continue
    fi

    fingerprint="ci-failure-${run_id}"
    if fingerprint_exists "${fingerprint}"; then
      continue
    fi

    title="[Auto][Scout] Investigate failing workflow: ${workflow_name}"
    body="### Objective
Investigate and fix the recurring CI failure so autonomous delivery remains healthy.

### Discovery
- source: automated issue scout (recent failed GitHub Actions run)
- workflow: \`${workflow_name}\`
- run: ${run_url}
- branch: \`${head_branch}\`
- event: \`${event}\`
- commit: \`${head_sha}\`
- failed within last: ${SCOUT_FAIL_WINDOW_HOURS}h

### In Scope
- reproduce the failure
- fix root cause in code/workflow/config
- add guardrails/tests where practical

### Out of Scope
- unrelated feature work

### Risk Level
medium

### Acceptance Criteria
- [ ] root cause identified and documented in PR
- [ ] failing check turns green
- [ ] regression prevention added when possible

[${SCOUT_MARKER}:${fingerprint}]"

    create_issue "${title}" "${body}" \
      "type/chore" "autonomous" "ready" "priority/p1" "area/infra" "agent/discovered" "role/developer"
  done < <(jq -c '.[]' <<<"${runs_json}")
}

main() {
  log "repo=${REPO} max=${SCOUT_MAX_ISSUES_PER_RUN} dry_run=${AUTONOMY_DRY_RUN}"
  scan_recent_failed_runs
  scan_todo_markers
  log "created=${created_count}"
}

main "$@"
