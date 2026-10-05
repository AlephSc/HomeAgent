# 📋 PLAN G10 — Script-Tool Self-Extension
## Agent membuat, memuat, dan mengelola tools-nya sendiri (tanpa compile ulang)

**Status: PLAN — menunggu persetujuan user. Tidak ada kode ditulis.**

---

## 1. Konsep

Agent mendapat kemampuan **menambah tool baru ke dirinya sendiri** dengan menulis
script (bash/python) + manifest JSON ke folder `data/tools/`. Bot memuat script
itu sebagai **tool reguler** yang bisa dia panggil seperti tool bawaan — muncul
di registry, bisa dipakai loop ReAct, dan (opsional) bisa dipakai sub-agent.

```
Agent: "aku butuh tool cek-suhu"
  └─ tool self_tool_save(name, desc, params, script, lang)
       → validasi ketat (nama, ukuran, syntax check)
       → tulis data/tools/<name>.json + <name>.sh/.py   (disk, atomik 0600)
       → load langsung ke registry (hot) + persist restart berikutnya
  └─ agent memanggil cek_suhu(...) seperti tool biasa
       → runner eksekusi script via perm engine (timeout wajib)
       → stdout = hasil tool
```

## 2. Struktur file (disk-first, sesuai prinsip server)

```
data/tools/
  manifest.json          ← daftar tool kustom [{name, desc, params, lang, enabled, created}]
  <name>.sh | <name>.py  ← isi script (satu file per tool)
```

- Semua di disk; RAM hanya saat eksekusi. Restart → load ulang dari manifest.
- Tidak ada batas jumlah folder (disk 421 GB bebas), tapi per-file dibatasi.

## 3. Tools baru (4)

| Tool | Fungsi |
|---|---|
| `self_tool_save` | Buat/timpa tool kustom: nama, deskripsi, params schema, script, lang (bash/python) |
| `self_tool_list` | Daftar tool kustom + status enabled |
| `self_tool_del` | Hapus tool kustom (bukan bawaan) |
| `self_tool_toggle` | Enable/disable tanpa hapus |

> Nama di-*namespace*-kan `x_` (mis. `x_cek_suhu`) agar tak bentrok tool bawaan
> dan langsung terlihat asalnya. Manifest menandai sumbernya.

## 4. Aturan validasi `self_tool_save` (hardening inti)

1. **Nama**: regex `^[a-z0-9_]{3,32}$`, WAJIB prefix `x_`, tolak jika bentrok nama tool bawaan/blocklist (`delegate_task`, `exec_command`, `service_control`, dsb.)
2. **Ukuran**: script maks 32 KB; params maks 16 field; deskripsi maks 500 char
3. **Charset script**: tolak `rm -rf /`, `mkfs`, `dd if=`, `:(){`, fork bomb, `curl | sh` — semuanya TETAP lewat perm engine saat eksekusi (double-layer), tapi ditolak sejak awal di save
4. **Syntax check saat save**: `bash -n` untuk bash, `python3 -m py_compile` untuk python → script rusak ditolak sebelum masuk registry
5. **Tulis atomik** (tmp+rename), perm 0600
6. **Quota**: maks 20 tool kustom (anti-spam registry; bisa dinaikkan lewat config kalau perlu)

## 5. Eksekusi (runtime)

- Runner: `exec.CommandContext` — **timeout wajib** (default 30 dtk, maks 120; dipilih saat save, divalidasi) — pelajaran langsung dari hardening H40/H41
- **Permission engine**: eksekusi lewat `ClassifyExec` sama seperti Run Command — blocklist, gated, audit. Tool kustom TIDAK di atas hukum
- Input: params dikirim sebagai **argumen `--params <json>`** (bukan string interpolation → anti-injeksi); script membaca JSON dari stdin
- Output: stdout (maks 64 KB, dipotong rune-safe); exit ≠ 0 → `Fail` dengan stderr (maks 4 KB)
- **Lingkungan**: tanpa env sensitif (API key tidak diwariskan ke script); `PATH` terbatas; working dir terisolasi `data/tools/run/`
- **Tidak bisa dipanggil saat `data/tools` disabled** di config (kill-switch)

## 6. Integrasi sistem yang sudah ada

| Sistem | Integrasi |
|---|---|
| **Registry** | Loader dipanggil dari `main.go` setelah semua register bawaan → tools muncul di summary & prompt otomatis |
| **Sub-agent (G7b)** | Tool kustom TIDAK masuk allowlist sub-agent secara default (least privilege); agent bisa menyebutkan nama `x_*` eksplisit saat delegasi |
| **Audit** | Setiap save/del/toggle/eksekusi tercatat `[selftool]` di log + perm audit |
| **Prompt** | Tambah instruksi: "kamu bisa membuat tool baru via self_tool_save untuk kemampuan yang sering kamu butuhkan" |
| **Persist config (B4)** | Tidak tersentuh — tools kustom persist sendiri via manifest.json |
| **aleph-guard** | Tidak terkait (tidak menyentuh binary; rollback binary tidak menghapus tools kustom) |

## 7. Yang TIDAK bisa dilakukan tool kustom (batas keras)

