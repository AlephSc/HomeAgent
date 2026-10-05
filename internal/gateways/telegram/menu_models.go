package telegram

// menu_models.go — G8: pemilih model paged (8/halaman) + search.
//
// Callback routing:
//   cfg:model            → halaman 0 (fetch /models bila perlu)
//   cfg:modelpage:<n>    → navigasi halaman
//   cfg:modsearch        → minta user ketik query (pendingAct kind=model-search)
//   cfg:modsearch:<q>    → hasil pencarian (halaman 0)
//   cfg:modsearchpage:<q>|<n> → navigasi hasil pencarian
//   cfg:setmodel:<id>    → set model aktif
//
// Hardening: query disanitasi (maks 32 char, charset aman utk callback data —
// callback data Telegram maks 64 bytes), data callback dipangkas aman,
// fetch /models dengan timeout & cache 6 jam (llm.ListModels).

import (
	"context"
	"fmt"
	"sort"
	"strings"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

const modelsPerPage = 8

// sanitizeQuery — aman untuk callback data & bebas injeksi.
func sanitizeQuery(q string) string {
	q = strings.TrimSpace(q)
	if len(q) > 32 {
		q = q[:32]
	}
	var b strings.Builder
	for _, r := range q {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '-', r == '_', r == '.', r == '/', r == ' ':
			b.WriteRune(r)
		}
	}
	return b.String()
}

// truncateCBData pastikan callback data ≤ 64 bytes (batas Telegram).
func truncateCBData(s string) string {
	if len(s) <= 64 {
		return s
	}
	return s[:64]
}

// modelPageKeyboard susun keyboard satu halaman daftar model.
// ids SUDAH terfilter (hasil search atau semua); q kosong = tanpa search.
func (g *Gateway) modelPageKeyboard(ids []string, page int, q string) (string, tgbotapi.InlineKeyboardMarkup) {
	total := len(ids)
	pages := (total + modelsPerPage - 1) / modelsPerPage
	if pages < 1 {
		pages = 1
	}
	if page < 0 {
		page = 0
	}
	if page >= pages {
		page = pages - 1
	}
	lo := page * modelsPerPage
	hi := lo + modelsPerPage
	if hi > total {
		hi = total
	}

	active := g.activeModel()
	var grid [][]tgbotapi.InlineKeyboardButton
	if len(ids) == 0 {
		grid = append(grid, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("∅ tidak ada model", "cfg:model"),
		))
	}
	for _, m := range ids[lo:hi] {
		mark := ""
		if m == active {
			mark = " ✅"
		}
		// label dipotong 48 char agar tombol tidak kelewat panjang
		lbl := m
		if len(lbl) > 48 {
			lbl = lbl[:48]
		}
		grid = append(grid, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(lbl+mark, truncateCBData("cfg:setmodel:"+m)),
		))
	}

	// baris navigasi: ◂ prev | halaman | next ▸
	nav := []tgbotapi.InlineKeyboardButton{}
	if page > 0 {
		if q != "" {
			nav = append(nav, tgbotapi.NewInlineKeyboardButtonData("◂", truncateCBData(fmt.Sprintf("cfg:modsearchpage:%s|%d", q, page-1))))
		} else {
			nav = append(nav, tgbotapi.NewInlineKeyboardButtonData("◂", fmt.Sprintf("cfg:modelpage:%d", page-1)))
		}
	}
	nav = append(nav, tgbotapi.NewInlineKeyboardButtonData(fmt.Sprintf("%d/%d", page+1, pages), "cfg:modelnoop"))
	if page < pages-1 {
		if q != "" {
			nav = append(nav, tgbotapi.NewInlineKeyboardButtonData("▸", truncateCBData(fmt.Sprintf("cfg:modsearchpage:%s|%d", q, page+1))))
		} else {
			nav = append(nav, tgbotapi.NewInlineKeyboardButtonData("▸", fmt.Sprintf("cfg:modelpage:%d", page+1)))
		}
	}
	grid = append(grid, nav)

	// baris aksi: 🔍 Search | 🔄 Refresh
	refresh := "cfg:modelrefresh"
	if q != "" {
		refresh = truncateCBData("cfg:modsearchpage:" + q + "|0r")
	}
	grid = append(grid, tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData("🔍 Cari", "cfg:modsearch"),
		tgbotapi.NewInlineKeyboardButtonData("🔄 Refresh", refresh),
	))
	// back
	back := tgbotapi.NewInlineKeyboardButtonData("◂ Config AI", "cfg:home")
	if q != "" {
		back = tgbotapi.NewInlineKeyboardButtonData("◂ Semua Model", "cfg:model")
	}
	grid = append(grid, tgbotapi.NewInlineKeyboardRow(back))

	title := fmt.Sprintf("🤖 *Model* (%d total) — hal %d/%d", total, page+1, pages)
	if q != "" {
		title = fmt.Sprintf("🔍 Hasil \"%s\" (%d) — hal %d/%d", q, total, page+1, pages)
	}
	return title, tgbotapi.NewInlineKeyboardMarkup(grid...)
}

// activeModel — model aktif (field g.model, mutex).
func (g *Gateway) activeModel() string {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.model != "" {
		return g.model
	}
	return g.cfg.LLM.Model
}

// loadModelIDs — daftar semua model: config statis kalau ada, selain itu
// fetch /models via llm.ListModels (cache 6 jam, fallback cache basi).
func (g *Gateway) loadModelIDs() ([]string, error) {
	if len(g.cfg.LLM.Models) > 0 {
		return g.cfg.LLM.Models, nil
	}
	return g.fetchModelIDs()
}

// fetchModelIDs — paksa fetch via llm.ListModels (cache 6 jam).
func (g *Gateway) fetchModelIDs() ([]string, error) {
	if g.agent == nil || g.agent.LLM == nil {
		if len(g.cfg.LLM.Models) > 0 {
			return g.cfg.LLM.Models, nil
		}
		return nil, fmt.Errorf("LLM client belum siap")
	}
	ids, _, err := g.agent.LLM.ListModels(context.Background(), g.cfg.Server.DataDir)
	return ids, err
}

// filterModels — saring ids by query (case-insensitive substring).
func filterModels(ids []string, q string) []string {
	if q == "" {
		return ids
	}
	ql := strings.ToLower(q)
	out := []string{}
	for _, m := range ids {
		if strings.Contains(strings.ToLower(m), ql) {
			out = append(out, m)
		}
	}
	sort.Strings(out)
	return out
}
