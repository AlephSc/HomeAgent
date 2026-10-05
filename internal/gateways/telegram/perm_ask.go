// Package telegram — F-perm: prompt inline 4-pilihan utk command baru.
// once | always (save) | session | deny. Disimpan di pendingAct{Kind:"perm"}.
package telegram

import (
	"fmt"
	"log"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// permKeyboard — 4 tombol keputusan. data: "perm:<id>:<choice>".
func permKeyboard(reqID int) tgbotapi.InlineKeyboardMarkup {
	return tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("✅ Sekali ini", fmt.Sprintf("perm:%d:once", reqID)),
			tgbotapi.NewInlineKeyboardButtonData("💾 Selalu", fmt.Sprintf("perm:%d:always", reqID)),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("⏳ Sesi ini saja", fmt.Sprintf("perm:%d:session", reqID)),
			tgbotapi.NewInlineKeyboardButtonData("⛔ Tolak", fmt.Sprintf("perm:%d:deny", reqID)),
		),
	)
}

// pendingAsk — permintaan izin yang menunggu keputusan user.
type pendingAsk struct {
	Command string
	ReqID   int
	MsgID   int
	Answer  chan string // dikirim 1x saat user memilih
}

// askPermission — implementasi perm.AskFunc: kirim prompt inline, tunggu keputusan.
// Dipanggil dari goroutine agent (blocking hingga user memilih / timeout).
func (g *Gateway) askPermission(command string) string {
	reqID := g.nextAskID()
	ch := make(chan string, 1)
	g.pendingMu.Lock()
	g.pendingPerm[reqID] = &pendingAsk{Command: command, ReqID: reqID, Answer: ch}
	g.pendingMu.Unlock()

	msg := tgbotapi.NewMessage(g.adminChatID(), fmt.Sprintf(
		"🔐 *Izin command baru*\n\n`%s`\n\nProgram: `%s`\n\nPilih izin:",
		command, headOf(command)))
	msg.ParseMode = "Markdown"
	msg.ReplyMarkup = permKeyboard(reqID) // ← 4 tombol: sekali/selalu/sesi/tolak
	sent, err := g.api.Send(msg)
	g.pendingMu.Lock()
	if p, ok := g.pendingPerm[reqID]; ok {
		p.MsgID = sent.MessageID
	}
	g.pendingMu.Unlock()
	if err != nil {
		// tak bisa kirim prompt → izinkan + audit (kebijakan longgar)
		return "once"
	}

	log.Printf("[perm] prompt izin #%d terkirim utk `%s` — menunggu keputusan...", reqID, command)
	select {
	case choice := <-ch:
		log.Printf("[perm] keputusan #%d diterima: %s", reqID, choice)
		return choice
	case <-time.After(300 * time.Second):
		// timeout → hapus pending, anggap sekali-izinkan (longgar)
		g.pendingMu.Lock()
		delete(g.pendingPerm, reqID)
		g.pendingMu.Unlock()
		g.editAndCleanup(reqID, "⏱ Timeout — diizinkan sekali ini saja.")
		return "once"
	}
}

// handlePermCallback — routing data "perm:<id>:<choice>" dari handleCallback.
func (g *Gateway) handlePermCallback(cbID string, chatID int64, msgID int, reqID int, choice string) {
	g.pendingMu.Lock()
	p, ok := g.pendingPerm[reqID]
	if ok {
		delete(g.pendingPerm, reqID)
	}
	g.pendingMu.Unlock()
	if !ok {
		// prompt sudah kedaluwarsa/di-restart — jangan diam, beri tahu user jelas
		exp := tgbotapi.NewEditMessageText(chatID, msgID,
			"⚠️ *Permintaan izin ini sudah kedaluwarsa.\n\nMinta bot menjalankan ulang command-nya* — prompt izin baru akan muncul dengan tombol aktif.")
		exp.ParseMode = "Markdown"
		g.api.Send(exp)
		g.api.Request(tgbotapi.NewCallback(cbID, "⚠️ kedaluwarsa — minta jalankan ulang"))
		log.Printf("[perm] klik pada prompt #%d sudah tidak berlaku (restart/timeout)", reqID)
		return
	}
	p.Answer <- choice
	label := map[string]string{
		"once": "✅ Diizinkan sekali ini", "always": "💾 Diizinkan SELALU",
		"session": "⏳ Diizinkan sesi ini", "deny": "⛔ Ditolak",
	}[choice]
	edit := tgbotapi.NewEditMessageText(chatID, msgID,
		fmt.Sprintf("%s\n\n`%s`", label, p.Command))
	edit.ParseMode = "Markdown"
	if _, err := g.api.Send(edit); err != nil {
		e2 := tgbotapi.NewEditMessageText(chatID, msgID, label)
		g.api.Send(e2)
	}
	g.api.Request(tgbotapi.NewCallback("", label))
}

// editAndCleanup — tutup prompt yang timeout.
func (g *Gateway) editAndCleanup(reqID int, text string) {
	g.pendingMu.Lock()
	p, ok := g.pendingPerm[reqID]
	delete(g.pendingPerm, reqID)
	g.pendingMu.Unlock()
	if !ok {
		return
	}
	e := tgbotapi.NewEditMessageText(g.adminChatID(), p.MsgID, text)
	g.api.Send(e)
}

// nextAskID — ID unik antar-goroutine.
func (g *Gateway) nextAskID() int {
	g.pendingMu.Lock()
	defer g.pendingMu.Unlock()
	g.askSeq++
	return g.askSeq
}

// adminChatID — chat admin pertama (tujuan prompt izin).
func (g *Gateway) adminChatID() int64 {
	for _, id := range g.cfg.Admins.TelegramIDs {
		return id
	}
	return 0
}

// headOf — head command pertama (untuk tampilan).
func headOf(command string) string {
	f := strings.Fields(command)
	if len(f) == 0 {
		return "?"
	}
	if i := strings.LastIndexByte(f[0], '/'); i >= 0 {
		return f[0][i+1:]
	}
	return f[0]
}

var _ = log.Printf
