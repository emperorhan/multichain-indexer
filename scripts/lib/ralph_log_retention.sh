#!/usr/bin/env bash

# Log retention helpers for local Ralph loop scripts.

to_nonneg_int() {
  local value="$1"
  local fallback="$2"
  if [[ "${value}" =~ ^[0-9]+$ ]]; then
    echo "${value}"
    return 0
  fi
  echo "${fallback}"
}

is_protected_log_file() {
  local file="$1"
  case "$(basename "${file}")" in
    runner.out|supervisor.out)
      return 0
      ;;
  esac
  return 1
}

get_log_retention_last_run() {
  awk 'NR==1 { if ($1 ~ /^[0-9]+$/) print $1; else print 0; exit }' "${LOG_RETENTION_STATE_FILE}" 2>/dev/null || echo 0
}

set_log_retention_last_run() {
  local epoch="$1"
  printf '%s\n' "${epoch}" > "${LOG_RETENTION_STATE_FILE}"
}

log_dir_total_bytes() {
  if [ ! -d "${LOGS_DIR}" ]; then
    echo 0
    return 0
  fi
  du -sb "${LOGS_DIR}" 2>/dev/null | awk '{print $1}' | awk 'NR==1 { if ($1 ~ /^[0-9]+$/) print $1; else print 0 }'
}

remove_log_file() {
  local file="$1"
  [ -f "${file}" ] || return 1
  if is_protected_log_file "${file}"; then
    return 1
  fi
  rm -f "${file}"
}

run_log_retention_if_due() {
  local enabled now last elapsed cooldown max_age_days max_files max_bytes
  local deleted_count reclaimed_bytes bytes_before bytes_after total_files overflow i
  local oldest_file oldest_size old_file old_size

  enabled="$(printf '%s' "${LOG_RETENTION_ENABLED}" | tr '[:upper:]' '[:lower:]')"
  [ "${enabled}" = "true" ] || return 0
  [ -d "${LOGS_DIR}" ] || return 0

  cooldown="$(to_nonneg_int "${LOG_RETENTION_COOLDOWN_SEC}" 300)"
  now="$(date -u +%s)"
  last="$(get_log_retention_last_run)"
  elapsed=$((now - last))
  if [ "${elapsed}" -lt "${cooldown}" ]; then
    return 0
  fi

  max_age_days="$(to_nonneg_int "${LOG_RETENTION_MAX_AGE_DAYS}" 14)"
  max_files="$(to_nonneg_int "${LOG_RETENTION_MAX_FILES}" 1200)"
  max_bytes="$(to_nonneg_int "${LOG_RETENTION_MAX_BYTES}" 2147483648)"

  deleted_count=0
  reclaimed_bytes=0
  bytes_before="$(log_dir_total_bytes)"

  if [ "${max_age_days}" -gt 0 ]; then
    while IFS= read -r old_file; do
      [ -f "${old_file}" ] || continue
      if is_protected_log_file "${old_file}"; then
        continue
      fi
      old_size="$(stat -c %s "${old_file}" 2>/dev/null || echo 0)"
      if remove_log_file "${old_file}"; then
        deleted_count=$((deleted_count + 1))
        reclaimed_bytes=$((reclaimed_bytes + old_size))
      fi
    done < <(find "${LOGS_DIR}" -maxdepth 1 -type f -mtime +"${max_age_days}" 2>/dev/null | sort)
  fi

  if [ "${max_files}" -gt 0 ]; then
    total_files="$(find "${LOGS_DIR}" -maxdepth 1 -type f ! -name 'runner.out' ! -name 'supervisor.out' 2>/dev/null | wc -l | awk '{print $1}')"
    overflow=$((total_files - max_files))
    if [ "${overflow}" -gt 0 ]; then
      i=0
      while IFS= read -r path_line; do
        [ "${i}" -lt "${overflow}" ] || break
        [ -f "${path_line}" ] || continue
        old_size="$(stat -c %s "${path_line}" 2>/dev/null || echo 0)"
        if remove_log_file "${path_line}"; then
          deleted_count=$((deleted_count + 1))
          reclaimed_bytes=$((reclaimed_bytes + old_size))
          i=$((i + 1))
        fi
      done < <(find "${LOGS_DIR}" -maxdepth 1 -type f ! -name 'runner.out' ! -name 'supervisor.out' -printf '%T@|%p\n' 2>/dev/null | sort -t'|' -k1,1n | awk -F'|' '{print $2}')
    fi
  fi

  if [ "${max_bytes}" -gt 0 ]; then
    bytes_after="$(log_dir_total_bytes)"
    while [ "${bytes_after}" -gt "${max_bytes}" ]; do
      oldest_file="$(find "${LOGS_DIR}" -maxdepth 1 -type f ! -name 'runner.out' ! -name 'supervisor.out' -printf '%T@|%p\n' 2>/dev/null | sort -t'|' -k1,1n | head -n 1 | awk -F'|' '{print $2}')"
      [ -n "${oldest_file}" ] || break
      [ -f "${oldest_file}" ] || break
      oldest_size="$(stat -c %s "${oldest_file}" 2>/dev/null || echo 0)"
      if ! remove_log_file "${oldest_file}"; then
        break
      fi
      deleted_count=$((deleted_count + 1))
      reclaimed_bytes=$((reclaimed_bytes + oldest_size))
      bytes_after="$(log_dir_total_bytes)"
    done
  fi

  set_log_retention_last_run "${now}"
  if [ "${deleted_count}" -gt 0 ]; then
    bytes_after="$(log_dir_total_bytes)"
    echo "[ralph-local] log retention pruned files=${deleted_count} reclaimed_bytes=${reclaimed_bytes} before_bytes=${bytes_before} after_bytes=${bytes_after}"
  fi
}
