# ROADMAP aleph-agent — Plan Perbaikan + Penambahan Fitur

> Disusun dari hasil brainstorm + bug nyata yang terjadi di lapangan.
> Prinsip: bot harus "sangat terpakai", RAM < 150 MB, bahasa Indonesia, verifikasi nyata di tiap fase.

## Status Saat Ini
- **Live**: v0.3.1-F3 (commit be9b46e) — F1 fastpath, F2 agent path (Anti-Dawn via 9Router),
  F3 alert engine, docker-aware watch, max_tool_calls=20.
- RAM ~12 MB, guardrail MemoryMax=200M + CPUQuota=30%.

## Bug Nyata yang Jadi Dasar Plan Ini
1. **Foto → balasan Mandarin** — gateway TG belum handle photo; tidak ada guard bahasa.
2. **Tugas "9Router auto start" → context deadline exceeded (150s)** — bot ditolak sandbox
   (write ke /etc/systemd/system, systemctl tidak di whitelist) lalu retry nekat sampai
   deadline; error mentah Go dikirim ke user. Chat biasa normal (8.7s) → bukan masalah jaringan.

---

## FASE 0 — Hotfix Cepat (prioritas tertinggi, ~1 sesi kerja)
Perbaikan yang membuat bot tidak "menyerah dengan cara jelek":

- [ ] **Guard bahasa Indonesia** — jaminan output selalu Indonesia:
  - system prompt diperkuat (jawaban, error, fallback = Indonesia)
  - fallback/error message di kode diganti teks manusiawi (bukan `⚠️ Agent error: <raw Go error>`)
- [ ] **Permission-aware failure** — tool ditolak karena izin/sandbox:
  - STOP retry (jangan habiskan 20 langkah)
  - lapor: "Saya tidak punya izin untuk X — butuh akses: Y" + saran langkah
- [ ] **Timeout lebih ramah** — sebelum deadline, model diminta menyimpulkan
  (sudah ada untuk budget; tambahkan untuk sisa waktu < 15s)

## FASE A — Tiered Access (akses berjenjang, ~2 sesi)
Bot tetap root di OS, tapi aksi dibatasi pagar berjenjang (config-driven):

- [ ] **4 tier di config.yaml** (`permissions:`):
  - 🟢 `free`: baca file, docker ps, status service, journalctl, df, free, uptime → langsung
  - 🟡 `gated`: tulis/edit /etc/systemd/system, config dnsmasq.d, restart service non-kritis →
    eksekusi **+ backup otomatis file lama** + audit log
  - 🔴 `confirm`: stop/disable service kritis (dnsmasq/docker/tailscaled), edit
    network/interfaces, iptables → **tanya user dulu** (konfirmasi via Telegram: "⚠️ akan X, lanjut?")
  - ⛔ `forbidden`: rm -rf /, dd, format, hapus config aleph-agent sendiri, baca file kredensial
- [ ] **Confirm flow Telegram**: prompt konfirmasi → user balas "ya"/"batal" (timeout 60s)
- [ ] **Audit log**: semua aksi 🟡🔴 tercatat (journal + file); fastpath baru `/audit`
- [ ] **Update sandbox & whitelist**: + /etc/systemd/system (tier 🟡), + systemctl (gated)
- [ ] **Implement prompt untuk tier** (agent tahu kemampuan & batasannya)

**Acceptance test Fase A**: kasus nyata kemarin — "9Router dijadikan auto startup" dikerjakan
penuh oleh BOT sendiri via Telegram (buat unit file → daemon-reload → enable → verify),
tanpa error, tanpa bahasa asing. (9Router saat ini node proses tanpa unit systemd.)

## FASE B — Media & UX (~1 sesi)
- [ ] **Handler photo**: kalau 9Router punya model vision → kirim ke model (tool vision);
      kalau tidak → respon rapi bahasa Indonesia ("saya belum bisa melihat gambar...")
