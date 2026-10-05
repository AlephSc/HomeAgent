#!/bin/bash
# deploy.sh — build & deploy aleph-agent dari laptop (Windows Git Bash / WSL) ke aleph
# Pemakaian: bash deploy.sh [host]  (default root@192.168.0.19)
set -e

HOST="${1:-root@192.168.0.19}"
KEY="${ALEPH_KEY:-$HOME/hermes_tmp_key}"
LOCAL_BIN="aleph-agent-linux"
REMOTE_DIR="/opt/aleph-agent"

echo "== build linux =="
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o "$LOCAL_BIN" .

echo "== stop service =="
ssh -i "$KEY" -o StrictHostKeyChecking=no "$HOST" "systemctl stop aleph-agent"

echo "== upload =="
scp -i "$KEY" -o StrictHostKeyChecking=no "$LOCAL_BIN" "$HOST:$REMOTE_DIR/aleph-agent"

echo "== start & verify =="
ssh -i "$KEY" -o StrictHostKeyChecking=no "$HOST" "
chmod 700 $REMOTE_DIR/aleph-agent
systemctl start aleph-agent
sleep 4
systemctl is-active aleph-agent && echo 'deploy OK' || { echo 'DEPLOY GAGAL'; journalctl -u aleph-agent -n 10 --no-pager; exit 1; }
journalctl -u aleph-agent -n 4 --no-pager | tail -n 4
"
