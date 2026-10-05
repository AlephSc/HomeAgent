// Package telegram — Fase E: konsumsi pending act (run command / konfirmasi service).
package telegram

import (
	"context"
	"fmt"
	"log"
	"os/exec"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"aleph-agent/internal/perm"
	"aleph-agent/internal/tools"
)

// consumePendingAct memproses teks user untuk aksi tombol yang menunggu.
func (g *Gateway) consumePendingAct(chatID int64, act *pendingAct, text string) {
	switch act.Kind {
	case "run":
		g.pendingMu.Lock()
		delete(g.pendingAct, chatID)
		g.pendingMu.Unlock()
		go g.execRunCommand(chatID, act.MsgID, text)
	case "svc-add":
		// G5: tambah service custom
		g.pendingMu.Lock()
		delete(g.pendingAct, chatID)
		g.pendingMu.Unlock()
		unit := strings.TrimSpace(text)
		if unit == "" || strings.HasPrefix(unit, "/") {
			g.reply(chatID, "↩️ Ditambahkan dibatalkan.")
			return
		}
		if svcMgr == nil {
			g.reply(chatID, "⚠️ Service manager belum aktif.")
			return
		}
		if err := svcMgr.Add(unit); err != nil {
			g.reply(chatID, "❌ Gagal: "+err.Error())
			return
		}
		if permEng := tools.PermEngine(); permEng != nil {
			permEng.Audit("service", "add-custom "+unit)
		}
		g.reply(chatID, "✅ "+unit+" ditambahkan ke daftar service (pantau: ON). Buka 🔧 Services untuk mengelolanya.")
	case "cfg-num":
		// G13: input angka custom config (context/rounds/to/mem/sub)
		g.pendingMu.Lock()
		delete(g.pendingAct, chatID)
		g.pendingMu.Unlock()
		var v int
		if _, err := fmt.Sscanf(strings.TrimSpace(text), "%d", &v); err != nil {
			g.reply(chatID, "⚠️ Angka tidak valid — coba lagi dari menu ⚙️ Config AI.")
			return
		}
		var msg string
		switch act.Action {
		case "customctx":
			if err := g.agent.SetContextBudget(v); err != nil {
				g.reply(chatID, "❌ "+err.Error())
				return
			}
			msg = "max context = " + humanTokens(v)
		case "customrounds":
			if err := g.agent.SetMaxRounds(v); err != nil {
				g.reply(chatID, "❌ "+err.Error())
				return
			}
			msg = fmt.Sprintf("max tool calls = %d", v)
		case "customto":
			if err := g.agent.SetTimeout(v); err != nil {
				g.reply(chatID, "❌ "+err.Error())
				return
			}
			msg = fmt.Sprintf("timeout = %ds", v)
		case "custommem":
			if err := g.agent.SetMaxMemoryMB(v); err != nil {
				g.reply(chatID, "❌ "+err.Error())
				return
			}
			msg = "max memory = " + memoryLabel(v)
		case "customsub":
			if err := g.agent.SetMaxSubAgents(v); err != nil {
				g.reply(chatID, "❌ "+err.Error())
				return
			}
			msg = fmt.Sprintf("max sub-agent = %d", v)
		case "customsysd":
			val := strings.TrimSpace(text)
			if val != "infinity" {
				var v2 int
				if _, err := fmt.Sscanf(val, "%d", &v2); err != nil {
					g.reply(chatID, "⚠️ Ketik angka MB (64–65536) atau 'infinity'.")
					return
				}
			}
			g.handleSysdMem(chatID, val)
			return
		default:
			g.reply(chatID, "⚠️ Jenis config tidak dikenal.")
			return
		}
		g.cfgSave() // B4: persist
		g.reply(chatID, "✅ "+msg+" (tersimpan permanen)")

	case "model-search":
		// G8: cari model — query user → tampilkan hasil paged
		g.pendingMu.Lock()
		delete(g.pendingAct, chatID)
		g.pendingMu.Unlock()
		q := sanitizeQuery(strings.TrimSpace(text))
		if q == "" || strings.HasPrefix(text, "/") {
			g.reply(chatID, "↩️ Pencarian dibatalkan.")
			return
		}
		ids, err := g.loadModelIDs()
		if err != nil {
			g.reply(chatID, "⚠️ Gagal mengambil daftar model: "+err.Error())
			return
		}
		res := filterModels(ids, q)
		title, kb := g.modelPageKeyboard(res, 0, q)
		e := tgbotapi.NewMessage(chatID, title)
		e.ParseMode = "Markdown"
		e.ReplyMarkup = &kb
		g.api.Send(e)
	case "svc":
		l := strings.ToLower(strings.TrimSpace(text))
		g.pendingMu.Lock()
		delete(g.pendingAct, chatID)
		g.pendingMu.Unlock()
		if l == "ya" || l == "y" || l == "ok" || l == "lanjut" {
			go func() {
				out := g.runServiceAction(act.Action, act.Unit)
				e := tgbotapi.NewEditMessageTextAndMarkup(chatID, act.MsgID, out, svcActKeyboard(act.Unit))
				g.api.Send(e)
			}()
		} else {
			e := tgbotapi.NewEditMessageTextAndMarkup(chatID, act.MsgID,
				"⏸ Dibatalkan — aksi tidak dijalankan.", svcActKeyboard(act.Unit))
			g.api.Send(e)
		}
	}
}

