#!/usr/bin/env bash
set -euo pipefail

RALPH_ROOT="${RALPH_ROOT:-.ralph}"
LEARNING_FILE="${RALPH_ROOT}/state.learning.md"
CONTEXT_FILE="${RALPH_ROOT}/context.md"

ARCHIVE_BASE_DIR="${RALPH_ROOT}/archive"
LEARNING_ARCHIVE_DIR="${ARCHIVE_BASE_DIR}/learning"
CONTEXT_ARCHIVE_DIR="${ARCHIVE_BASE_DIR}/context"

LEARNING_MAX_LINES="${RALPH_LEARNING_MAX_LINES:-220}"
LEARNING_KEEP_ITEMS="${RALPH_LEARNING_KEEP_ITEMS:-30}"
LEARNING_NOTE_MAX_CHARS="${RALPH_LEARNING_NOTE_MAX_CHARS:-220}"

CONTEXT_MAX_LINES="${RALPH_CONTEXT_MAX_LINES:-320}"
CONTEXT_HEAD_LINES="${RALPH_CONTEXT_HEAD_LINES:-120}"
CONTEXT_TAIL_LINES="${RALPH_CONTEXT_TAIL_LINES:-120}"

DRY_RUN="${RALPH_COMPACT_DRY_RUN:-false}"

mkdir -p "${LEARNING_ARCHIVE_DIR}" "${CONTEXT_ARCHIVE_DIR}"

line_count() {
  local file="$1"
  [ -f "${file}" ] || {
    echo "0"
    return 0
  }
  wc -l < "${file}" | awk '{print $1}'
}

archive_copy() {
  local src="$1"
  local dst="$2"
  if [ "${DRY_RUN}" = "true" ]; then
    echo "[dry-run] archive ${src} -> ${dst}"
    return 0
  fi
  cp "${src}" "${dst}"
}

compact_learning() {
  local lines ts archive_file tmp_file
  lines="$(line_count "${LEARNING_FILE}")"
  if ! [[ "${LEARNING_MAX_LINES}" =~ ^[0-9]+$ ]]; then
    LEARNING_MAX_LINES=220
  fi
  if [ "${lines}" -le "${LEARNING_MAX_LINES}" ]; then
    echo "[context-compact] learning notes unchanged (${lines} lines)"
    return 0
  fi

  ts="$(date -u +%Y%m%dT%H%M%SZ)"
  archive_file="${LEARNING_ARCHIVE_DIR}/state.learning.${ts}.md"
  archive_copy "${LEARNING_FILE}" "${archive_file}"

  tmp_file="$(mktemp)"
  awk \
    -v keep="${LEARNING_KEEP_ITEMS}" \
    -v max_chars="${LEARNING_NOTE_MAX_CHARS}" '
    function clip(s, c) {
      gsub(/[[:space:]]+/, " ", s)
      gsub(/^[[:space:]]+|[[:space:]]+$/, "", s)
      if (c > 0 && length(s) > c) {
        return substr(s, 1, c) "..."
      }
      return s
    }
    NR == 1 && /^#/ {
      header=$0
      next
    }
    /^- / {
      line=$0
      sub(/^[[:space:]]*-[[:space:]]*/, "", line)
      items[++n]=clip(line, max_chars)
    }
    END {
      if (header == "") {
        header="# Ralph Learning Notes"
      }
      print header
      if (keep <= 0) {
        keep=30
      }
      start=n-keep+1
      if (start < 1) {
        start=1
      }
      for (i=start; i<=n; i++) {
        print "- " items[i]
      }
    }
  ' "${LEARNING_FILE}" > "${tmp_file}"

  if [ "${DRY_RUN}" = "true" ]; then
    echo "[dry-run] compact learning notes -> ${LEARNING_FILE}"
    rm -f "${tmp_file}"
    return 0
  fi

  mv "${tmp_file}" "${LEARNING_FILE}"
  echo "[context-compact] compacted learning notes (${lines} lines -> $(line_count "${LEARNING_FILE}") lines)"
  echo "[context-compact] archived original: ${archive_file}"
}

compact_context() {
  local lines ts archive_file tmp_file omitted
  lines="$(line_count "${CONTEXT_FILE}")"
  if ! [[ "${CONTEXT_MAX_LINES}" =~ ^[0-9]+$ ]]; then
    CONTEXT_MAX_LINES=320
  fi
  if [ "${lines}" -le "${CONTEXT_MAX_LINES}" ]; then
    echo "[context-compact] context unchanged (${lines} lines)"
    return 0
  fi

  if ! [[ "${CONTEXT_HEAD_LINES}" =~ ^[0-9]+$ ]]; then
    CONTEXT_HEAD_LINES=120
  fi
  if ! [[ "${CONTEXT_TAIL_LINES}" =~ ^[0-9]+$ ]]; then
    CONTEXT_TAIL_LINES=120
  fi

  ts="$(date -u +%Y%m%dT%H%M%SZ)"
  archive_file="${CONTEXT_ARCHIVE_DIR}/context.${ts}.md"
  archive_copy "${CONTEXT_FILE}" "${archive_file}"

  omitted=$((lines - CONTEXT_HEAD_LINES - CONTEXT_TAIL_LINES))
  if [ "${omitted}" -lt 0 ]; then
    omitted=0
  fi

  tmp_file="$(mktemp)"
  {
    echo "# Ralph Local Context (Compacted)"
    echo
    echo "- compacted_at_utc: $(date -u +%Y-%m-%dT%H:%M:%SZ)"
    echo "- original_lines: ${lines}"
    echo "- archived_original: ${archive_file}"
    echo
    echo "## Context Head (${CONTEXT_HEAD_LINES} lines)"
    sed -n "1,${CONTEXT_HEAD_LINES}p" "${CONTEXT_FILE}"
    if [ "${omitted}" -gt 0 ]; then
      echo
      echo "... (${omitted} lines omitted, see archived original) ..."
    fi
    if [ "${CONTEXT_TAIL_LINES}" -gt 0 ]; then
      echo
      echo "## Context Tail (${CONTEXT_TAIL_LINES} lines)"
      tail -n "${CONTEXT_TAIL_LINES}" "${CONTEXT_FILE}"
    fi
  } > "${tmp_file}"

  if [ "${DRY_RUN}" = "true" ]; then
    echo "[dry-run] compact context -> ${CONTEXT_FILE}"
    rm -f "${tmp_file}"
    return 0
  fi

  mv "${tmp_file}" "${CONTEXT_FILE}"
  echo "[context-compact] compacted context (${lines} lines -> $(line_count "${CONTEXT_FILE}") lines)"
  echo "[context-compact] archived original: ${archive_file}"
}

compact_learning
compact_context

