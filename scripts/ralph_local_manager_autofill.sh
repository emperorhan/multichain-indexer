#!/usr/bin/env bash
set -euo pipefail

# Auto-manager for local Ralph loop.
# - Detects product completeness gaps when queue is empty.
# - Creates ready issues so the loop keeps moving.
#
# Usage:
#   scripts/ralph_local_manager_autofill.sh

RALPH_ROOT="${RALPH_ROOT:-.ralph}"
ISSUES_DIR="${RALPH_ROOT}/issues"
LOCK_FILE="${RALPH_ROOT}/manager.lock"
NEW_ISSUE_SCRIPT="${RALPH_NEW_ISSUE_SCRIPT:-scripts/ralph_local_new_issue.sh}"
ISSUE_CONTRACT_CMD="${RALPH_ISSUE_CONTRACT_CMD:-scripts/ralph_issue_contract.sh}"
CYCLE_STATE_FILE="${RALPH_ROOT}/state.automanager_cycle"
SEARCH_TOOL="${RALPH_SEARCH_TOOL:-}"

mkdir -p "${ISSUES_DIR}" "${RALPH_ROOT}/logs"
[ -f "${CYCLE_STATE_FILE}" ] || printf '0\n' > "${CYCLE_STATE_FILE}"

if [ ! -x "${NEW_ISSUE_SCRIPT}" ]; then
  echo "[ralph-manager] missing new issue script: ${NEW_ISSUE_SCRIPT}" >&2
  exit 2
fi
if [ ! -x "${ISSUE_CONTRACT_CMD}" ]; then
  echo "[ralph-manager] missing issue contract script: ${ISSUE_CONTRACT_CMD}" >&2
  exit 2
fi

if [ -z "${SEARCH_TOOL}" ]; then
  if command -v rg >/dev/null 2>&1; then
    SEARCH_TOOL="rg"
  else
    SEARCH_TOOL="grep"
  fi
fi

exec 8>"${LOCK_FILE}"
if ! flock -n 8; then
  echo "[ralph-manager] lock busy; skip"
  exit 0
fi

text_exists() {
  local pattern="$1"
  shift
  if [ "${SEARCH_TOOL}" = "rg" ]; then
    rg -q -e "${pattern}" "$@" 2>/dev/null
    return $?
  fi
  grep -R -E -q -- "${pattern}" "$@" 2>/dev/null
}

issue_status() {
  local file="$1"
  awk -F': ' '
    /^---[[:space:]]*$/ { exit }
    $1 == "status" { print $2; found=1; exit }
    END { if (!found) print "ready" }
  ' "${file}" 2>/dev/null || echo "ready"
}

issue_marked_superseded() {
  local file="$1"
  text_exists "^[[:space:]-]*superseded_by:[[:space:]]*[^[:space:]]+" "${file}"
}

issue_is_stale_superseded_blocked() {
  local file="$1"
  local status
  status="$(issue_status "${file}")"
  [ "${status}" = "blocked" ] || return 1
  issue_marked_superseded "${file}"
}

issue_is_terminal() {
  local file="$1"
  local status
  status="$(issue_status "${file}")"
  case "${status}" in
    done|closed|cancelled|canceled)
      return 0
      ;;
  esac
  return 1
}

issue_exists_with_key_in_dirs() {
  local key="$1"
  shift
  text_exists "automanager_key:[[:space:]]*${key}" "$@"
}

issue_exists_with_key() {
  local key="$1"
  issue_exists_with_key_in_dirs "${key}" \
    "${RALPH_ROOT}/issues" \
    "${RALPH_ROOT}/in-progress" \
    "${RALPH_ROOT}/done" \
    "${RALPH_ROOT}/blocked"
}

