# Arsitektur aleph-agent

## Gambaran
Satu proses Go (`aleph-agent`) menjalankan gateway Telegram, agent loop, registry tools,
scheduler, alert engine, dan Web UI. LLM eksternal via 9Router (OpenAI-compatible `/v1`).

```
Telegram ⇄ Gateway ─┬─ Fastpath (slash command, 0 token)
                    ├─ Agent loop (ReAct) ── 9Router /v1
                    │     ├─ Tools registry (34) ─┬─ bawaan (exec, file, notes, ...)
                    │     │                        ├─ self-tool (data/tools/)  ← agent buat sendiri
                    │     │                        └─ delegate_task → sub-agent (tool subset)
                    │     ├─ Session/Summary/Memory (SQLite)
                    │     └─ Prompt (dynamic rebuild, race-safe)
                    ├─ Cron scheduler (persist + self-verify)
                    ├─ Alert engine (non-AI, 0 token)
                    └─ Web UI (bind Tailscale-only)
aleph-guard (systemd timer, di luar proses bot)
aleph-memctl.sh (helper root-only, sudo whitelist)
```

## Komponen internal/

| Paket | Isi |
|---|---|
| `agent` | Loop ReAct, compacting konteks, subagent (Delegate + semaphore runtime-resize), runtime_config (SetMaxMemoryMB/SetMaxSubAgents/SetContextBudget/SetTimeout/SetMaxRounds), persist_config (overlay atomik), rebuild_prompt (PromptSnapshot/RebuildPrompt/SetToolsRef/SwapLLMClient) |
| `tools` | Registry + 34 tool; interface `Delegator` (anti import-cycle); `Disable/Enable`, `SetCurrentRegistry`; selftool (G10); cron_tools (H-2) |
| `llm` | Client OpenAI-compatible; mutex Model (H-B); `ListModels` (cache disk 6 jam, fallback offline) |
| `telegram` | Gateway; fastpath; menu*.go (utama, services, AP, notes, config, models, usage); pending input; perm callback; exec_ctx (CommandContext helper); reloadapi (H-3) |
| `webui` | HTTP server bind Tailscale-only; token + HttpOnly cookie; MaxBytesReader |
| `memory` | SQLite: sessions, running summary, memory, notes + note_links (wiki-link), uploads, lessons, cron_job |
| `perm` | Permission engine: tier read-only/gated, prompt izin (once/always/session/deny), blocklist, audit trail |
| `cron` | Scheduler `Add(*Job)/Remove/List/SaveJobs`; format `every 5m\|30m\|1h\|2h\|1d` / `daily HH:MM`; persist + verify-after-action |
| `extract` | Ekstraksi dokumen per-halaman, throttle, checkpoint resume, cache .txt permanen |
| `config` | Load YAML (load sekali; bagian yang bisa hot di-overlay runtime) |

## Alur chat
1. Update masuk → fastpath? (slash command → jawab instan, 0 token)
2. Admin check → agent loop: system prompt (PromptSnapshot — selalu terbaru) + riwayat + running summary
3. LLM balas / panggil tool → permission engine → exec (CommandContext, timeout) → hasil ke LLM
4. Budget tool-call habis → compacting → lanjut
5. Jawab user; session + summary disimpan (SQLite)

## Data dir (`/opt/aleph-agent/data`)
```
aleph.db              — SQLite (sessions, notes, memory, lessons, uploads, cron)
agent-config.json     — overlay config persist (0600, atomik)   [B4]
model-cache.json      — cache daftar model 6 jam                [G8]
tools/                — manifest tool kustom (x_*)              [G10]
skills/ chat/ files/  — skill, chat log, upload/inbox/outbox
```

## Pola keamanan
- Sub-agent: `CloneAllowed` — tanpa delegate_task/exec_command/write_file/service_control (anti-rekursi, least-privilege)
- Prompt rebuild & model swap: mutex + atomic swap (pola H-A/H-B, race-safe)
- Semua exec eksternal: `CommandContext` + timeout wajib + output rune-safe
- Body HTTP Web UI: `http.MaxBytesReader`
