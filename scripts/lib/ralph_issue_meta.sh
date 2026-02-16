#!/usr/bin/env bash

# Shared issue metadata helpers for local Ralph loop scripts.

meta_value() {
  local file="$1"
  local key="$2"
  [ -f "${file}" ] || return 0
  awk -F': ' -v key="${key}" '
    $1 == key {
      sub($1": ", "");
      print;
      exit
    }
  ' "${file}" 2>/dev/null || true
}

issue_reference_value() {
  local file="$1"
  local key="$2"
  local value
  [ -f "${file}" ] || return 0

  value="$(meta_value "${file}" "${key}")"
  if [ -n "${value}" ]; then
    printf '%s\n' "${value}"
    return 0
  fi

  awk -v key="${key}" '
    {
      line=$0
      gsub(/^[[:space:]]*-[[:space:]]*/, "", line)
      if (line ~ ("^" key ":[[:space:]]*")) {
        sub("^" key ":[[:space:]]*", "", line)
        gsub(/^[[:space:]]+|[[:space:]]+$/, "", line)
        print line
        exit
      }
    }
  ' "${file}" 2>/dev/null || true
}
