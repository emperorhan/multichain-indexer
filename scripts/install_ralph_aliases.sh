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
alias lralph='bash ${ROOT_DIR}/scripts/ralph_local_control.sh'
alias lrstart='bash ${ROOT_DIR}/scripts/ralph_local_daemon.sh start'
alias lrstop='bash ${ROOT_DIR}/scripts/ralph_local_daemon.sh stop'
alias lrrestart='bash ${ROOT_DIR}/scripts/ralph_local_daemon.sh stop && bash ${ROOT_DIR}/scripts/ralph_local_daemon.sh start'
alias lrstatus='bash ${ROOT_DIR}/scripts/ralph_local_runtime_status.sh'
alias lrsvcstatus='systemctl --user status ralph-local.service --no-pager'
alias lrtail='journalctl --user -u ralph-local.service -f'
alias lrcheck='bash ${ROOT_DIR}/scripts/ralph_local_runtime_status.sh'
alias lrwhat='lrcheck'
alias lragents='bash ${ROOT_DIR}/scripts/ralph_local_agent_tracker.sh'
alias lrtrack='lragents'
alias lrinvariants='bash ${ROOT_DIR}/scripts/ralph_invariants.sh list'
alias lrcontract='bash ${ROOT_DIR}/scripts/ralph_issue_contract.sh validate'
alias lrplancheck='bash ${ROOT_DIR}/scripts/validate_planning_output.sh'
alias lrpreflight='bash ${ROOT_DIR}/scripts/ralph_local_preflight.sh'
alias lrsvcinstall='bash ${ROOT_DIR}/scripts/install_ralph_local_service.sh'
alias lrdaemonstart='bash ${ROOT_DIR}/scripts/ralph_local_daemon.sh start'
alias lrdaemonstop='bash ${ROOT_DIR}/scripts/ralph_local_daemon.sh stop'
alias lrdaemonstatus='bash ${ROOT_DIR}/scripts/ralph_local_daemon.sh status'
alias lrdaemontail='bash ${ROOT_DIR}/scripts/ralph_local_daemon.sh tail'
alias lrmainpush='bash ${ROOT_DIR}/scripts/enable_direct_main_push.sh'
alias lrdoctor='bash ${ROOT_DIR}/scripts/ralph_local_doctor.sh'
alias lrauth='bash ${ROOT_DIR}/scripts/codex_auth_status.sh'
alias lron='lrstart'
alias lroff='lrstop'
alias lrstat='lrstatus'
alias lrrun='bash ${ROOT_DIR}/scripts/ralph_local_run.sh'
alias lrinit='bash ${ROOT_DIR}/scripts/ralph_local_init.sh'
alias lrnew='bash ${ROOT_DIR}/scripts/ralph_local_new_issue.sh'
${END_MARK}
EOF

mv "${tmp_file}" "${RC_FILE}"
echo "Installed Ralph aliases into ${RC_FILE}"
echo "Run: source ${RC_FILE}"
