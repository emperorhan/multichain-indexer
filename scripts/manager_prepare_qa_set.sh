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
MANAGER_MAX_SETS_PER_RUN="${MANAGER_MAX_SETS_PER_RUN:-2}"
QA_ADDRESS_BATCH_SIZE="${QA_ADDRESS_BATCH_SIZE:-5}"
QA_CHAIN_TARGETS="${QA_CHAIN_TARGETS:-solana-devnet,base-sepolia}"
SOLANA_WHITELIST_FILE="${SOLANA_WHITELIST_FILE:-configs/solana_whitelist_addresses.txt}"
SOLANA_WHITELIST_CSV="${SOLANA_WHITELIST_CSV:-}"
BASE_WHITELIST_FILE="${BASE_WHITELIST_FILE:-configs/base_whitelist_addresses.txt}"
BASE_WHITELIST_CSV="${BASE_WHITELIST_CSV:-}"
MANAGER_MARKER="${MANAGER_MARKER:-manager-qa-set}"

created_count=0

log() {
  printf '[manager-loop] %s\n' "$*"
}

should_dry_run() {
  [ "${AUTONOMY_DRY_RUN}" = "true" ]
}

trim() {
  awk '{gsub(/^[[:space:]]+|[[:space:]]+$/, "", $0); print}' <<<"$1"
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

normalize_chain() {
  local chain="$1"
  chain="$(tr '[:upper:]' '[:lower:]' <<<"${chain}")"
  chain="$(trim "${chain}")"
  case "${chain}" in
    solana|solana-devnet)
      echo "solana-devnet"
      ;;
    base|base-sepolia)
      echo "base-sepolia"
      ;;
    *)
      echo ""
      ;;
  esac
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

load_addresses_from_csv() {
  local csv="$1"
  printf '%s\n' "${csv}" | tr ',' '\n' | awk '{gsub(/^[[:space:]]+|[[:space:]]+$/, "", $0); if ($0 != "") print}'
}

load_addresses() {
  local chain="$1"
  local csv=""
  local file_path=""
  case "${chain}" in
    solana-devnet)
      csv="${SOLANA_WHITELIST_CSV}"
      file_path="${SOLANA_WHITELIST_FILE}"
      ;;
    base-sepolia)
      csv="${BASE_WHITELIST_CSV}"
      file_path="${BASE_WHITELIST_FILE}"
      ;;
    *)
      return 0
      ;;
  esac

  if [ -n "${csv}" ]; then
    load_addresses_from_csv "${csv}"
    return 0
  fi

  load_addresses_from_file "${file_path}"
}

default_whitelist_file_for_chain() {
  local chain="$1"
  case "${chain}" in
    solana-devnet)
      echo "${SOLANA_WHITELIST_FILE}"
      ;;
    base-sepolia)
      echo "${BASE_WHITELIST_FILE}"
      ;;
    *)
      echo ""
      ;;
  esac
}

csv_var_name_for_chain() {
  local chain="$1"
  case "${chain}" in
    solana-devnet)
      echo "SOLANA_WHITELIST_CSV"
      ;;
    base-sepolia)
      echo "BASE_WHITELIST_CSV"
      ;;
    *)
      echo "WHITELIST_CSV"
      ;;
  esac
}

create_needs_config_issue_once() {
  local chain="$1"
  local fingerprint="whitelist-missing-${chain}"
  local whitelist_file csv_var_name
  local title body

  if fingerprint_exists "${fingerprint}"; then
    return 0
  fi

  whitelist_file="$(default_whitelist_file_for_chain "${chain}")"
  csv_var_name="$(csv_var_name_for_chain "${chain}")"
  title="[Decision] Configure ${chain} whitelist for manager agent"
  body="Manager agent could not discover whitelist addresses for \`${chain}\`.

Configure one of:
1. Repository variable \`${csv_var_name}\` (comma-separated addresses)
2. File \`${whitelist_file}\` with one address per line

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
    --label "decision/major" \
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
  local chain="$1"
  local csv="$2"
  local count="$3"
  local chain_scope_desc
  local fingerprint title body

  fingerprint="${chain}-set-$(hash_short "${csv}")"
  if fingerprint_exists "${fingerprint}"; then
    log "skip duplicate QA set ${fingerprint}"
    return 0
  fi

  chain_scope_desc="${chain}"
  title="[Auto][Manager] QA validation set for ${chain_scope_desc} indexer (${count} addresses)"
  body="### Objective
Validate that the ${chain_scope_desc} on-chain indexer processes this whitelisted address set without defects.

### Discovery
- source: manager agent
- chain: ${chain_scope_desc}
- selected_count: ${count}
- strategy: daily rotating subset from whitelist

### QA Input
QA_CHAIN=${chain_scope_desc}
QA_WATCHED_ADDRESSES=${csv}

### In Scope
- run QA validation executor with \`QA_CHAIN\` and \`WATCHED_ADDRESSES\` from QA input
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
  local max_attempts attempts before_created
  local raw_chain chain
  local -a chains addresses

  log "repo=${REPO} dry_run=${AUTONOMY_DRY_RUN} max_sets=${MANAGER_MAX_SETS_PER_RUN} chains=${QA_CHAIN_TARGETS}"

  mapfile -t chains < <(
    printf '%s\n' "${QA_CHAIN_TARGETS}" \
      | tr ',' '\n' \
      | while IFS= read -r raw_chain; do
          chain="$(normalize_chain "${raw_chain}")"
          [ -n "${chain}" ] && echo "${chain}"
        done \
      | awk '!seen[$0]++'
  )

  if [ "${#chains[@]}" -eq 0 ]; then
    mapfile -t chains < <(printf '%s\n' "solana-devnet" "base-sepolia")
  fi

  for chain in "${chains[@]}"; do
    limit_reached && break

    mapfile -t addresses < <(load_addresses "${chain}" | awk '!seen[$0]++')
    if [ "${#addresses[@]}" -eq 0 ]; then
      log "no whitelist addresses found for ${chain}"
      create_needs_config_issue_once "${chain}"
      continue
    fi

    max_attempts="${#addresses[@]}"
    attempts=0
    set_index=0

    while ! limit_reached && [ "${attempts}" -lt "${max_attempts}" ]; do
      addresses_csv="$(select_batch_csv addresses "${QA_ADDRESS_BATCH_SIZE}" "${set_index}")"
      [ -n "${addresses_csv}" ] || break
      selected_count="$(awk -F, '{print NF}' <<<"${addresses_csv}")"

      before_created="${created_count}"
      create_qa_issue_for_batch "${chain}" "${addresses_csv}" "${selected_count}"

      attempts=$((attempts + 1))
      set_index=$((set_index + QA_ADDRESS_BATCH_SIZE))

      if [ "${created_count}" -gt "${before_created}" ]; then
        break
      fi
    done
  done

  if [ "${created_count}" -eq 0 ]; then
    log "no new QA set issue created (all candidate sets already open or missing whitelist)"
  fi

  log "created=${created_count}"
}

main "$@"