// execRunCommand — eksekusi command dari tombol Run Command (lewat permission engine).
func (g *Gateway) execRunCommand(chatID int64, msgID int, command string) {
	fields := strings.Fields(command)
	if len(fields) == 0 {
		e := tgbotapi.NewEditMessageText(chatID, msgID, "❌ command kosong.")
		g.api.Send(e)
		return
	}
	base := baseName(fields[0])
	permE := tools.PermEngine()
	if permE == nil {
		e := tgbotapi.NewEditMessageText(chatID, msgID, "❌ permission engine belum siap.")
		g.api.Send(e)
		return
	}
	// F-perm: head-based + blocklist; command baru → prompt 4-pilihan inline
	ctxAsk := perm.WithAsk(context.Background(), g.askPermission)
	tier := permE.ClassifyExec(command, ctxAsk, nil, permE.GatedExec())
	permE.Audit("button-run", command+" ["+tier.String()+"]")
	switch tier {
	case perm.Forbidden:
		e := tgbotapi.NewEditMessageText(chatID, msgID,
			"⛔ Command DESTRUKTIF — dilarang keras (blocklist).")
		g.api.Send(e)
		return
	case perm.Denied:
		e := tgbotapi.NewEditMessageText(chatID, msgID,
			"⛔ Ditolak — Anda memilih tolak untuk `"+base+"`.")
		e.ParseMode = "Markdown"
		g.api.Send(e)
		return
	}
	defer permE.ConsumeOnce(command)

	// edit "berjalan..."
	runE := tgbotapi.NewEditMessageText(chatID, msgID, "💻 Menjalankan: "+command+" ...")
	g.api.Send(runE)

	cmdCtx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	start := time.Now()
	out, err := exec.CommandContext(cmdCtx, fields[0], fields[1:]...).CombinedOutput()
	dur := time.Since(start).Round(time.Millisecond)
	text := fmt.Sprintf("💻 `%s`\n⏱ %s\n\n", command, dur)
	if err != nil {
		text += "⚠️ exit: " + err.Error() + "\n"
	}
	o := string(out)
	if o == "" {
		o = "(output kosong)"
	}
	if len(o) > 3500 {
		o = o[:3500] + "\n…(terpotong)"
	}
	text += "```\n" + o + "\n```"
	e := tgbotapi.NewEditMessageTextAndMarkup(chatID, msgID, text, mainMenuKeyboard())
	e.ParseMode = "Markdown"
	if _, err := g.api.Send(e); err != nil {
		e2 := tgbotapi.NewEditMessageText(chatID, msgID, text)
		g.api.Send(e2)
	}
	log.Printf("[telegram] run-command: %s (%s)", command, tier)
}

// baseName — filepath.Base sederhana (hindari import cycle).
func baseName(p string) string {
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		return p[i+1:]
	}
	if i := strings.LastIndexByte(p, '\\'); i >= 0 {
		return p[i+1:]
	}
	return p
}