- [ ] **Handler document/text-file**: baca isi (batas ukuran), jadikan konteks agent
- [ ] **Live progress narration** (meniru pola CodeBuddy — narasi tiap langkah kerja):
  - Saat agent mulai: kirim 1 pesan progress ("🔧 mengerjakan: <ringkasan tugas>")
  - Tiap tool call → update pesan yang SAMA via `editMessageText` (anti-spam, tak kena
    rate limit Telegram), format: "🔍 cek status service... ✅ / 📝 tulis unit file... ⏳"
  - Narasi DETERMINISTIK 0 token: template per tool (nama tool + argumen ringkas → kalimat),
    BUKAN minta LLM menulis narasi (hemat token & latency)
  - Selesai: edit jadi "✅ selesai dalam 12.3s" + jawaban final dikirim sebagai pesan baru
  - Kalau tool ditolak permission (Fase 0): progress menunjukkan "⛔ ditolak: butuh izin X"
    sehingga user paham KAPAN dan KENAPA bot berhenti (bukan deadline 150s diam-diam)
  - Progress jangan kirim isi sensitif (argumen berisi path kredensial → disamarkan)
- [ ] **Typing indicator** tetap jalan bersamaan (chat action)
- [ ] **Anti-drift bahasa**: cek output akhir; fallback paksa Indonesia

## FASE C — Memory, Skill & Auto-Learning (inti "agent seperti Hermes", ~3 sesi)
- [ ] **F5: Memory + Session** — SQLite FTS5 via `modernc.org/sqlite` (murni Go, tanpa CGO):
  - session per chat (riwayat singkat masuk konteks)
  - memory eksplisit: fakta penting tersimpan, di-recall saat relevan
- [ ] **F5b: Skill system**:
  - folder `/opt/aleph-agent/skills/*.md` (nama + deskripsi terindeks di system prompt)
  - tool `recall_skill(name)` — baca prosedur saat relevan
  - tool `save_skill(name, content)` — user bisa mengajari bot lewat chat
  - starter skills: docker-management, router-recovery (SR9700), 9router-ops
- [ ] **F5c: Auto-lesson** (belajar dari pengalaman):
  - pemicu: tool error sama ≥2× atau koreksi eksplisit user
  - lesson ditulis ke `lessons.md` setelah lolos review model (anti sampah)
  - lesson top-N dimuat ke system prompt (budget token terjaga)
- [ ] **Kurator**: rutin merapikan memory/lesson (dedup, buang usang)

## FASE E — Shortcut Buttons & Inline UI (permintaan user, 2026-10-02)
- [ ] **Inline keyboard menu utama** (`/menu` atau reply keyboard):
  - 📊 Status · 🏓 Ping · ⚡ Speedtest · 💻 Run Command · 🔧 Services
- [ ] **Status button** → jalankan server_status (fast, 0 token) + edit pesan dengan hasil
- [ ] **Ping button** → ping google.com & cloudflare.com (3x masing-masing) →
      hasil rapi: latency ms + loss; ping = gated_exec 🟡 (ter-audit)
- [ ] **Speedtest button** → jalankan speedtest-cli (perlu install di aleph; kalau berat,
      alternatif: curl ke speedtest endpoint ringan) → tampilkan download/upload/ping;
      jalankan sebagai gated + progress "⏳ speedtest berjalan ±30 dtk..." (edit pesan)
- [ ] **Run Command button** → tanya input command (pakai mekanisme pending yang sama
      dengan konfirmasi) → eksekusi → output via editMessageText (realtime bila bisa;
      kalau kosong → "output kosong"); TETAP lewat permission engine (whitelist/gated)
- [ ] **Service shortcuts** → tombol start/stop/restart/status per service populer:
      9router, tailscaled, docker, adguardhome (docker:), casaos, dnsmasq, aleph-agent;
      aksi tetap lewat tiered access (🔴 konfirmasi utk service kritis)
- [ ] **Callback handler** di gateway TG (query.Button callback, bukan cuma text message)
- [ ] **Anti-drift bahasa level kode**: cek output final agent — kalau mengandung blok
      CJK/Han dan user tidak minta terjemahan, re-ask model sekali dengan instruksi
      "jawab ULANG dalam Bahasa Indonesia" (fallback dari prompt-only guard)
      — dari bug nyata: bot balas Mandarin saat user minta analisis foto via teks

## FASE F — Otak Ekspanded: Auto-Compacting 64k + Memory Penuh + Builder (disetujui 2026-10-02)
Keputusan user: **context verbatim 64k token** untuk compacting — hemat & konsisten.

