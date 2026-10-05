# Build & Deploy

## Prasyarat
- Go 1.23+ (build dari Windows: `C:\Go\bin\go`)
- Server: Debian/Ubuntu + systemd; user root atau sudo

## Build
```cmd
cd /d D:\Aleph\aleph-agent
set CGO_ENABLED=0&& set GOOS=linux&& set GOARCH=amd64
C:\Go\bin\go build -o aleph-agent-linux .
gzip -k -9 -f aleph-agent-linux
```
Verifikasi dulu: `go vet ./...` dan `go test ./internal/... -count=1` (GOOS kosongkan untuk test).

## Deploy
```bash
scp -o ConnectTimeout=15 aleph-agent-linux.gz server:/opt/aleph-agent/vNN.gz

ssh server "cd /opt/aleph-agent && \
  cp -f aleph-agent aleph-agent.prev && \
  gunzip -cf vNN.gz > aleph-agent-vNN && chmod +x aleph-agent-vNN && \
  systemctl stop aleph-agent && mv -f aleph-agent-vNN aleph-agent && \
  systemctl start aleph-agent && sleep 3 && systemctl is-active aleph-agent"
```
- `.prev` = bahan rollback aleph-guard. Jangan pernah deploy tanpa memperbarui `.prev` dulu.
- Verifikasi pasca-deploy: `journalctl -u aleph-agent --since '-1 min' | grep 'total tools'` dan `ps -o rss=,comm= -C aleph-agent`.
- scp kadang "Connection reset by peer" → tunggu 5 dtk, retry (pola terbukti).

## Setup awal server
1. Binary + `config.yaml` (chmod 600) di `/opt/aleph-agent/`.
2. Unit systemd `/etc/systemd/system/aleph-agent.service` (`Restart=on-failure`).
3. Drop-in `/etc/systemd/system/aleph-agent.service.d/override.conf`:
   ```ini
   [Service]
   MemoryMax=400M
   CPUQuota=60%
   ```
   (atau kelola via menu ⚙️ → 🖥 Systemd RAM — butuh `deploy/aleph-memctl.sh` terpasang di `/usr/local/sbin/` + sudoers whitelist.)
4. Guard: salin `deploy/aleph-guard.sh` → `/usr/local/sbin/`, `aleph-guard.service` + `aleph-guard.timer` → `/etc/systemd/system/`, `systemctl enable --now aleph-guard.timer`.
5. Web UI bind Tailscale IP — pastikan tailscale up.

## Rollback manual
```bash
ssh server "cd /opt/aleph-agent && systemctl stop aleph-agent && \
  cp -f aleph-agent aleph-agent.broken && cp -f aleph-agent.prev aleph-agent && \
  systemctl start aleph-agent"
```

## Scaling ke server besar
Tanpa rebuild: naikkan semua config dari menu ⚙️ (custom hingga context 1M, memory 64 GB, sub-agent 16, timeout 1 jam) dan Systemd RAM (400M → 2G → ∞). Bot restart otomatis via helper saat systemd RAM diubah.