- Tidak bisa menimpa/menghapus tool bawaan
- Tidak bisa mengubah config, perm.json, skills, atau file sistem
- Tidak bisa menonaktifkan permission engine, guard, atau watcher
- Tidak bisa memanggil `self_tool_save` via sub-agent (anti-rekursi ekstensi)

## 8. Estimasi & urutan kerja

| Tahap | Isi | Perkiraan |
|---|---|---|
| 1 | `internal/tools/selftool.go` (save/list/del/toggle + validasi + runner) | inti, ~400 lines |
| 2 | Loader + wiring main.go + prompt update | kecil |
| 3 | Test: validasi nama/ukuran/blocklist charset, syntax-check gagal, timeout eksekusi, anti-injeksi params, quota | 1 suite |
| 4 | Hardening audit + deploy v23 + uji end-to-end dari Telegram ("buatkan tool X lalu pakai") | 1 gelombang |

**Contoh uji penerimaan**: user bilang *"agent, buatkan tool yang mengecek suhu CPU tiap dipanggil"* → agent `self_tool_save` (script python baca `/sys/class/thermal`) → langsung dipanggil → hasil muncul → tetap ada setelah restart.

---

Catatan desain: nama disarankan `x_` + manifest terpisah agar mudah diaudit
sekaligus memudahkan B3 (warn kustom) nanti — format manifest mirip, tinggal
tambah evaluator. Saya sarankan B3 dan G10 dibangun dengan pola disk-manifest
yang sama supaya konsisten.

**Menunggu perintah Anda untuk mulai.**


---

# 📋 PLAN G11 — Hot Reload Menyeluruh
**Status: PLAN — menunggu persetujuan. Semua komponen bot reload tanpa restart.**

## Latar (audit v22)
Sudah hot: config AI runtime (context/rounds/timeout/model/memory/sub-agent, persist B4),
skills, lessons, services custom, notes/memory/perm decisions.
BELUM hot: prompt agent (statis sejak start), tool registry baru, cron (scheduler
punya Add/Remove tapi tak ada tool utk agent), config.yaml (base_url/API key/admin),
telegram token (restart saja — wajar).

## Bagan kerja

### H-1. Dynamic Prompt Rebuild (KRITIS — prasyarat G10)
- Sekarang: prompt disusun sekali di start (`BuildPromptExtra`), tools summary
  dibekukan. Tool baru via G10 tidak akan "diketahui" agent.
- Fix: `Agent.RebuildPrompt()` — susun ulang system prompt dari `reg.Summary()`
  + extra (memory/skills) saat daftar tool berubah (panggilan setelah
  self_tool_save/del/toggle). Mutex cfgMu, snapshot per HandleChat (race-safe,
  pola sama dgn H-A).
- Prompt cache per chat di-reset saat rebuild agar ronde berikutnya memakai
  prompt baru.

### H-2. Cron Tools (agent bisa jadwalkan sendiri)
- Tool `cron_add(name, schedule, prompt)`, `cron_list`, `cron_del` — dipanggil
  `Scheduler.Add/Remove` yang SUDAH ADA (runtime, tidak butuh restart).
- Validasi: schedule format cron standar, prompt maks 4k char, maks 20 job,
  job persist ke cron.json (sudah ada LoadJobs di start).
- Self-verify + alert dari F7 tetap jalan.

### H-3. Watcher Config Endpoint (base_url/API key)
- Command `/reloadapi` (admin-only) atau tombol menu: re-load llm.BaseURL/APIKey
  dari config.yaml TANPA restart — klien LLM dibuat ulang, model aktif dipertahankan.
- Guard: kalau endpoint baru gagal health-check (GET /models), keep yang lama +
  laporkan gagal. Tidak pernah mati karena config salah.
- (fsnotify auto-watch sengaja TIDAK dipakai — N2600 dihemat, reload manual saja.)

### H-4. Admin IDs & Telegram minor
- Admin IDs dibaca ulang via /reloadapi (menambah admin baru tanpa restart).
- Telegram token TETAP butuh restart (koneksi websocket bot) — diberi catatan
  jelas, bukan dijanjikan hot.

## Hardening
- RebuildPrompt: di bawah cfgMu, tidak boleh mengubah prompt TENGAH percakapan
  (apply di HandleChat berikutnya — atomic swap pointer).
- cron_add: prompt cron dieksekusi dengan budget rounds sendiri (sub-isolasi),
  anti-rekursi (job tidak boleh memanggil cron_add via prompt? boleh — tapi
  lewat agent utama & ter-audit).
- /reloadapi: hanya admin, audit, dan selalu fallback ke config lama saat gagal.

## Urutan
G11 dikerjakan BERSAMA G10 (H-1 prasyarat langsung G10): G10 (tool kustom) +
H-1 (prompt rebuild) + H-2 (cron tools) = deploy v23. H-3/H-4 = v23 sekalian
(kerja kecil). Total masih ~1,5 gelombang.

## Uji penerimaan
1. "Agent, buat tool X" → tool jalan LANGSUNG + agent tahu tool barunya tanpa restart ✅
2. "Agent, jadwalkan tiap jam cek disk" → cron_add → jalan tiap jam, persist restart ✅
3. Ganti base_url/API key di config.yaml → /reloadapi → langsung pakai endpoint baru ✅
4. Semua di atas dilakukan TANPA `systemctl restart aleph-agent` ✅