### F6a — Auto Context Compacting (ambang 64k, configurable)
- [x] `context_max_tokens: 65536` di config.yaml (BUKAN hardcoded; ganti tanpa rebuild)
- [x] Estimasi token per pesan (≈ len/4); total dihitung tiap request
- [x] Saat riwayat > ambang: pesan lama diringkas 1x LLM call → **running summary**
      per chat (kolom `summary` di SQLite) — merge ringkasan lama + pesan yang keluar jendela
- [x] Struktur konteks final: [system+skills+lessons] + [ringkasan] + [verbatim ≤64k]
- [x] Compacting jalan ASYNC setelah balasan terkirim (user tak menunggu)
- [x] Guard luap: model via /model diganti ke jendela kecil → pangkas verbatim otomatis
- [x] Chat harian tetap ringan: pemicu ringkas bisa lebih awal utk riwayat tak relevan

### F6b — Persistent Memory Penuh
- [x] **Auto-recall**: sebelum menjawab, cari memory relevan (keyword dari pesan user) → suntik ke konteks diam-diam
- [x] **Konsolidasi berkala** (kurator): dedup memory mirip, gabungkan, usangkan yang tak pernah di-recall 30 hari
- [x] Memory difaktorkan per jenis: preferensi user / fakta perangkat / keputusan proyek

### F10 — Builder Capability (bot bisa MEMBUAT app/web dari prompt)
- [x] **F10a Workspace**: `/opt/aleph-agent/projects/<nama>/` per proyek (isolasi, hapus utuh)
      + tools `scaffold_project` (template static/Go/Flask/Node), `run_project` (port dedicated),
      `verify_project` (curl endpoint → cek 200/konten), `stop_project`
- [x] **F10b Loop builder**: spesifikasi → struktur → implementasi per file → SELF-VERIFY
      (curl sendiri → perbaiki) → laporkan URL; safety: tulis bebas di projects/, buka port = 🔴 konfirmasi
- [x] **F10c Hosting**: static via Caddy/nginx ringan; app dinamis via Docker (memory limit);
      acceptance test nyata: "buatkan web dashboard status server"
- [x] Catatan batas model: glm-flash andal utk web statis/script kecil; app kompleks butuh
      model berat via /model (loop self-verify menutup sebagian gap)

## FASE D — Otonomi & Parity (sesudah fondasi kuat)
- [x] **F7: Cron & scheduled tasks** — "tiap pagi lapor status", "cek tiap jam";
      verification loop (aksi → cek hasil → lapor)
- [x] **F8: MCP client** — sambung MCP server eksternal via API (bukan server lokal)
- [x] **F4: Discord parity** — fastpath + agent path + tiered access di Discord
- [x] **F9: Hardening** — pprof endpoint internal, stress test 7 hari, memory leak check,
      rotasi log, dokumentasi operasional

---

## Prinsip Eksekusi
1. Satu fase = satu commit + deploy + verifikasi nyata (tes dari Telegram, bukan asumsi).
2. Deploy flow terkunci: build laptop → stop service → scp → start → cek journal + RAM.
3. Kredensial TIDAK PERNAH dituliskan ulang ([REDACTED] saja).
4. Setiap fitur baru wajib lulus "tes 9Router auto-start"-nya sendiri: tugas nyata yang
   dulu gagal harus berhasil.

---

## STATUS EKSEKUSI (2026-10-02) — SELURUH PLAN SELESAI ✅
- **F6a+F6b**: v0.7.0-F6 (commit 64b279c) — compacting 64k configurable, running summary, auto-recall, kurator 6 jam
- **F10**: v0.8.0-F10 (commit f5fbb18) — 5 tools builder, 18 tools total, workspace /opt/aleph-agent/projects
- **Fase D**: v0.9.0-D (commit 2f26a66) — F7 cron (cron.json + verify + alert), F8 MCP client stdio,
  F4 Discord gateway (discordgo), F9 pprof opsional
- Verifikasi produksi: systemd active, 18 tools, RAM bot ~19 MB RSS, 9Router OK (HTTP 200)
- Sisa (di luar kode): Brave API key (user), token Discord bot (user), tes F10c end-to-end via chat

---

