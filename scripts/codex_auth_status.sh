#!/usr/bin/env bash
set -euo pipefail

# Report Codex auth mode and optionally enforce ChatGPT login mode.
# Usage:
#   scripts/codex_auth_status.sh
#   scripts/codex_auth_status.sh --require-chatgpt

require_chatgpt=false
if [ "${1:-}" = "--require-chatgpt" ]; then
  require_chatgpt=true
fi

if ! command -v codex >/dev/null 2>&1; then
  echo "codex_auth: missing codex CLI" >&2
  exit 2
fi

status_text="$(codex login status 2>&1 || true)"
status_line="$(printf '%s\n' "${status_text}" | awk '
  /Logged in using ChatGPT|Logged in using API key|Not logged in/ { found=1; print; exit }
  $0 !~ /^WARNING:/ && NF { fallback=$0 }
  END { if (!found && fallback != "") print fallback }
')"

mode="unknown"
if printf '%s\n' "${status_text}" | grep -qi "Logged in using ChatGPT"; then
  mode="chatgpt"
elif printf '%s\n' "${status_text}" | grep -qi "Logged in using API key"; then
  mode="api_key"
elif printf '%s\n' "${status_text}" | grep -qi "Not logged in"; then
  mode="logged_out"
fi

api_key_env="unset"
if [ -n "${OPENAI_API_KEY:-}" ]; then
  api_key_env="set"
fi

echo "codex_auth_mode=${mode}"
echo "codex_login_status=${status_line:-unknown}"
echo "openai_api_key_env=${api_key_env}"

if [ "${require_chatgpt}" != "true" ]; then
  exit 0
fi

if [ "${mode}" != "chatgpt" ]; then
  echo "codex_auth: ChatGPT login is required for Pro-plan usage." >&2
  echo "codex_auth: run 'codex logout' then 'codex login' (without --with-api-key)." >&2
  exit 3
fi

if [ "${api_key_env}" = "set" ]; then
  echo "codex_auth: OPENAI_API_KEY env is set; unset it to avoid API-credit mode confusion." >&2
  exit 4
fi
