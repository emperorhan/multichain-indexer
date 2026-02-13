#!/usr/bin/env bash
set -euo pipefail

# Installs short aliases into ~/.bashrc (or custom rc path).
# Usage:
#   scripts/install_ralph_aliases.sh [rc_file]

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
RC_FILE="${1:-${HOME}/.bashrc}"
BEGIN_MARK="# >>> ralph aliases >>>"
END_MARK="# <<< ralph aliases <<<"

mkdir -p "$(dirname "${RC_FILE}")"
touch "${RC_FILE}"

tmp_file="$(mktemp)"
trap 'rm -f "${tmp_file}"' EXIT

awk -v begin="${BEGIN_MARK}" -v end="${END_MARK}" '
  $0 == begin { skip=1; next }
  $0 == end { skip=0; next }
  !skip { print }
' "${RC_FILE}" > "${tmp_file}"

cat >> "${tmp_file}" <<EOF
${BEGIN_MARK}
alias ralph='bash ${ROOT_DIR}/scripts/ralphctl.sh'
alias ron='ralph on'
alias roff='ralph off'
alias rstat='ralph status'
alias rkick='ralph kick'
alias rscout='ralph scout'
${END_MARK}
EOF

mv "${tmp_file}" "${RC_FILE}"
echo "Installed Ralph aliases into ${RC_FILE}"
echo "Run: source ${RC_FILE}"
