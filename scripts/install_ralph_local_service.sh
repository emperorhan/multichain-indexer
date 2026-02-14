#!/usr/bin/env bash
set -euo pipefail

# Install or update user systemd service for Ralph local loop.
# Usage:
#   scripts/install_ralph_local_service.sh [service_name]

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SERVICE_NAME="${1:-ralph-local}"
UNIT_DIR="${HOME}/.config/systemd/user"
UNIT_FILE="${UNIT_DIR}/${SERVICE_NAME}.service"

mkdir -p "${UNIT_DIR}"

cat >"${UNIT_FILE}" <<EOF
[Unit]
Description=Ralph Local Loop Supervisor
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
WorkingDirectory=${ROOT_DIR}
ExecStart=/bin/bash ${ROOT_DIR}/scripts/ralph_local_systemd_entry.sh
Restart=always
RestartSec=5
TimeoutStopSec=20
KillMode=mixed

[Install]
WantedBy=default.target
EOF

systemctl --user daemon-reload
systemctl --user enable "${SERVICE_NAME}.service" >/dev/null

echo "Installed ${UNIT_FILE}"
echo "Next:"
echo "  systemctl --user start ${SERVICE_NAME}.service"
echo "  systemctl --user status ${SERVICE_NAME}.service --no-pager"
