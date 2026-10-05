# Tools (34 bawaan)

## Sistem
| Tool | Fungsi |
|---|---|
| `exec_command` | Jalankan shell (permission gated, timeout, output rune-safe) |
| `service_control` | start/stop/restart/status unit systemd (whitelist + gated) |
| `server_status` | RAM, disk, load, uptime, suhu |
| `network_tool` | ping, speedtest (CommandContext), port check |

## File & Dokumen
| Tool | Fungsi |
|---|---|
| `list_uploads` / `read_upload` / `upload_info` | Akses file upload (cache ekstraksi .txt permanen) |
| `write_file` | Tulis file (sandbox/gated) |
| `make_document` | Buat .md/.txt ke outbox (bisa dikirim via Telegram) |
| `make_ppt` | Buat **.pptx asli** — OOXML pure stdlib |

## Web
| Tool | Fungsi |
|---|---|
| `web_search` | Pencarian web |
| `web_fetch` | Ambil halaman (markdown, head+tail untuk halaman besar) |

## Notes & Memory
| Tool | Fungsi |
|---|---|
| `save_note` / `search_notes` | Catatan markdown + wiki-link `[[Judul]]` + backlink |
| `memory_save` / `memory_recall` | Fakta/preferensi jangka panjang |
| `save_skill` / `recall_skill` / `skill_patch` | Prosedur yang dipakai ulang |
| `add_lesson` | Pembelajaran dari koreksi user |

## Agent & Meta
| Tool | Fungsi |
|---|---|
| `delegate_task` | Sub-agent: konteks bersih, budget sendiri, deadline wajib, semaphore (B2), tolak saat mem ≥85% (B1) |
| `self_tool_save` / `self_tool_list` / `self_tool_toggle` / `self_tool_del` | **Self-extension G10** — agent membuat tool sendiri |
| `cron_add` / `cron_list` / `cron_del` | Jadwal tugas berulang (persist, self-verify) |
| `ask_user` | Tanya user dengan tombol pilihan |

## Format tool kustom (x_)
```bash
#!/bin/bash
# stdin: JSON params, contoh: {"target":"8.8.8.8"}
read -r INPUT
TARGET=$(echo "$INPUT" | python3 -c 'import sys,json;print(json.load(sys.stdin).get("target",""))')
ping -c 3 "$TARGET"
```
Simpan via `self_tool_save` (nama `x_ping`, desc, timeout). Manifest persist di `data/tools/`;
langsung dikenali agent (prompt rebuild otomatis). Tidak masuk allowlist sub-agent.
