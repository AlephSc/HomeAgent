# Keamanan

## Lapisan
1. **Admin-only**: hanya `admins.telegram_ids` di config yang bisa memerintah bot (semua gateway).
2. **Permission engine** (setiap panggilan tool):
   - Tier read-only (status, baca file dalam sandbox, notes) → bebas
   - Tier gated (exec_command, write di luar sandbox, service_control) → prompt izin 4 pilihan: Once / Always (whitelist) / Session / Deny
   - Blocklist permanen: perintah bunuh diri, `reboot`/`shutdown`, sentuh aleph-guard, rm -rf luas, dst.
   - Audit trail: `/audit` menampilkan 15 aksi terakhir.
3. **Sub-agent least-privilege**: CloneAllowed membuang delegate_task, exec_command, write_file, service_control — sub-agent tidak bisa memanggil sub-agent lain atau mengeksekusi bebas.
4. **Self-tool (G10)**: syntax check (`bash -n` / `py_compile`), blocklist charset (`rm -rf`, fork bomb, `curl|sh`), nama `x_` regex, quota 20, maks 32 KB, params via **stdin JSON** (anti-injeksi), env minimal **tanpa API key**, timeout wajib 30–120 s, lewat PermEngine().Audit. Tolak menimpa tool bawaan / menyentuh config/perm/skills/guard.
5. **aleph-guard** (di luar proses bot, systemd timer 2 menit): crash-loop 3× dalam 10 menit → rollback otomatis ke `aleph-agent.prev`. Bot **dilarang** membaca/mengubah guard ini (blocklist perm).
6. **aleph-memctl.sh**: satu-satunya jalur bot mengubah systemd MemoryMax — script root dengan validasi ketat (64–65536 MB atau `infinity`), dipanggil via `sudo -n` yang di-whitelist HANYA untuk script ini.
7. **Web UI**: bind khusus interface Tailscale (tidak pernah ke LAN/WAN), token acak via `/cfgweb`, cookie HttpOnly, `MaxBytesReader` anti body-bomb.
8. **Secret**: config.yaml chmod 600, gitignored; API key tidak pernah masuk log (disensor `***`), tidak pernah masuk env tool kustom, tidak pernah ditulis ulang di chat.

## Model threat
- Pengguna non-admin: tidak bisa apa pun (semua command/interaksi ditolak).
- LLM "nakal": dibatasi permission engine + blocklist + budget tool-call + sub-agent least-privilege.
- Binary rusak/deploy gagal: guard rollback otomatis.
- Bot dikompromi total: blocklist perm + guard di luar proses + memctl single-function tetap membatasi blast radius; host tetap aman karena bot bukan root-sentuh unit systemd selain via helper tervalidasi.

## Checklist hardening historis (H1–H41 ringkas)
Context budget snapshot anti-race; mutex Model; CommandContext di semua exec menu/watcher/speedtest; MaxBytesReader; rune-safe truncation (UTF-8); tx error + rollback; timeout semua exec; callback data ≤64 byte; sanitize query; validasi input angka; audit semua tool kustom.
