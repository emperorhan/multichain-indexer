#!/usr/bin/env bash
set -euo pipefail

trim_ws() {
  local value="$1"
  value="${value#"${value%%[![:space:]]*}"}"
  value="${value%"${value##*[![:space:]]}"}"
  printf '%s' "${value}"
}

split_csv() {
  local raw="$1"
  local -n out_ref="$2"
  local -a raw_items=()
  local item
  out_ref=()
  IFS=',' read -r -a raw_items <<<"${raw}"
  for item in "${raw_items[@]}"; do
    item="$(trim_ws "${item}")"
    [ -n "${item}" ] && out_ref+=("${item}")
  done
}

contains_exact() {
  local needle="$1"
  shift
  local item
  for item in "$@"; do
    if [ "${item}" = "${needle}" ]; then
      return 0
    fi
  done
  return 1
}

OMX_SAFE_MODE="${OMX_SAFE_MODE:-true}"
[ "${OMX_SAFE_MODE}" = "true" ] || exit 0

OMX_FORBIDDEN_FLAGS="${OMX_FORBIDDEN_FLAGS:---madmax,--yolo,--dangerously-bypass-approvals-and-sandbox}"
OMX_FORBIDDEN_SANDBOXES="${OMX_FORBIDDEN_SANDBOXES:-danger-full-access}"
OMX_ALLOWED_APPROVALS="${OMX_ALLOWED_APPROVALS:-never,on-request,on-failure,untrusted}"

forbidden_flags=()
forbidden_sandboxes=()
allowed_approvals=()

split_csv "${OMX_FORBIDDEN_FLAGS}" forbidden_flags
split_csv "${OMX_FORBIDDEN_SANDBOXES}" forbidden_sandboxes
split_csv "${OMX_ALLOWED_APPROVALS}" allowed_approvals

sandbox_value=""
approval_value=""

args=("$@")
i=0
while [ "${i}" -lt "${#args[@]}" ]; do
  arg="${args[$i]}"

  for forbidden in "${forbidden_flags[@]}"; do
    if [ "${arg}" = "${forbidden}" ] || [[ "${arg}" == "${forbidden}="* ]]; then
      echo "[codex-safety-guard] blocked forbidden flag: ${forbidden}" >&2
      exit 64
    fi
  done

  case "${arg}" in
    --sandbox)
      i=$((i + 1))
      if [ "${i}" -ge "${#args[@]}" ]; then
        echo "[codex-safety-guard] --sandbox requires a value" >&2
        exit 64
      fi
      sandbox_value="${args[$i]}"
      ;;
    --sandbox=*)
      sandbox_value="${arg#--sandbox=}"
      ;;
    --ask-for-approval)
      i=$((i + 1))
      if [ "${i}" -ge "${#args[@]}" ]; then
        echo "[codex-safety-guard] --ask-for-approval requires a value" >&2
        exit 64
      fi
      approval_value="${args[$i]}"
      ;;
    --ask-for-approval=*)
      approval_value="${arg#--ask-for-approval=}"
      ;;
  esac

  i=$((i + 1))
done

if [ -n "${sandbox_value}" ] && contains_exact "${sandbox_value}" "${forbidden_sandboxes[@]}"; then
  echo "[codex-safety-guard] blocked forbidden sandbox: ${sandbox_value}" >&2
  exit 64
fi

if [ -n "${approval_value}" ] && ! contains_exact "${approval_value}" "${allowed_approvals[@]}"; then
  echo "[codex-safety-guard] blocked unsupported approval policy: ${approval_value}" >&2
  exit 64
fi
