// Package telegram — G3: ask_user, pertanyaan pilihan ganda inline keyboard.
// Model memanggil tool ask_user → gateway kirim pesan + tombol → jawaban user
// dikembalikan ke model sebagai hasil tool. Callback data: "ask:<seq>:<idx>|free".
package telegram

import (
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// pendingAskUser — pertanyaan agent yang menunggu jawaban user.
type pendingAskUser struct {
	Question string
	Options  []string
	MsgID    int
	Answer   chan string
}

// askUser — implementasi agent.AskUserFn. Blocking di goroutine agent.
func (g *Gateway) askUser(question string, options []string, allowFree bool) (string, error) {
	g.pendingMu.Lock()
	g.askSeq++
	reqID := g.askSeq
	ch := make(chan string, 1)
	g.pendingAskUser[reqID] = &pendingAskUser{Question: question, Options: options, Answer: ch}
	g.pendingMu.Unlock()

	msg := tgbotapi.NewMessage(g.adminChatID(),
		"❓ *Pertanyaan:*\n"+question+"\n\nPilih salah satu:")
	msg.ParseMode = "Markdown"

	var rows [][]tgbotapi.InlineKeyboardButton
	for i, opt := range options {
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(opt, fmt.Sprintf("ask:%d:%d", reqID, i)),
		))
	}
	if allowFree {
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("✍️ Jawab bebas", fmt.Sprintf("ask:%d:free", reqID)),
		))
	}
	msg.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(rows...)

	sent, err := g.api.Send(msg)
	var pendingByID *pendingAskUser
	g.pendingMu.Lock()
	if p, ok := g.pendingAskUser[reqID]; ok {
		p.MsgID = sent.MessageID
		pendingByID = p
	}
	g.pendingMu.Unlock()
	if err != nil {
		return "", fmt.Errorf("kirim pertanyaan gagal: %w", err)
	}
	log.Printf("[ask] pertanyaan #%d terkirim (%d opsi) — menunggu jawaban...", reqID, len(options))

	select {
	case ans := <-ch:
		// jawaban bebas terkonsumsi → pastikan tunggu free-nya bersih
		g.pendingMu.Lock()
		for cid, fp := range g.freeAskWait {
			if fp == pendingByID {
				delete(g.freeAskWait, cid)
			}
		}
		g.pendingMu.Unlock()
		return ans, nil
	case <-time.After(120 * time.Second):
		g.pendingMu.Lock()
		delete(g.pendingAskUser, reqID)
		// bersihkan juga tunggu jawab-bebas milik pertanyaan ini (anti leak)
		for cid, fp := range g.freeAskWait {
			if fp == pendingByID {
				delete(g.freeAskWait, cid)
			}
		}
		g.pendingMu.Unlock()
		g.editText(g.adminChatID(), sent.MessageID, "⏱ Tidak ada jawaban — agent melanjutkan dengan asumsi.")
		return "", fmt.Errorf("timeout menunggu jawaban user")
	}
}

// handleAskCallback — data "ask:<reqID>:<payload>" (payload = index opsi / "free").
func (g *Gateway) handleAskCallback(cbID string, chatID int64, msgID int, data string) {
	parts := strings.SplitN(strings.TrimPrefix(data, "ask:"), ":", 2)
	if len(parts) != 2 {
		g.api.Request(tgbotapi.NewCallback(cbID, "data tombol tidak valid"))
		return
	}
	reqID, _ := strconv.Atoi(parts[0])
	payload := parts[1]

	g.pendingMu.Lock()
	p, ok := g.pendingAskUser[reqID]
	if ok {
		delete(g.pendingAskUser, reqID)
	}
	g.pendingMu.Unlock()
	if !ok {
		exp := tgbotapi.NewEditMessageText(chatID, msgID,
			"⚠️ *Pertanyaan ini sudah kedaluwarsa.*\nAgent sudah melanjutkan dengan asumsi.")
		exp.ParseMode = "Markdown"
		g.api.Send(exp)
		g.api.Request(tgbotapi.NewCallback(cbID, "⚠️ kedaluwarsa"))
		log.Printf("[ask] klik pada pertanyaan #%d sudah tidak berlaku", reqID)
		return
	}

	if payload == "free" {
		// jawaban bebas: user mengetik → simpan tunggu per chat
		g.pendingMu.Lock()
		g.freeAskWait[chatID] = p
		g.pendingMu.Unlock()
		g.api.Request(tgbotapi.NewCallback(cbID, "✍️ Silakan ketik jawabanmu (1 pesan)"))
		g.editText(chatID, msgID, "✍️ *Silakan ketik jawabanmu* (1 pesan berikutnya).")
		return
	}

	ans := payload
	if idx, err := strconv.Atoi(payload); err == nil && idx >= 0 && idx < len(p.Options) {
		ans = p.Options[idx]
	}
	select {
	case p.Answer <- ans:
	default:
	}
	g.editText(chatID, msgID, "✅ Jawaban terkirim: *"+ans+"*")
	g.api.Request(tgbotapi.NewCallback(cbID, "jawaban dicatat"))
}

// consumeFreeAsk — dipanggil dari handler teks: kalau user sedang menunggu jawab bebas,
// kirim teksnya sebagai jawaban. Return true kalau teks dikonsumsi.
func (g *Gateway) consumeFreeAsk(chatID int64, text string) bool {
	g.pendingMu.Lock()
	p, ok := g.freeAskWait[chatID]
	if ok {
		delete(g.freeAskWait, chatID)
	}
	g.pendingMu.Unlock()
	if !ok {
		return false
	}
	select {
	case p.Answer <- text:
	default:
	}
	return true
}

// editText — helper edit pesan aman (abaikan error).
func (g *Gateway) editText(chatID int64, msgID int, text string) {
	e := tgbotapi.NewEditMessageText(chatID, msgID, text)
	e.ParseMode = "Markdown"
	if _, err := g.api.Send(e); err != nil {
		e2 := tgbotapi.NewEditMessageText(chatID, msgID, text)
		g.api.Send(e2)
	}
}