issue_exists_with_prefix_open() {
  local prefix="$1"
  local file
  while IFS= read -r file; do
    [ -f "${file}" ] || continue
    if ! text_exists "automanager_key:[[:space:]]*${prefix}" "${file}"; then
      continue
    fi
    # Done/closed issues should never keep a cycle "open".
    if issue_is_terminal "${file}"; then
      continue
    fi
    # Ignore stale blocked issues already superseded by newer tasks.
    if issue_is_stale_superseded_blocked "${file}"; then
      continue
    fi
    return 0
  done < <(find \
    "${RALPH_ROOT}/issues" \
    "${RALPH_ROOT}/in-progress" \
    "${RALPH_ROOT}/blocked" \
    -maxdepth 1 -type f -name 'I-*.md' 2>/dev/null | sort)
  return 1
}

next_cycle_number() {
  local current
  current="$(awk 'NR==1 { if ($1 ~ /^[0-9]+$/) print $1; else print 0; exit }' "${CYCLE_STATE_FILE}" 2>/dev/null || echo 0)"
  if ! [[ "${current}" =~ ^[0-9]+$ ]]; then
    current=0
  fi
  echo $((current + 1))
}

set_cycle_number() {
  local value="$1"
  printf '%s\n' "${value}" > "${CYCLE_STATE_FILE}"
}

validate_created_issue_contract() {
  local issue_path="$1"
  local role="$2"
  if "${ISSUE_CONTRACT_CMD}" validate "${issue_path}" "${role}" >/dev/null 2>&1; then
    return 0
  fi

  echo "[ralph-manager] generated invalid contract: ${issue_path} (role=${role})" >&2
  "${ISSUE_CONTRACT_CMD}" validate "${issue_path}" "${role}" >&2 || true
  rm -f "${issue_path}"
  return 1
}

base_sepolia_runtime_gap_detected() {
  # Runtime still wired as Solana-only in main/indexer boot path.
  if ! text_exists "Chain:[[:space:]]*model\\.ChainSolana" cmd/indexer/main.go; then
    return 1
  fi
  if ! text_exists "solana\\.NewAdapter" cmd/indexer/main.go; then
    return 1
  fi

  # Base/EVM runtime adapter and config surface are not present.
  if [ -d "internal/chain/ethereum" ] || [ -d "internal/chain/base" ]; then
    return 1
  fi
  if text_exists "BASE_|ETHEREUM_" internal/config/config.go; then
    return 1
  fi

  # Sidecar contract is still Solana-only.
  if text_exists "Decode(Ethereum|Evm|Base).*Batch" proto/sidecar/v1/decoder.proto; then
    return 1
  fi

  return 0
}

