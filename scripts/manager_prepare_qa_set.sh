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
require_cmd sha256sum

REPO="${GITHUB_REPOSITORY:-}"
if [ -z "${REPO}" ]; then
  REPO="$(gh repo view --json nameWithOwner -q .nameWithOwner)"
fi

AUTONOMY_DRY_RUN="${AUTONOMY_DRY_RUN:-false}"
MANAGER_MAX_SETS_PER_RUN="${MANAGER_MAX_SETS_PER_RUN:-1}"
QA_ADDRESS_BATCH_SIZE="${QA_ADDRESS_BATCH_SIZE:-5}"
SOLANA_WHITELIST_FILE="${SOLANA_WHITELIST_FILE:-configs/solana_whitelist_addresses.txt}"
SOLANA_WHITELIST_CSV="${SOLANA_WHITELIST_CSV:-}"
MANAGER_MARKER="${MANAGER_MARKER:-manager-qa-set}"

created_count=0

log() {
  printf '[manager-loop] %s\n' "$*"
}

should_dry_run() {
  [ "${AUTONOMY_DRY_RUN}" = "true" ]
}

hash_short() {
  local raw="$1"
  printf '%s' "${raw}" | sha256sum | awk '{print substr($1,1,12)}'
}

limit_reached() {
  [ "${created_count}" -ge "${MANAGER_MAX_SETS_PER_RUN}" ]
}

fingerprint_exists() {
  local fingerprint="$1"
  local count
  count="$(gh issue list \
    --repo "${REPO}" \
    --state open \
    --limit 1 \
    --search "\"[${MANAGER_MARKER}:${fingerprint}]\" in:body" \
    --json number \
    --jq 'length')"
  [ "${count}" -gt 0 ]
}

load_addresses_from_file() {
  local file_path="$1"
  [ -f "${file_path}" ] || return 0

  awk '
    /^[[:space:]]*#/ { next }
    /^[[:space:]]*$/ { next }
    { gsub(/^[[:space:]]+|[[:space:]]+$/, "", $0); print }
  ' "${file_path}"
}

load_addresses() {
  if [ -n "${SOLANA_WHITELIST_CSV}" ]; then
    printf '%s\n' "${SOLANA_WHITELIST_CSV}" | tr ',' '\n' | awk '{gsub(/^[[:space:]]+|[[:space:]]+$/, "", $0); if ($0 != "") print}'
    return 0
  fi

  load_addresses_from_file "${SOLANA_WHITELIST_FILE}"
}

create_needs_config_issue_once() {
  local fingerprint="whitelist-missing"
  local title body

  if fingerprint_exists "${fingerprint}"; then
    return 0
  fi

  title="[Decision] Configure Solana whitelist for manager agent"
  body="Manager agent could not discover whitelist addresses.

Configure one of:
1. Repository variable \`SOLANA_WHITELIST_CSV\` (comma-separated addresses)
2. File \`${SOLANA_WHITELIST_FILE}\` with one address per line

After configuration, remove \`decision-needed\` and add \`ready\` if needed.

[${MANAGER_MARKER}:${fingerprint}]"

  if should_dry_run; then
    log "dry-run create decision-needed issue: ${title}"
    return 0
  fi

  gh issue create \
    --repo "${REPO}" \
    --title "${title}" \
    --body "${body}" \
    --label "decision-needed" \
    --label "needs-opinion" \
    --label "agent/needs-config" \
    --label "role/manager" >/dev/null
}

select_batch_csv() {
  local -n all_addrs_ref=$1
  local total="${#all_addrs_ref[@]}"
  local requested="$2"
  local offset="$3"
  local size start i idx
  local selected=()

  if [ "${total}" -eq 0 ]; then
    echo ""
    return 0
  fi

  size="${requested}"
  if [ "${size}" -gt "${total}" ]; then
    size="${total}"
  fi

  # Rotate set daily in UTC so QA coverage changes over time.
  start="$(((10#$(date -u +%j) + offset) % total))"

  for ((i = 0; i < size; i++)); do
    idx=$(((start + i) % total))
    selected+=("${all_addrs_ref[idx]}")
  done

  (IFS=,; echo "${selected[*]}")
}

create_qa_issue_for_batch() {
  local csv="$1"
  local count="$2"
  local fingerprint title body

  fingerprint="set-$(hash_short "${csv}")"
  if fingerprint_exists "${fingerprint}"; then
    log "skip duplicate QA set ${fingerprint}"
    return 0
  fi

  title="[Auto][Manager] QA validation set for Solana indexer (${count} addresses)"
  body="### Objective
Validate that the Solana on-chain indexer processes this whitelisted address set without defects.

### Discovery
- source: manager agent
- selected_count: ${count}
- strategy: daily rotating subset from whitelist

### QA Input
QA_WATCHED_ADDRESSES=${csv}

### In Scope
- run QA validation executor with \`WATCHED_ADDRESSES\` set to QA_WATCHED_ADDRESSES
- verify test/lint/build health and report failures

### Out of Scope
- unrelated feature work

### Risk Level
low

### Acceptance Criteria
- [ ] QA agent run completed
- [ ] if failed, bug issue created for developer agent with repro context
- [ ] if passed, mark issue as qa/passed

[${MANAGER_MARKER}:${fingerprint}]"

  if should_dry_run; then
    log "dry-run create QA issue: ${title}"
    created_count=$((created_count + 1))
    return 0
  fi

  gh issue create \
    --repo "${REPO}" \
    --title "${title}" \
    --body "${body}" \
    --label "type/task" \
    --label "area/pipeline" \
    --label "priority/p1" \
    --label "role/manager" \
    --label "qa-ready" \
    --label "agent/discovered" >/dev/null

  created_count=$((created_count + 1))
  log "created QA set issue (${fingerprint})"
}

main() {
  local addresses_csv selected_count set_index=0
  local max_attempts attempts=0 before_created=0
  local -a addresses

  log "repo=${REPO} dry_run=${AUTONOMY_DRY_RUN} max_sets=${MANAGER_MAX_SETS_PER_RUN}"
  mapfile -t addresses < <(load_addresses | awk '!seen[$0]++')

  if [ "${#addresses[@]}" -eq 0 ]; then
    log "no whitelist addresses found"
    create_needs_config_issue_once
    return 0
  fi

  max_attempts="${#addresses[@]}"
  while ! limit_reached && [ "${attempts}" -lt "${max_attempts}" ]; do
    addresses_csv="$(select_batch_csv addresses "${QA_ADDRESS_BATCH_SIZE}" "${set_index}")"
    [ -n "${addresses_csv}" ] || break
    selected_count="$(awk -F, '{print NF}' <<<"${addresses_csv}")"

    before_created="${created_count}"
    create_qa_issue_for_batch "${addresses_csv}" "${selected_count}"

    attempts=$((attempts + 1))
    set_index=$((set_index + QA_ADDRESS_BATCH_SIZE))

    if [ "${created_count}" -gt "${before_created}" ]; then
      break
    fi
  done

  if [ "${created_count}" -eq 0 ]; then
    log "no new QA set issue created (all candidate sets already open)"
  fi

  log "created=${created_count}"
}

main "$@"
