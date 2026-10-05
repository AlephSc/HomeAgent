package telegram

// G2 — Menu Notes "Obsidian": browse kategori → catatan → baca/aksi.
// Routing callback "note:*" + input pending "note-add" (tambah catatan cepat).

import (
	"fmt"
	"strings"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"aleph-agent/internal/memory"
)

const notesPageSize = 6

// noteMenuKeyboard — kategori + tombol aksi.
func (g *Gateway) noteMenuKeyboard() tgbotapi.InlineKeyboardMarkup {
	var grid [][]tgbotapi.InlineKeyboardButton
	cats := g.noteCats()
	// tombol kategori 2 per baris
	for i := 0; i < len(cats); i += 2 {
		row := []tgbotapi.InlineKeyboardButton{}
		for j := i; j < i+2 && j < len(cats); j++ {
			label := fmt.Sprintf("📂 %s (%d)", cats[j].Name, cats[j].Count)
			row = append(row, tgbotapi.NewInlineKeyboardButtonData(label, "note:cat:"+cats[j].Name))
		}
		grid = append(grid, row)
	}
	grid = append(grid,
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("➕ Catatan Baru", "note:add"),
			tgbotapi.NewInlineKeyboardButtonData("🔍 Cari", "note:search"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("◂ Kembali", "menu:home"),
		),
	)
	return tgbotapi.NewInlineKeyboardMarkup(grid...)
}

func (g *Gateway) noteCats() []struct {
	Name  string
	Count int
} {
	if g.store == nil {
		return nil
	}
	return g.store.Categories()
}

// noteCatKeyboard — daftar catatan satu kategori.
func (g *Gateway) noteCatKeyboard(category string, page int) (string, tgbotapi.InlineKeyboardMarkup) {
	notes := g.store.NotesByCategory(category, 100)
	total := len(notes)
	start := page * notesPageSize
	end := start + notesPageSize
	if start > total {
		start = 0
	}
	if end > total {
		end = total
	}
	var grid [][]tgbotapi.InlineKeyboardButton
	for _, n := range notes[start:end] {
		grid = append(grid, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("📄 "+n.Title, "note:view:"+n.Slug),
		))
	}
	// navigasi halaman
	nav := []tgbotapi.InlineKeyboardButton{}
	if page > 0 {
		nav = append(nav, tgbotapi.NewInlineKeyboardButtonData("◀️", fmt.Sprintf("note:catpage:%s:%d", category, page-1)))
	}
	if end < total {
		nav = append(nav, tgbotapi.NewInlineKeyboardButtonData("▶️", fmt.Sprintf("note:catpage:%s:%d", category, page+1)))
	}
	if len(nav) > 0 {
		grid = append(grid, nav)
	}
	grid = append(grid, tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData("◂ Kategori", "note:home"),
	))
	header := fmt.Sprintf("🗂 Kategori *%s* — %d catatan", category, total)
	return header, tgbotapi.NewInlineKeyboardMarkup(grid...)
}

// noteViewKeyboard — aksi pada satu catatan.
func noteViewKeyboard(slug string) tgbotapi.InlineKeyboardMarkup {
	return tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🗑 Hapus", "note:del:"+slug),
			tgbotapi.NewInlineKeyboardButtonData("✏️ Tanya agent soal ini", "note:ask:"+slug),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("◂ Kembali", "note:home"),
		),
	)
}

// handleNoteCallback — dipanggil dari handleCallback untuk data "note:*".
func (g *Gateway) handleNoteCallback(answer func(string), chatID int64, msgID int, data string) {
	if g.store == nil {
		answer("db belum siap")
		g.reply(chatID, "⚠️ Database belum aktif.")
		return
	}
	parts := strings.SplitN(strings.TrimPrefix(data, "note:"), ":", 2)
	op := parts[0]
	arg := ""
	if len(parts) > 1 {
		arg = parts[1]
	}

	switch op {
	case "home":
		answer("notes")
		e := tgbotapi.NewEditMessageTextAndMarkup(chatID, msgID,
			fmt.Sprintf("🗂 *Notes* — %d catatan\nKategori bebas buatan Anda. Gunakan agent: \"catat ke Pembelajaran: ...\" — catatan saling terhubung dengan [[tautan]].",
				g.store.NoteCount()), g.noteMenuKeyboard())
		e.ParseMode = "Markdown"
		g.api.Send(e)

	case "cat", "catpage":
		answer("buka kategori")
		page := 0
		category := arg
		if op == "catpage" {
			pp := strings.SplitN(arg, ":", 2)
			if len(pp) == 2 {
				category = pp[0]
				fmt.Sscanf(pp[1], "%d", &page)
			}
		}
		header, kb := g.noteCatKeyboard(category, page)
		e := tgbotapi.NewEditMessageTextAndMarkup(chatID, msgID, header, kb)
		e.ParseMode = "Markdown"
		g.api.Send(e)

	case "view":
		answer("buka catatan")
		n, err := g.store.GetNoteBySlug(arg)
		if err != nil || n == nil {
			e := tgbotapi.NewEditMessageText(chatID, msgID, "❌ Catatan tidak ditemukan.")
			g.api.Send(e)
			return
		}
		back, fwd := g.store.LinkedNotes(n.Slug)
		text := formatNoteView(n, back, fwd)
		e := tgbotapi.NewEditMessageTextAndMarkup(chatID, msgID, text, noteViewKeyboard(n.Slug))
		g.api.Send(e)

	case "del":
		answer("konfirmasi hapus")
		n, err := g.store.GetNoteBySlug(arg)
		if err != nil || n == nil {
			g.reply(chatID, "❌ Catatan tidak ditemukan.")
			return
		}
		// hapus via pendingAct konfirmasi teks "ya"
		g.pendingMu.Lock()
		g.pendingAct[chatID] = &pendingAct{Kind: "note-del", Unit: n.Slug, MsgID: msgID}
		g.pendingMu.Unlock()
		e := tgbotapi.NewEditMessageText(chatID, msgID,
			fmt.Sprintf("⚠️ Hapus catatan *%s*?\nBalas *ya* untuk menghapus, /batal untuk tolak.", n.Title))
		e.ParseMode = "Markdown"
		g.api.Send(e)

	case "ask":
		answer("agent siap")
		n, err := g.store.GetNoteBySlug(arg)
		if err == nil && n != nil {
			g.reply(chatID, "💬 Ketik pertanyaan Anda. Saya akan mengutip catatan \""+n.Title+"\" sebagai konteks (sebutkan judulnya di pertanyaan).")
		}

	case "add":
		answer("ketik catatan")
		g.pendingMu.Lock()
		g.pendingAct[chatID] = &pendingAct{Kind: "note-add"}
		g.pendingMu.Unlock()
		e := tgbotapi.NewEditMessageText(chatID, msgID,
			"➕ *Catatan Baru*\n\nFormat (2 baris pertama):\n`Judul`\n`Kategori`\nlalu isi catatan di baris berikutnya.\n\nContoh:\n`Tips Docker`\n`Pemrograman`\n`docker compose up -d` untuk jalankan...\n\nKetik /batal untuk membatalkan.")
		e.ParseMode = "Markdown"
		g.api.Send(e)

	case "search":
		answer("ketik kata kunci")
		g.pendingMu.Lock()
		g.pendingAct[chatID] = &pendingAct{Kind: "note-search"}
		g.pendingMu.Unlock()
		e := tgbotapi.NewEditMessageText(chatID, msgID, "🔍 Ketik kata kunci pencarian catatan:")
		g.api.Send(e)
	}
}