create_base_runtime_issue() {
  local issue_path issue_id
  issue_path="$("${NEW_ISSUE_SCRIPT}" developer "M6 implement Base Sepolia runtime pipeline wiring" "I-0109,I-0107" p0 high)"
  issue_id="$(basename "${issue_path}" .md)"

  cat > "${issue_path}" <<EOF
id: ${issue_id}
role: developer
status: ready
priority: p0
depends_on: I-0109,I-0107
title: M6 implement Base Sepolia runtime pipeline wiring
complexity: high
risk_class: high
max_diff_scope: 25
allowed_paths: cmd/,internal/,pkg/,proto/,sidecar/,scripts/,docs/,specs/,PROMPT_build.md,PROMPT_plan.md,IMPLEMENTATION_PLAN.md,.ralph/
denied_paths: .github/workflows/,deployments/,.git/
acceptance_tests: make test,make test-sidecar,make lint
invariants: canonical_event_id_unique,replay_idempotent,cursor_monotonic,signed_delta_conservation,chain_adapter_runtime_wired
non_goals: mainnet-rollout
evidence_required: true
---
## Objective
- Deliver true Base Sepolia runtime indexing (not only normalization-unit support), while preserving Solana stability.

## In Scope
- Add Base/EVM chain adapter runtime path under \`internal/chain/\`.
- Extend boot/config surface for Base Sepolia RPC and watched addresses.
- Update pipeline orchestration to run Solana + Base Sepolia ingestion paths deterministically.
- Extend sidecar protobuf/API surface if needed for non-Solana decode input.
- Add integration/regression tests proving Base Sepolia events are fetched -> decoded -> normalized -> ingested end-to-end.

## Out of Scope
- Mainnet rollout and infra deployment policy.

## Acceptance Criteria
- [ ] Base Sepolia path runs in real runtime boot path (not test-only branches).
- [ ] End-to-end tests cover Base Sepolia from fetch to DB persistence.
- [ ] Solana regression remains green.
- [ ] Validation passes: \`make test\`, \`make test-sidecar\`, \`make lint\`.

## Notes
- automanager_key: auto-base-sepolia-runtime-gap
- Trigger reason: runtime is currently Solana-only despite Base Sepolia target.

## Non Goals
- mainnet-rollout
EOF
  validate_created_issue_contract "${issue_path}" "developer"

  echo "[ralph-manager] created ${issue_id} for Base Sepolia runtime gap"
}

create_continuous_quality_cycle() {
  local cycle_num cycle_id
  local planner_path planner_issue_id planner_key
  local developer_path developer_issue_id developer_key
  local qa_path qa_issue_id qa_key

  cycle_num="$(next_cycle_number)"
  cycle_id="$(printf 'C%04d' "${cycle_num}")"
  set_cycle_number "${cycle_num}"

  planner_key="auto-quality-cycle-${cycle_id}-plan"
  developer_key="auto-quality-cycle-${cycle_id}-build"
  qa_key="auto-quality-cycle-${cycle_id}-qa"

  planner_path="$("${NEW_ISSUE_SCRIPT}" planner "CQ ${cycle_id} planner: PRD-priority tranche" "" p0 high high 15 "canonical_event_id_unique,replay_idempotent,cursor_monotonic,signed_delta_conservation,chain_adapter_runtime_wired")"
  planner_issue_id="$(basename "${planner_path}" .md)"
  cat > "${planner_path}" <<EOF
id: ${planner_issue_id}
role: planner
status: ready
priority: p0
depends_on:
title: CQ ${cycle_id} planner: PRD-priority tranche
complexity: high
risk_class: high
max_diff_scope: 15
allowed_paths: IMPLEMENTATION_PLAN.md,specs/,docs/,PROMPT_plan.md,.agent/,.ralph/
denied_paths: .github/workflows/,deployments/,.git/
acceptance_tests: make test,make test-sidecar,make lint
invariants: canonical_event_id_unique,replay_idempotent,cursor_monotonic,signed_delta_conservation,chain_adapter_runtime_wired
non_goals: no-runtime-code-changes
evidence_required: true
---
## Objective
- Produce the next executable plan that prioritizes unresolved requirements in \`PRD.md\` before optional reliability refinements.

## In Scope
- Select the next unresolved PRD requirement(s) and map them to one focused implementation slice.
- Update \`IMPLEMENTATION_PLAN.md\` and/or \`specs/*\` with PRD-traceable milestone/slice updates.
- Create at least one downstream developer issue and one downstream qa issue in \`.ralph/issues/\`.
- Emit planner contract JSON at \`.ralph/plans/plan-output-${planner_issue_id}.json\`.

## Out of Scope
- Direct production runtime implementation in this planner issue.

## Acceptance Criteria
- [ ] Updated milestone and acceptance gates are committed in plan/spec docs with explicit PRD requirement traceability.
- [ ] At least one developer issue and one qa issue are created with explicit invariants and diff bounds.
- [ ] Planner contract JSON exists and passes schema validation.
- [ ] Validation remains green: \`make test\`, \`make test-sidecar\`, \`make lint\`.

## Notes
- automanager_key: ${planner_key}
- cycle_id: ${cycle_id}
- prd_source: PRD.md
- Trigger reason: queue became idle; PRD-priority plan->build->test progression is required.

## Non Goals
- no-runtime-code-changes
EOF
  validate_created_issue_contract "${planner_path}" "planner"

  developer_path="$("${NEW_ISSUE_SCRIPT}" developer "CQ ${cycle_id} implementation: PRD-priority increment" "${planner_issue_id}" p0 high high 25 "canonical_event_id_unique,replay_idempotent,cursor_monotonic,signed_delta_conservation,solana_fee_event_coverage,base_fee_split_coverage,reorg_recovery_deterministic,chain_adapter_runtime_wired")"
  developer_issue_id="$(basename "${developer_path}" .md)"
  cat > "${developer_path}" <<EOF
id: ${developer_issue_id}
role: developer
status: ready
priority: p0
depends_on: ${planner_issue_id}
title: CQ ${cycle_id} implementation: PRD-priority increment
complexity: high
risk_class: high
max_diff_scope: 25
allowed_paths: cmd/,internal/,pkg/,proto/,sidecar/,scripts/,docs/,specs/,PROMPT_build.md,PROMPT_plan.md,IMPLEMENTATION_PLAN.md,.ralph/
denied_paths: .github/workflows/,deployments/,.git/
acceptance_tests: make test,make test-sidecar,make lint
invariants: canonical_event_id_unique,replay_idempotent,cursor_monotonic,signed_delta_conservation,solana_fee_event_coverage,base_fee_split_coverage,reorg_recovery_deterministic,chain_adapter_runtime_wired
non_goals: infra-deployment-orchestration
evidence_required: true
---
## Objective
- Implement one production-safe increment that closes planner-selected PRD requirement gap(s).

## In Scope
- Execute one concrete planner-selected slice that explicitly traces to unresolved PRD requirement(s).
- Add/extend deterministic tests proving the increment.
- Update docs/specs only when needed to keep behavior auditable.

## Out of Scope
- Broad refactors unrelated to the selected reliability slice.

## Acceptance Criteria
- [ ] The selected PRD-priority increment is implemented with bounded diff scope.
- [ ] New/updated tests fail before and pass after the change.
- [ ] No invariant regression across mandatory-chain canonical indexing paths (Solana/Base/BTC).
- [ ] Validation passes: \`make test\`, \`make test-sidecar\`, \`make lint\`.

## Notes
- automanager_key: ${developer_key}
- cycle_id: ${cycle_id}
- prd_source: PRD.md
- Trigger reason: enforce continuous planner->developer execution chain with PRD-first ordering.

## Non Goals
- infra-deployment-orchestration
EOF
  validate_created_issue_contract "${developer_path}" "developer"

  qa_path="$("${NEW_ISSUE_SCRIPT}" qa "CQ ${cycle_id} QA: PRD-priority gate and counterexample checks" "${developer_issue_id}" p0 medium medium 20 "canonical_event_id_unique,replay_idempotent,cursor_monotonic,signed_delta_conservation,solana_fee_event_coverage,base_fee_split_coverage,reorg_recovery_deterministic,chain_adapter_runtime_wired")"
  qa_issue_id="$(basename "${qa_path}" .md)"
  cat > "${qa_path}" <<EOF
id: ${qa_issue_id}
role: qa
status: ready
priority: p0
depends_on: ${developer_issue_id}
title: CQ ${cycle_id} QA: PRD-priority gate and counterexample checks
complexity: medium
risk_class: medium
max_diff_scope: 20
allowed_paths: cmd/,internal/,pkg/,proto/,sidecar/,scripts/,docs/,specs/,PROMPT_build.md,PROMPT_plan.md,IMPLEMENTATION_PLAN.md,.ralph/
denied_paths: .github/workflows/,deployments/,.git/
acceptance_tests: make test,make test-sidecar,make lint
invariants: canonical_event_id_unique,replay_idempotent,cursor_monotonic,signed_delta_conservation,solana_fee_event_coverage,base_fee_split_coverage,reorg_recovery_deterministic,chain_adapter_runtime_wired
non_goals: bypass-failures-without-repro
evidence_required: true
---
## Objective
- Validate the PRD-priority increment with invariant-focused QA and explicit pass/fail evidence.

## In Scope
- Run full validation and invariant/counterexample checks for changed indexing paths.
- Produce a QA report in \`.ralph/reports/\`.
- File follow-up developer issue(s) if failures or regressions are found.

## Out of Scope
- Shipping new runtime features outside verification scope.

## Acceptance Criteria
- [ ] QA report with clear pass/fail recommendation is written under \`.ralph/reports/\`.
- [ ] Counterexample-oriented checks are executed for at least one declared invariant.
- [ ] Any failure has a reproducible follow-up issue in \`.ralph/issues/\`.
- [ ] Validation passes: \`make test\`, \`make test-sidecar\`, \`make lint\`.

## Notes
- automanager_key: ${qa_key}
- cycle_id: ${cycle_id}
- prd_source: PRD.md
- Trigger reason: enforce continuous developer->qa closure for PRD-priority slices.

## Non Goals
- bypass-failures-without-repro
EOF
  validate_created_issue_contract "${qa_path}" "qa"

  echo "[ralph-manager] seeded continuous quality cycle ${cycle_id}: ${planner_issue_id} -> ${developer_issue_id} -> ${qa_issue_id}"
}

SPEC_ONLY_CYCLE_CHECK_COUNT="${RALPH_SPEC_ONLY_CYCLE_CHECK_COUNT:-3}"

recent_cycles_produced_no_code() {
  local check_count="${1:-${SPEC_ONLY_CYCLE_CHECK_COUNT}}"
  local cycle_num code_found=0 count=0
  cycle_num="$(awk 'NR==1{print $1+0; exit}' "${CYCLE_STATE_FILE}" 2>/dev/null || echo 0)"
  while [ "${count}" -lt "${check_count}" ] && [ "${cycle_num}" -gt 0 ]; do
    local cycle_id dev_key
    cycle_id="$(printf 'C%04d' "${cycle_num}")"
    dev_key="auto-quality-cycle-${cycle_id}-build"
    for f in "${RALPH_ROOT}/done"/I-*.md; do
      [ -f "${f}" ] || continue
      if ! text_exists "automanager_key:[[:space:]]*${dev_key}" "${f}"; then
        continue
      fi
      local commit_sha
      commit_sha="$(awk -F': ' '$1 == "- commit" {print $2; exit}' "${f}" 2>/dev/null)"
      if [ -n "${commit_sha}" ] && [ "${commit_sha}" != "none" ] && [ "${commit_sha}" != "" ]; then
        if git diff --name-only "${commit_sha}~1" "${commit_sha}" 2>/dev/null \
          | grep -qvE '\.(md)$|^specs/|^docs/|^\.ralph/|^\.agent/|^PROMPT_|^IMPLEMENTATION_PLAN'; then
          code_found=1
        fi
      fi
    done
    cycle_num=$((cycle_num - 1))
    count=$((count + 1))
  done
  [ "${code_found}" -eq 0 ]
}

main() {
  if ! issue_exists_with_key "auto-base-sepolia-runtime-gap" && base_sepolia_runtime_gap_detected; then
    create_base_runtime_issue
    return 0
  fi

  if issue_exists_with_prefix_open "auto-quality-cycle-"; then
    echo "[ralph-manager] continuous quality cycle already open; skip"
    return 0
  fi

  if recent_cycles_produced_no_code "${SPEC_ONLY_CYCLE_CHECK_COUNT}"; then
    echo "[ralph-manager] last ${SPEC_ONLY_CYCLE_CHECK_COUNT} cycles produced no code changes; halting cycle creation"
    return 0
  fi

  create_continuous_quality_cycle
  return 0
}

main "$@"
