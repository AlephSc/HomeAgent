#!/bin/bash
# aleph-guard (G6) — rollback otonom di luar proses bot.
# Dijalankan systemd timer tiap 2 menit. TIDAK BOLEH bisa dimatikan/diubah oleh bot:
# file milik root, bot berjalan sebagai user 'aleph' (atau root tapi guard di luar sandbox-nya).
#
# Logika:
#  - Baca restart counter dari journal (systemd RestartCount tidak diekspos; pakai waktu start terakhir).
#  - Jika bot restart >= 3x dalam 10 menit -> rollback ke binary cadangan sebelumnya (aleph-agent.prev).
#  - Tulis keputusan di /opt/aleph-agent/data/guard.log.
set -u
BASE=/opt/aleph-agent
CUR="$BASE/aleph-agent"
PREV="$BASE/aleph-agent.prev"
LOG="$BASE/data/guard.log"
STATE="$BASE/data/guard-state"
WINDOW=600   # 10 menit
THRESH=3

log() { echo "$(date '+%F %T') $*" >> "$LOG"; }

# hitung restart: jumlah baris "Started aleph-agent" dalam window
count=$(journalctl -u aleph-agent --since "-${WINDOW}s" --no-pager 2>/dev/null | grep -cE "Started|Starting" || true)

if [ "$count" -lt "$THRESH" ]; then
  [ -f "$STATE" ] && rm -f "$STATE"
  exit 0
fi

# sudah pernah rollback di crash-loop ini? jangan loop rollback
if [ -f "$STATE" ]; then
  log "crash-loop berlanjut setelah rollback — butuh campur tangan manual"
  exit 0
fi

if [ ! -f "$PREV" ]; then
  log "crash terdeteksi ($countx/${WINDOW}s) tapi tidak ada cadangan .prev — lewati"
  touch "$STATE"
  exit 0
fi

systemctl stop aleph-agent
cp -f "$CUR" "$BASE/aleph-agent.bad.$(date +%s)"
mv -f "$PREV" "$CUR"
chmod 700 "$CUR"
systemctl start aleph-agent
touch "$STATE"
log "ROLLBACK dilakukan (crash $countx dalam ${WINDOW}s) — versi buruk disimpan .bad.*"
