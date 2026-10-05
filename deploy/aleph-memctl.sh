#!/bin/bash
# aleph-memctl.sh — G13b: helper set MemoryMax aleph-agent (satuan MB, 64-65536, atau infinity).
# Dipanggil via sudo dari aleph-agent (whitelist sudoers). TIDAK menerima arg lain.
set -euo pipefail
VAL="${1:-}"
case "$VAL" in
  ''|*[!0-9]*)
    if [ "$VAL" != "infinity" ]; then
      echo "ERR: argumen harus angka MB (64-65536) atau 'infinity'" >&2
      exit 1
    fi
    ;;
esac
if [ "$VAL" != "infinity" ]; then
  if [ "$VAL" -lt 64 ] || [ "$VAL" -gt 65536 ]; then
    echo "ERR: di luar rentang 64-65536 MB" >&2
    exit 1
  fi
  VAL="${VAL}M"
fi
DIR=/etc/systemd/system/aleph-agent.service.d
mkdir -p "$DIR"
# tulis override (MemoryMax saja; CPUQuota dipertahankan bila sudah ada)
if [ -f "$DIR/override.conf" ] && grep -q '^CPUQuota=' "$DIR/override.conf"; then
  CPUQ=$(grep '^CPUQuota=' "$DIR/override.conf")
  printf '[Service]\nMemoryMax=%s\n%s\n' "$VAL" "$CPUQ" > "$DIR/override.conf"
else
  printf '[Service]\nMemoryMax=%s\n' "$VAL" > "$DIR/override.conf"
fi
systemctl daemon-reload
systemctl restart aleph-agent
sleep 2
ST=$(systemctl is-active aleph-agent)
EFF=$(systemctl show aleph-agent -p MemoryMax --value)
echo "OK status=$ST MemoryMax_bytes=$EFF"
