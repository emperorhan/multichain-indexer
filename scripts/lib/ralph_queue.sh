#!/usr/bin/env bash

# Queue and issue state helpers shared by local Ralph loop scripts.

dependency_satisfied() {
  local dep="$1"
  [ -z "${dep}" ] && return 0
  [ -f "${DONE_DIR}/${dep}.md" ]
}

all_dependencies_satisfied() {
  local deps_csv="$1"
  local dep
  [ -z "${deps_csv}" ] && return 0
  while IFS= read -r dep; do
    dep="$(awk '{gsub(/^[[:space:]]+|[[:space:]]+$/, "", $0); print}' <<<"${dep}")"
    [ -n "${dep}" ] || continue
    if ! dependency_satisfied "${dep}"; then
      return 1
    fi
  done < <(printf '%s\n' "${deps_csv}" | tr ',' '\n')
  return 0
}

set_issue_status() {
  local file="$1"
  local status="$2"
  local tmp_file

  [ -f "${file}" ] || return 0
  tmp_file="${file}.tmp"
  awk -v status="${status}" '
    BEGIN { in_header=1; replaced=0; inserted=0 }
    in_header && /^---[[:space:]]*$/ {
      if (!replaced) {
        print "status: " status
        inserted=1
      }
      in_header=0
      print
      next
    }
    in_header && /^status:[[:space:]]*/ {
      print "status: " status
      replaced=1
      next
    }
    { print }
    END {
      if (in_header && !replaced && !inserted) {
        print "status: " status
      }
    }
  ' "${file}" > "${tmp_file}"
  mv "${tmp_file}" "${file}"
}

file_mtime_epoch() {
  local file="$1"
  if stat -c %Y "${file}" >/dev/null 2>&1; then
    stat -c %Y "${file}"
    return 0
  fi
  date -r "${file}" +%s
}

get_blocked_retry_count() {
  local issue_id="$1"
  local count
  count="$(awk -v issue_id="${issue_id}" '
    $1 == issue_id {
      if ($2 ~ /^[0-9]+$/) {
        print $2
      } else {
        print 0
      }
      found=1
      exit
    }
    END {
      if (!found) {
        print 0
      }
    }
  ' "${BLOCKED_RETRY_STATE_FILE}" 2>/dev/null || echo 0)"
  if ! [[ "${count}" =~ ^[0-9]+$ ]]; then
    count=0
  fi
  echo "${count}"
}

set_blocked_retry_count() {
  local issue_id="$1"
  local count="$2"
  local tmp_file
  tmp_file="${BLOCKED_RETRY_STATE_FILE}.tmp"
  awk -v issue_id="${issue_id}" -v count="${count}" '
    BEGIN { updated=0 }
    $1 == issue_id {
      print issue_id " " count
      updated=1
      next
    }
    NF > 0 { print }
    END {
      if (!updated) {
        print issue_id " " count
      }
    }
  ' "${BLOCKED_RETRY_STATE_FILE}" > "${tmp_file}"
  mv "${tmp_file}" "${BLOCKED_RETRY_STATE_FILE}"
}

clear_blocked_retry_count() {
  local issue_id="$1"
  local tmp_file
  tmp_file="${BLOCKED_RETRY_STATE_FILE}.tmp"
  awk -v issue_id="${issue_id}" '
    $1 == issue_id { next }
    NF > 0 { print }
  ' "${BLOCKED_RETRY_STATE_FILE}" > "${tmp_file}"
  mv "${tmp_file}" "${BLOCKED_RETRY_STATE_FILE}"
}

read_queue_issue_fields() {
  local file="$1"
  awk -F': ' '
    BEGIN { id=""; status=""; deps="" }
    $1=="id" && id=="" { id=$2; next }
    $1=="status" && status=="" { status=$2; next }
    $1=="depends_on" && deps=="" { deps=$2; next }
    END {
      if (status == "") status = "ready"
      print id "\t" status "\t" deps
    }
  ' "${file}" 2>/dev/null
}

pick_retryable_blocked_issue() {
  local file issue_id deps attempts now cooldown age blocked_at status
  [ "${BLOCKED_REQUEUE_ENABLED}" = "true" ] || return 1
  now="$(date -u +%s)"
  cooldown="${BLOCKED_REQUEUE_COOLDOWN_SEC}"

  while IFS= read -r file; do
    [ -f "${file}" ] || continue
    IFS=$'\t' read -r issue_id status deps < <(read_queue_issue_fields "${file}")
    [ -n "${issue_id}" ] || issue_id="$(basename "${file}" .md)"
    if ! all_dependencies_satisfied "${deps}"; then
      continue
    fi

    attempts="$(get_blocked_retry_count "${issue_id}")"
    if [ "${attempts}" -ge "${BLOCKED_REQUEUE_MAX_ATTEMPTS}" ]; then
      continue
    fi

    blocked_at="$(file_mtime_epoch "${file}")"
    if ! [[ "${blocked_at}" =~ ^[0-9]+$ ]]; then
      blocked_at="${now}"
    fi
    age=$((now - blocked_at))
    if [ "${age}" -lt "${cooldown}" ]; then
      continue
    fi

    printf '%s|%s|%s\n' "${file}" "${issue_id}" "${attempts}"
    return 0
  done < <(find "${BLOCKED_DIR}" -maxdepth 1 -type f -name '*.md' | sort)

  return 1
}

requeue_retryable_blocked_issue() {
  local candidate file issue_id attempts next_attempt queue_path
  candidate="$(pick_retryable_blocked_issue || true)"
  [ -n "${candidate}" ] || return 1

  IFS='|' read -r file issue_id attempts <<<"${candidate}"
  [ -f "${file}" ] || return 1

  set_issue_status "${file}" "ready"
  queue_path="${QUEUE_DIR}/${issue_id}.md"
  if [ -f "${queue_path}" ]; then
    queue_path="${QUEUE_DIR}/retry-${issue_id}-$(date -u +%Y%m%dT%H%M%SZ).md"
  fi
  mv "${file}" "${queue_path}"

  next_attempt=$((attempts + 1))
  set_blocked_retry_count "${issue_id}" "${next_attempt}"
  echo "[ralph-local] requeued blocked issue ${issue_id} (attempt ${next_attempt}/${BLOCKED_REQUEUE_MAX_ATTEMPTS})"
  return 0
}

pick_next_issue() {
  local file issue_id status deps
  while IFS= read -r file; do
    [ -f "${file}" ] || continue
    IFS=$'\t' read -r issue_id status deps < <(read_queue_issue_fields "${file}")
    [ "${status}" = "ready" ] || continue
    if ! all_dependencies_satisfied "${deps}"; then
      continue
    fi
    echo "${file}"
    return 0
  done < <(find "${QUEUE_DIR}" -maxdepth 1 -type f -name 'I-*.md' | sort)
  return 1
}