## GEL. G1 — F-perm v2: Akar Masalah "Approve Lebih dari 1×" (PRIORITAS #1, disetujui 2026-10-03)

Diagnosis dari log produksi (chatlog 2026-10-03.log + journal):
- `grep -E "hostapd|dnsmasq"` → pemecah segmen memecah `|` DI DALAM KUTIP → head palsu
  `dnsmasq` dianggap program → diprompt (padahal bagian argumen grep)
- Klasifikasi per-head menampilkan FULL command di prompt → head `ps` ditanya, user jawab
  "always" → lalu head `grep` ditanya dengan COMMAND YANG SAMA terlihat → user menilai
  bot "menanya 2×" untuk hal yang sama. 3 prompt beruntun utk 1 command = keluhan user.

- [x] **G1a Tokenizer sadar-kutip** — `splitSegments`: `|` `;` `&&` `||` di dalam
      `"..."` / `'...'` TIDAK dianggap pemisah (parser state sederhana inQuote) ✅ commit 52c206e
- [x] **G1b Satu prompt untuk SEMUA head belum-diputuskan** — kumpulkan head tanpa
      keputusan → 1× AskFunc dengan daftar: "Program: `ps`, `grep` — izinkan semua?"
      → sekali "always" tersimpan utk semua head sekaligus (bukan per-head berulang) ✅ 52c206e
