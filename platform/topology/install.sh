#!/usr/bin/env bash
# Serves the interactive lab topology (topology.html) on :3002 for the Instruqt "Topology" tab.
set -euo pipefail
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cat > /etc/systemd/system/lab-topology.service <<UNIT
[Unit]
Description=Lab topology page
After=network-online.target
[Service]
ExecStart=/usr/bin/python3 -m http.server 3002 --bind 0.0.0.0 --directory ${DIR}
Restart=always
[Install]
WantedBy=multi-user.target
UNIT
systemctl daemon-reload
systemctl enable --now lab-topology >/dev/null
systemctl restart lab-topology
for i in $(seq 1 20); do
  curl -fs -o /dev/null http://127.0.0.1:3002/topology.html && { echo "  topology page on :3002"; exit 0; }
  sleep 1
done
echo "  topology page did not answer on :3002" >&2; exit 1
