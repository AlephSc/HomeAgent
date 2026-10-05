# aleph-agent — Home AI Assistant

Bot Telegram + AI Agent self-hosted untuk home server. **Hybrid**: slash command instan (0 token) **dan** percakapan natural berbasis LLM (via 9Router, OpenAI-compatible). Satu proses Go, ringan — riil ~25 MB RAM, cocok untuk perangkat kelas Atom hingga server besar (skala penuh via config runtime).

**Live saat ini di home server "aleph"** (Debian 12, Atom N2600, 2 GB RAM) — 34 tools, 11 command, self-extension aktif.

---

## ✨ Fitur

### 🤖 Agent Core
- **Loop ReAct** dengan budget tool-call per tugas, compacting konteks (budget + running summary), session per chat
- **34 tools**: exec_command, service_control, file I/O, web_search/web_fetch, server_status, notes, memory, skill, dokumen, PPT, delegasi, self-tool, cron
- **Sub-agent delegation (`delegate_task`)** — konteks bersih, budget sendiri, semaphore paralel (1–16, runtime), anti-rekursi, deadline wajib
- **Ask-user** — agent bertanya dengan pilihan tombol saat keputusan ambigu
- **Reply context** — memahami pesan yang di-reply

### 🧬 Self-Extension (G10) — *bot mengembangkan dirinya sendiri*
- `self_tool_save` — agent **membuat tool baru** (bash/python) untuk dirinya sendiri; syntax-check, blocklist pola berbahaya, quota, persist via manifest, langsung masuk registry
- `self_tool_list/toggle/del` — kelola tool kustom (namespace `x_`)
- Eksekusi tool kustom: params via stdin JSON (anti-injeksi), timeout wajib, env minimal (tanpa API key), lewat permission engine + audit