- [x] **G1c Prompt menampilkan head** — teks prompt: command utuh + "Program: <heads>" ✅ 52c206e
- [x] **G1d Singleflight** — BONUS temuan dari log: model kerap mengirim 2 tool call IDENTIK
      dalam satu round → ClassifyExec paralel → 2 prompt utk command sama (bukti: keputusan #7
      & prompt #8 di 20:05:06, 1 detik). Command identik yang menunggu keputusan kini berbagi
      1 prompt via inFlight map ✅ 52c206e
- [x] **G1e Budget verbatim realistis** — BONUS: "context deadline exceeded" berulang akarnya
      bukan permission: 64k verbatim × 8 round di N2600 = inference menit/round → deadline.
      Default 65536 → 8192 + deadline-aware wrap-up (sisa <90s → jawaban rangkuman, bukan
      error) ✅ commit 30859ce

**Acceptance test G1**: ✅ diverifikasi lewat log — command `grep -E "upstream_dns|..."`
yang tadinya 2 prompt beruntun kini harusnya 1 prompt, sekali jawab. Verifikasi final
menunggu tes user via Telegram.

## GEL. G2 — Narrasi Progress Detail ala Hermes (keluhan user #2)

Saat ini: "💻 jalankan command..." generik. Target: spesifik per tool + durasi.

- [x] **G2a progressStep dinamis dengan argumen nyata**:
      - exec_command → `💻 $ ps aux | grep -E "hostapd|dnsmasq"` (command dipendekkan 60 char)
      - write_file → `📝 tulis /etc/hostapd/hostapd.conf (240 B)`
      - service_control → `⚙️ start dnsmasq-hotspot`
      - read_file/list_files/scaffold_project → path tujuan
      - path/argumen sensitif (kredensial) tetap disamarkan
- [x] **G2b baris selesai diberi tanda + durasi**: `✔ 💻 $ ps aux ... (2.3s)`
      — agent.go mencatat start per tool, Progress menerima status done
- [x] **G2c counter langkah**: header progress `🤔 langkah 4/20 — <tugas>`
- [x] **G2d label nama user di chatlog** (sekarang `USER [id/]` kosong) —
      gateway meneruskan `msg.From.FirstName`

**Acceptance test G2**: tugas berantai hotspot → progress menampilkan command nyata
yang berjalan + durasi per langkah, user paham bot sedang apa tanpa buka audit log.

## GEL. G3 — Tombol Pilihan Ganda `ask_user` (permintaan user #1)

Tool baru agar model bisa bertanya dengan opsi klik (seperti fitur clarify di Hermes):

- [x] **G3a Tool `ask_user(question, options[], multi_select?)`** di registry tools
- [x] **G3b Gateway Telegram**: kirim pesan + inline keyboard opsi (2 per baris) +
      tombol "✍️ Lainnya..." (replykeyboard force_reply / pending text input utk jawaban bebas)
- [x] **G3c Jawaban dikembalikan ke model sebagai tool result** → agent lanjut kerja
      (infrastruktur reuse: pendingAct + callback handler + askPermission pattern)
- [x] **G3d Timeout 5 menit** → hasil "(tidak dijawab)" agar model punya fallback
- [x] **G3e Prompt diperbarui**: sarankan model memakai ask_user saat pilihan
      bercabang (mis. skema IP, mode install) daripada menebak

**Acceptance test G3**: "buatkan AP-nya, tanya dulu mau SSID apa" → bot bertanya
via tombol pilihan + jawab bebas → lanjut eksekusi sesuai jawaban.

**Implementasi (selesai)**: tool `ask_user` di agent (bukan registry) via
`Agent.AskUserFn` (intersep `execTool`), gateway `ask_user.go` — keyboard 1 opsi/baris,
`✍️ Jawab bebas` → konsumsi teks berikutnya (`consumeFreeAsk`), timeout **120s**
(bukan 5m — N2600 + UX: agent lanjut dgn asumsi), expired prompt diberi tanda.

## GEL. G4 — Konteks Benar-Benar Dipahami (keluhan "agent lupa")

Fakta DB: 24 session rows, `chat_summary` KOSONG — running summary belum pernah
terbentuk; verbatim disuntik tapi model kadang tidak memperlakukannya sebagai memorinya.

- [x] **G4a Prompt konteks eksplisit** — blok verbatim/summary diberi framing:
      "Berikut ingatan percakapan Anda sendiri dengan user ini (BUKAN kutipan orang lain).
      Anda SUDAH melakukan ini bersama user sebelumnya."
- [x] **G4b Summary proaktif** — jangan tunggu overflow 64k: setiap sesi melewati
      20 pesan, ringkasan dibuat/diperbarui (async, murah) → chat_summary selalu terisi
- [x] **G4c "ingat/catat/jangan lupa" auto-save** — frasa perintah mengingat dari user
      → langsung `Put()` memory permanen (0 token LLM) + konfirmasi singkat
- [x] **G4d Recalled memory ditandai** — auto-recall yang disuntik diberi label jelas
      di konteks agar model tidak mengira halusinasi sendiri
- [x] **G4e Chatlog + journal** memuat event `[compact]` dan recall → verifikasi mudah

**Acceptance test G4**: sesi panjang > 20 pesan → chat_summary terisi (cek DB);
"cobalagi" setelah task gagal → agent tahu task apa yang dimaksud tanpa diulang.

## Urutan Eksekusi
1. **G1 dulu** (approve ganda = irritasi terbesar), deploy + verifikasi
2. **G2 + G4** satu gelombang (sama-sama menyentuh progress/konteks), deploy
3. **G3** terakhir (fitur baru terbesar, reuse hasil G1)

## HARDENING (review pasca-G1–G4) — selesai, commit 92b264e
- [x] **splitSegments ditulis ulang penuh quote-aware** — unit test MENEMKAN bug nyata:
      (1) `&&`/`||` di dalam kutip ikut di-replace jadi `;` (command ter-mangle),
      (2) `|` tunggal tertinggal di akhir segmen (segmen "ps aux |").
      Parser baru: tidak ada pre-replace; `|`, `||`, `&&`, `;` hanya pemisah di luar kutip;
      escape `\` di dalam kutip dihormati.
- [x] **Unit test G1** (internal/perm/perm_test.go) — 2 test, PASS; test pertama di repo.
- [x] **Done-line matcher** — ProgressDone mencocokkan via teks step (bukan "baris terakhir"),
      multi tool call per round tidak lagi salah pasang.
- [x] **/batal** membersihkan freeAskWait + semua pertanyaan ask_user terbuka.
- [x] **Anti-leak** — timeout/jawaban ask_user membersihkan tunggu jawab-bebas (freeAskWait).
- [x] **Summary proaktif de-dupe** — summarizedCount per chat; ringkasan hanya utk pesan
      BARU (sebelumnya ringkasan sama diproses berulang tiap pesan).
- [x] **ask_user masuk progress narration + chatlog** (event TOOL).
- [x] **go vet bersih**; build linux OK; deploy 21:03 sukses (service active).
