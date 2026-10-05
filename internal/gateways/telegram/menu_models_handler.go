package telegram

// handler G8 di file menu_models_handler.go — logika handleCfgModels.

import (
	"fmt"
	"os"
	"strings"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// editFn — closure edit pesan menu (dibuat di handleCfgCallback).
type editFn func(text string, kb tgbotapi.InlineKeyboardMarkup)

// handleCfgModels — dispatcher semua callback model (G8).
func (g *Gateway) handleCfgModels(answer func(string), chatID int64, msgID int, op, arg string, editMenu editFn) {
	switch op {
	case "modelnoop":
		answer("—")
		return

	case "model":
		answer("daftar model")
		ids, err := g.loadModelIDs()
		if err != nil {
			answer("gagal")
			editMenu("⚠️ Gagal mengambil daftar model: `"+err.Error()+"`", tgbotapi.NewInlineKeyboardMarkup(
				tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonData("🔄 Coba lagi", "cfg:model")),
				tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonData("◂ Config AI", "cfg:home")),
			))
			return
		}
		title, kb := g.modelPageKeyboard(ids, 0, "")
		editMenu(title, kb)

	case "modelpage":
		answer("halaman")
		ids, err := g.loadModelIDs()
		if err != nil {
			answer("gagal")
			editMenu("⚠️ Gagal mengambil daftar model.", tgbotapi.NewInlineKeyboardMarkup(
				tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonData("◂ Config AI", "cfg:home")),
			))
			return
		}
		var page int
		fmt.Sscanf(arg, "%d", &page)
		title, kb := g.modelPageKeyboard(ids, page, "")
		editMenu(title, kb)

	case "modelrefresh":
		// paksa fetch ulang (cache di-skip): hapus cache file lalu fetch
		answer("refresh…")
		g.invalidateModelCache()
		ids, err := g.fetchModelIDs()
		if err != nil {
			answer("gagal")
			editMenu("⚠️ Refresh gagal: `"+err.Error()+"`", tgbotapi.NewInlineKeyboardMarkup(
				tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonData("🔄 Coba lagi", "cfg:modelrefresh")),
				tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonData("◂ Config AI", "cfg:home")),
			))
			return
		}
		title, kb := g.modelPageKeyboard(ids, 0, "")
		editMenu(title, kb)

	case "modsearch":
		// minta user ketik query — pakai pendingAct (pola sama dgn run/svc-add)
		answer("ketik query")
		g.pendingMu.Lock()
		g.pendingAct[chatID] = &pendingAct{Kind: "model-search"}
		g.pendingMu.Unlock()
		e := tgbotapi.NewMessage(chatID,
			"🔍 *Cari model*\n\nKetik kata kunci (mis. `claude`, `gemini`, `flash`), lalu kirim.\n/batal untuk membatalkan.")
		e.ParseMode = "Markdown"
		g.api.Send(e)

	case "modsearchpage":
		// arg = "<query>|<page>" (opsional sufiks "r" = refresh dulu)
		parts := strings.SplitN(arg, "|", 2)
		if len(parts) != 2 {
			answer("data salah")
			return
		}
		q := sanitizeQuery(parts[0])
		page := 0
		refresh := strings.HasSuffix(parts[1], "r")
		p := strings.TrimSuffix(parts[1], "r")
		fmt.Sscanf(p, "%d", &page)
		var ids []string
		var err error
		if refresh {
			g.invalidateModelCache()
			ids, err = g.fetchModelIDs()
		} else {
			ids, err = g.loadModelIDs()
		}
		if err != nil {
			answer("gagal")
			editMenu("⚠️ Gagal mengambil daftar model.", tgbotapi.NewInlineKeyboardMarkup(
				tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonData("◂ Config AI", "cfg:home")),
			))
			return
		}
		answer("hasil cari")
		title, kb := g.modelPageKeyboard(filterModels(ids, q), page, q)
		editMenu(title, kb)

	case "setmodel":
		// validasi: model harus ada di daftar (config ATAU /models)
		m := sanitizeQuery(arg) // nama model aman-charset juga
		ids, _ := g.loadModelIDs()
		found := false
		for _, x := range ids {
			if x == m {
				found = true
				break
			}
		}
		if !found {
			answer("model tidak dikenal")
			return
		}
		g.mu.Lock()
		g.model = m
		g.mu.Unlock()
		if g.agent != nil && g.agent.LLM != nil {
			g.agent.LLM.SetModel(m)
		}
		g.cfgSave()
		answer("model = " + m)
		editMenu(g.cfgHeaderText(), g.cfgMenuKeyboard())
	}
}

// invalidateModelCache — hapus cache file (refresh paksa).
func (g *Gateway) invalidateModelCache() {
	if g.cfg == nil || g.cfg.Server.DataDir == "" {
		return
	}
	_ = os.Remove(g.cfg.Server.DataDir + "/model-cache.json")
}