### 🔁 Hot Reload Menyeluruh (G11) — *nol restart untuk hampir semua config*
- Dynamic prompt rebuild — tool baru langsung "diketahui" agent
- `cron_add/list/del` — agent menjadwalkan tugas berulang sendiri (persist)
- `/reloadapi` — ganti endpoint/API key tanpa restart, health-check dulu, gagal = fallback aman
- Lihat [Hot Reload Matrix](#hot-reload-matrix)

### ⚙️ Config AI (runtime + persist)
Menu Telegram **⚙️ Config AI**: Max Context, Max Tool Calls, Timeout LLM, Model, Max Memory (soft), Max Sub-Agent, Systemd RAM. Semua bisa **preset atau ✏️ custom bebas** (context hingga 1M token, memory hingga 64 GB, sub-agent hingga 16 — siap scale-up). Perubahan **persist otomatis** (`data/agent-config.json`, overlay atomik 0600) dan tahan restart. `♻️ Reset ke Default` menghapus overlay.

### 🗂 Notes (Obsidian-style)
Catatan markdown per kategori, wiki-link `[[Judul]]`, backlink dua arah, pencarian, menu Telegram lengkap.

### 📁 Upload & Ekstraksi
Upload dokumen via Telegram (atau folder inbox untuk file >20 MB), ekstraksi per-halaman + throttle + checkpoint resume, cache `.txt` **permanen** (disk-first, tanpa eviksi). Tools: `list_uploads`, `read_upload`, `upload_info`.

### 📚 Tools Pembelajaran
`make_document` (.md/.txt ke outbox), `make_ppt` (**.pptx asli** — OOXML pure stdlib, tanpa dependensi).

### 🧠 Memory & Skill
- `memory_save/recall` — preferensi & fakta jangka panjang
- `save_skill/recall_skill/skill_patch` — prosedur yang berhasil disimpan & dipakai ulang
- `add_lesson` — pembelajaran dari koreksi user

### 🖥 Operasional Server
- Menu: Status, Ping, Speedtest, Run Command, Services, AP WiFi, 📊 **Usage Resource** (RAM/load/uptime/disk/docker stats/top proses), Notes, Config AI
- Custom service monitor (add/enable/disable, alert down/recovered dengan debounce)
- Alert engine non-AI: disk >90%, RAM, service watch — 0 token

### 🛡 Keamanan Berlapis
- **Permission engine**: whitelist read-only, gated exec, prompt izin 4-pilihan (once/always/session/deny), blocklist destruktif, audit trail
- **aleph-guard** — systemd timer di **luar proses bot**: rollback otomatis ke `.prev` saat crash-loop 3×/10 menit; bot dilarang menyentuhnya
- **aleph-memctl.sh** — helper root-only (sudo whitelist) untuk ubah MemoryMax; satu fungsi, validasi ketat
- Web UI bind Tailscale-only + token; kredensial tidak pernah masuk log/jawaban

### 🌐 Web UI (G3)
`http://<tailscale-ip>:8080` — tab Files (drag-drop), Notes, Config AI. Token via `/cfgweb`. Bind hanya ke interface Tailscale.

---

## Hot Reload Matrix

| Komponen | Hot? | Cara |
|---|---|---|
| Max Context / Tool Calls / Timeout / Model | ✅ | Menu ⚙️ / Web UI / `/model` |
| Max Memory (soft) / Max Sub-Agent | ✅ | Menu ⚙️ (custom hingga 64GB/16) |
| Persist config | ✅ | Otomatis — tahan restart |
| Tool kustom (self-extension) | ✅ | `self_tool_save` — langsung jalan |
| Prompt agent (tool baru) | ✅ | Rebuild otomatis |
| Cron jobs | ✅ | `cron_add` (persist) |
| Endpoint / API key LLM | ✅ | `/reloadapi` (health-check + fallback) |
| Admin IDs | ✅ | `/reloadapi` |
| Systemd MemoryMax / CPUQuota | ✅ | Menu ⚙️ → 🖥 Systemd RAM (via `aleph-memctl.sh`, bot restart otomatis) |
| Skills / Lessons / Notes / Memory / Services | ✅ | Selalu live |
| Telegram token | ❌ | Restart service (sifat koneksi bot) |

---

## 🏗 Arsitektur

```
Telegram ⇄ Gateway ─┬─ Fastpath (slash command, 0 token)
                    ├─ Agent loop (ReAct) ── 9Router /v1 (OpenAI-compatible)
                    │     ├─ Tools registry (34) ─┬─ bawaan
                    │     │                        ├─ self-tool (data/tools/) ← agent buat sendiri
                    │     │                        └─ delegate_task → sub-agent (semaphore, tool subset)
                    │     ├─ Session/Summary/Memory (SQLite)
                    │     └─ Prompt (dynamic rebuild)
                    ├─ Cron scheduler (persist, self-verify)
                    ├─ Alert engine (non-AI)
                    └─ Web UI (Tailscale-only)
aleph-guard (systemd timer, rollback .prev)   ← di luar proses bot
```

Struktur:
```
cmd/main.go
internal/
  agent/       — loop ReAct, compacting, subagent, runtime config, persist, prompt rebuild
  tools/       — registry + 34 tools (termasuk selftool.go, delegate_tools.go, cron_tools.go)
  llm/         — client OpenAI-compatible (mutex model, ListModels cache disk)
  telegram/    — gateway, menu*.go, pending input, perm callback
  webui/       — server HTTP Tailscale-only
  memory/      — SQLite: sessions, summary, memory, notes, uploads, lessons
  perm/        — permission engine (tiers, blocklist, audit)
  cron/        — scheduler + persist + self-verify
  extract/     — ekstraksi dokumen per-halaman + checkpoint
  config/      — load YAML
deploy/        — aleph-guard.sh|.service|.timer, aleph-memctl.sh
```

---

## 🚀 Build & Deploy

### Build (Windows dev → Linux server)
```cmd
set CGO_ENABLED=0&& set GOOS=linux&& set GOARCH=amd64
C:\Go\bin\go build -o aleph-agent-linux .
gzip -9 aleph-agent-linux
```

### Deploy ke server
```bash
scp aleph-agent-linux.gz server:/opt/aleph-agent/vNN.gz
ssh server "cd /opt/aleph-agent && \
  cp -f aleph-agent aleph-agent.prev && \
  gunzip -cf vNN.gz > aleph-agent-vNN && chmod +x aleph-agent-vNN && \
  systemctl stop aleph-agent && mv -f aleph-agent-vNN aleph-agent && \
  systemctl start aleph-agent"
```
`aleph-agent.prev` dipakai aleph-guard untuk rollback otomatis.

### Systemd
```ini
# /etc/systemd/system/aleph-agent.service (+ drop-in .d/override.conf: MemoryMax/CPUQuota)
# /etc/systemd/system/aleph-guard.timer  — timer 2 menit (rollback crash-loop 3x/10 menit)
```

### Config (`config.yaml`, chmod 600)
```yaml
server: {name: aleph, data_dir: /opt/aleph-agent/data}
admins: {telegram_ids: [YOUR_ID]}
token:  {telegram: "BOT_TOKEN"}
llm:
  base_url: "http://router:20128/v1"   # 9Router / endpoint OpenAI-compatible
  api_key:  "..."
  model: "default-model"
  max_tool_calls: 20
  timeout_sec: 180
alert: {enabled: true, disk_pct: 90, ram_min_mb: 250}
```
Semua nilai LLM di atas bisa dioverride runtime dari menu ⚙️ (persist di `data/agent-config.json` — **config.yaml tidak pernah disentuh bot**).

---

## 📱 Command Telegram

| Command | Fungsi |
|---|---|
| `/menu` | Menu tombol lengkap (incl. 📊 Usage Resource) |
| `/status`, `/services`, `/ping` | Kondisi server (instan) |
| `/model` | Lihat/ganti model AI |
| `/files` | Daftar file upload |
| `/memory <kw>` | Cari memory bot |
| `/lesson` | Review lesson |
| `/audit` | 15 aksi terakhir |
| `/cfgweb` | URL + token Web UI |
| `/reloadapi` | Reload endpoint/API dari config.yaml |
| `/help` | Bantuan |

## 🧪 Test
```bash
go vet ./... && go test ./internal/... -count=1
```
Coverage: agent (persist/rentang/compact), tools (selftool save/run/persist/validasi/quota, delegate anti-rekursi, pptx XML), memory (notes), perm, telegram (model paging), fastpath.

## 💬 Riwayat Pengembangan
Transkrip lengkap percakapan pengembangan (F, G1–G13, hardening, GitHub): [`docs/CHATLOG.md`](docs/CHATLOG.md).

## 🗺 Roadmap
Semua gelombang F1–F9 + G1–G13 **selesai** (detail di `ROADMAP.md`, `ROADMAP-G10.md`).
Tersisa opsional: **B3 warn kustom** (alert informasi murni buatan agent, evaluator di luar proses — format disk-manifest mengikuti pola G10) — dalam pertimbangan.

## 📄 Lisensi
Private — proyek pribadi.

---
*aleph-agent v25 · Go 1.23 · ringan: stdlib + tgbotapi + driver SQLite*