// formatNoteView susun teks tampilan catatan.
func formatNoteView(n *memory.Note, back, fwd []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "📄 %s\n📂 %s · diubah %s\n\n", n.Title, n.Category, n.UpdatedAt)
	content := n.Content
	if r := []rune(content); len(r) > 3000 { // jaga pesan Telegram <4096, aman-rune
		content = string(r[:3000]) + "\n\n…(dipotong — tanya agent untuk selengkapnya)"
	}
	b.WriteString(content)
	if len(back) > 0 {
		b.WriteString("\n\n🔗 Dirujuk oleh: " + strings.Join(back, ", "))
	}
	if len(fwd) > 0 {
		b.WriteString("\n\n↗️ Merujuk ke: " + strings.Join(fwd, ", "))
	}
	return b.String()
}

// noteConsumeInput — input teks untuk note-add / note-search / note-del.
// Return true jika teks dikonsumsi.
func (g *Gateway) noteConsumeInput(chatID int64, act *pendingAct, text string) bool {
	if act == nil {
		return false
	}
	switch act.Kind {
	case "note-add":
		g.pendingMu.Lock()
		delete(g.pendingAct, chatID)
		g.pendingMu.Unlock()
		lines := strings.SplitN(text, "\n", 3)
		if len(lines) < 3 || strings.TrimSpace(lines[0]) == "" || strings.TrimSpace(lines[1]) == "" {
			g.reply(chatID, "⚠️ Format salah. Butuh: baris 1 = judul, baris 2 = kategori, baris 3+ = isi.\nKetik ulang atau /batal.")
			return true
		}
		title := strings.TrimSpace(lines[0])
		category := strings.TrimSpace(lines[1])
		content := strings.TrimSpace(lines[2])
		if _, _, err := g.store.UpsertNote(title, category, content); err != nil {
			g.reply(chatID, "❌ Gagal menyimpan: "+err.Error())
			return true
		}
		g.reply(chatID, "✅ Catatan \""+title+"\" tersimpan di kategori "+category+".")
		return true

	case "note-search":
		g.pendingMu.Lock()
		delete(g.pendingAct, chatID)
		g.pendingMu.Unlock()
		notes := g.store.SearchNotes(strings.TrimSpace(text), 10)
		if len(notes) == 0 {
			g.reply(chatID, "🔍 Tidak ada catatan cocok untuk \""+text+"\".")
			return true
		}
		var b strings.Builder
		b.WriteString("🔍 Hasil pencarian:\n")
		for _, n := range notes {
			fmt.Fprintf(&b, "• %s [%s]\n", n.Title, n.Category)
		}
		g.reply(chatID, b.String())
		return true

	case "note-del":
		l := strings.ToLower(strings.TrimSpace(text))
		if l == "ya" || l == "y" || l == "ok" {
			g.pendingMu.Lock()
			delete(g.pendingAct, chatID)
			g.pendingMu.Unlock()
			if err := g.store.DeleteNote(act.Unit); err != nil {
				g.reply(chatID, "❌ Gagal menghapus: "+err.Error())
			} else {
				g.reply(chatID, "🗑 Catatan dihapus.")
			}
			return true
		}
		g.pendingMu.Lock()
		delete(g.pendingAct, chatID)
		g.pendingMu.Unlock()
		g.reply(chatID, "↩️ Penghapusan dibatalkan.")
		return true
	}
	return false
}
