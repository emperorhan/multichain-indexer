#!/usr/bin/env bash

# Validation cadence state helpers for local Ralph loop scripts.

get_validation_tier_counter() {
  awk 'NR==1 { if ($1 ~ /^[0-9]+$/) print $1; else print 0; exit }' "${VALIDATION_TIER_STATE_FILE}" 2>/dev/null || echo 0
}

set_validation_tier_counter() {
  local count="$1"
  printf '%s\n' "${count}" > "${VALIDATION_TIER_STATE_FILE}"
}
