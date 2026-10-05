// Package telegram — gateway Telegram (long-polling) + router FAST/AGENT
package telegram

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"aleph-agent/internal/agent"
	"aleph-agent/internal/config"
	"aleph-agent/internal/fastpath"
	"aleph-agent/internal/llm"
	"aleph-agent/internal/memory"
	"aleph-agent/internal/perm"
	"aleph-agent/internal/tools"
)

// Gateway menerima pesan Telegram dan merutekannya ke fast/agent path.
type Gateway struct {
	api   *tgbotapi.BotAPI
	cfg   *config.Config
	fast  *fastpath.Handler
	agent *agent.Agent
	reg   *tools.Registry // untuk aksi tombol (exec/service lewat permission engine)

	mu       sync.Mutex
	model    string             // model aktif (bisa diganti /model)
	sessions map[int64]*session // context percakapan per chat (ringan)

	// pending confirm (Fase A): chatID → channel jawaban "ya/batal"
	pendingMu sync.Mutex
	pending   map[int64]chan string

	// pendingAct (Fase E): aksi tombol yang menunggu teks user (input command / konfirmasi)
	pendingAct map[int64]*pendingAct

	// pendingPerm (F-perm): prompt izin command baru yang menunggu keputusan user
	pendingPerm map[int]*pendingAsk
	askSeq      int

	// pendingAskUser (G3): pertanyaan ask_user yang menunggu jawaban
	pendingAskUser map[int]*pendingAskUser
	// freeAskWait: user diminta mengetik jawaban bebas (1 chat = 1 tunggu)
	freeAskWait map[int64]*pendingAskUser

	// uploads (G1): antrean ekstraksi worker tunggal + store memory
	extractQueue chan int64
	store        *memory.Store

	// webUI (G3): token untuk command /cfgweb
	webUIToken string

	// G11 H-3: path config.yaml untuk /reloadapi
	cfgPath string
}

// pendingAct — aksi yang menunggu jawaban teks dari user (Fase E).
type pendingAct struct {
	Kind   string // "run" | "svc" | "ap-ssid" | "ap-channel"
	Unit   string
	Action string
	MsgID  int // pesan yang akan diedit dengan hasil
}

// session — konteks chat ringan: pesan terakhir user & bot (untuk keluhan lanjutan).
type session struct {
	LastUserMsg string
	LastTime    time.Time
}

// New membuat gateway (validasi token sekali di sini).
func New(cfg *config.Config, fast *fastpath.Handler, ag *agent.Agent, reg *tools.Registry) (*Gateway, error) {
	api, err := tgbotapi.NewBotAPI(cfg.Token.Telegram)
	if err != nil {
		return nil, fmt.Errorf("auth bot: %w", err)
	}
	api.Debug = false
	log.Printf("[telegram] authorized sebagai @%s", api.Self.UserName)

	// ==== Register command ke Telegram (menu ☰ kiri atas chat) ====
	cmds := []tgbotapi.BotCommand{
		{Command: "menu", Description: "☰ Menu tombol pintar"},
		{Command: "status", Description: "📊 Kondisi server (RAM, disk, load)"},
		{Command: "services", Description: "🔧 Daftar service + status"},
		{Command: "ping", Description: "pong — tes bot hidup"},
		{Command: "audit", Description: "📜 15 aksi terakhir yang ter-audit"},
		{Command: "memory", Description: "🧠 Cari memory bot (/memory <kata kunci>)"},
		{Command: "lesson", Description: "📚 Review lesson (approve/hapus)"},
		{Command: "model", Description: "🤖 Lihat/ganti model AI (/model <nama>)"},
		{Command: "files", Description: "📎 Daftar file yang pernah diupload"},
		{Command: "help", Description: "❓ Bantuan"},
		{Command: "reloadapi", Description: "🔄 Reload endpoint/API dari config"},
	}
	if _, err := api.Request(tgbotapi.NewSetMyCommands(cmds...)); err != nil {
		log.Printf("[telegram] setMyCommands gagal: %v", err)
	} else {
		log.Printf("[telegram] %d command terdaftar ke menu Telegram", len(cmds))
	}

	g := &Gateway{
		api:            api,
		cfg:            cfg,
		fast:           fast,
		agent:          ag,
		reg:            reg,
		model:          cfg.LLM.Model,
		sessions:       map[int64]*session{},
		pending:        map[int64]chan string{},
		pendingAct:     map[int64]*pendingAct{},
		pendingPerm:    map[int]*pendingAsk{},
		pendingAskUser: map[int]*pendingAskUser{},
		freeAskWait:    map[int64]*pendingAskUser{},
		extractQueue:   make(chan int64, 32),
	}
	// G3: hidupkan tool ask_user — callback dari goroutine agent (loop utama bebas)
	g.agent.AskUserFn = g.askUser
	// G5: service manager custom (daftar user + watcher)
	InitServiceManager(cfg.Server.DataDir, g)
	return g, nil
}

// startUploadServices — dipanggil dari main.go SETELAH SetStore+SetFilesRoot
// (menghindari race: goroutine jalan sebelum filesRoot/store terisi).
// SetWebUI simpan token web UI untuk command /cfgweb (G3).
func (g *Gateway) SetWebUI(token string) { g.webUIToken = token }

// handleCfgWeb kirim URL + token web UI ke admin via chat (jangan pernah di grup).
func (g *Gateway) handleCfgWeb(chatID int64) {
	if g.webUIToken == "" {
		g.reply(chatID, "Web UI belum aktif.")
		return
	}
	msg := fmt.Sprintf(
		"🌐 *Web UI Aleph*\n\nhttp://100.72.168.93:8080/?t=`%s`\n\n"+
			"Token bersifat rahasia & berubah setiap bot restart. "+
			"Buka lewat jaringan Tailscale Anda saja.", g.webUIToken)
	g.reply(chatID, msg)
}

func (g *Gateway) StartUploadServices() {
	if g.store == nil || filesRoot == "" {
		return
	}
	go g.extractWorker()
	go g.watchInbox()
	g.resumePendingExtracts()
}

// Run loop utama long-polling — blocking; panggil dalam goroutine.
func (g *Gateway) Run() {
	u := tgbotapi.NewUpdate(0)
	u.Timeout = 30

	updates := g.api.GetUpdatesChan(u)
	for update := range updates {
		// ==== CALLBACK (tombol inline, Fase E) ====
		// Goroutine terpisah: saat agent path memblokir loop ini (menunggu
		// keputusan izin via askPermission), klik tombol tetap harus diproses —
		// kalau tidak, terjadi deadlock dan tombol tidak berfungsi.
		if update.CallbackQuery != nil {
			go g.handleCallback(update.CallbackQuery)
			continue
		}
		if update.Message == nil {
			continue
		}
		msg := update.Message

		// ==== Handler FOTO (Fase B — fix bug "balas Mandarin") ====
		if msg.Photo != nil && len(msg.Photo) > 0 {
			g.handlePhoto(msg)
			continue
		}
		if msg.Document != nil {
			go g.handleDocument(msg)
			continue
		}
		if msg.Text == "" {
			continue
		}
		g.handle(msg)
	}
}

// handlePhoto — Fase E+: coba vision via model aktif (HEC dsb punya capability vision).
// Kalau model menolak vision → fallback jawab rapi bahasa Indonesia.
func (g *Gateway) handlePhoto(msg *tgbotapi.Message) {
	if !g.cfg.IsTelegramAdmin(msg.From.ID) {
		return
	}
	chatID := msg.Chat.ID
	caption := msg.Caption
	if caption == "" {
		caption = "Jelaskan isi gambar ini secara ringkas dalam Bahasa Indonesia."
	}
	// konteks reply: foto dikirim sebagai balasan → sertakan isi pesan yang dibalas
	if rc := replyContext(msg); rc != "" {
		caption = rc + "\n\n" + caption
	}

	// butuh agent + LLM aktif
	if g.agent == nil || g.agent.LLM == nil {
		g.reply(chatID, "📸 Saya menerima foto, tapi otak AI belum aktif sehingga belum bisa melihatnya. Coba lagi saat agent aktif ya.")
		return
	}

	// download foto (pilih resolusi terbesar)
	photo := msg.Photo[len(msg.Photo)-1]
	fileCfg := tgbotapi.FileConfig{FileID: photo.FileID}
	file, err := g.api.GetFile(fileCfg)
	if err != nil {
		g.reply(chatID, "📸 Foto diterima tapi gagal diunduh ("+err.Error()+"). Coba kirim ulang ya.")
		return
	}
	url := "https://api.telegram.org/file/bot" + g.api.Token + "/" + file.FilePath
	httpClient := &http.Client{Timeout: 60 * time.Second}
	resp, err := httpClient.Get(url)
	if err != nil {
		g.reply(chatID, "📸 Foto diterima tapi gagal diunduh. Coba kirim ulang ya.")
		return
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
	if err != nil || len(data) == 0 {
		g.reply(chatID, "📸 Foto diterima tapi gagal dibaca. Coba kirim ulang ya.")
		return
	}

	// kirim ke LLM dengan progress
	prog, _ := g.api.Send(tgbotapi.NewMessage(chatID, "📸 menganalisis foto..."))
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	dataURL := "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(data)
	ans, err := g.agent.LLM.ChatVision(ctx, caption, dataURL)
	if err != nil || strings.TrimSpace(ans.Content) == "" {
		e := tgbotapi.NewEditMessageText(chatID, prog.MessageID,
			"📸 Saya menerima foto tapi model saat ini tidak bisa melihat gambar (vision tidak tersedia).\n\n"+
				"Alternatif: jelaskan isi fotonya dengan teks — saya bantu dari situ.")
		g.api.Send(e)
		return
	}
	out := "📸 *Analisis foto:*\n\n" + ans.Content
	// language guard
	if agent.HasCJKPublic(out) {
		if fix, err2 := g.agent.LLM.Chat(ctx, []llm.Message{
			{Role: "system", Content: "Terjemahkan ke Bahasa Indonesia natural. Keluarkan HANYA hasilnya."},
			{Role: "user", Content: ans.Content},
		}, nil); err2 == nil && strings.TrimSpace(fix.Content) != "" {
			out = "📸 *Analisis foto:*\n\n" + fix.Content
		}
	}
	e := tgbotapi.NewEditMessageText(chatID, prog.MessageID, out)
	e.ParseMode = "Markdown"
	if _, err := g.api.Send(e); err != nil {
		e2 := tgbotapi.NewEditMessageText(chatID, prog.MessageID, out)
		g.api.Send(e2)
	}
}

// waitForReply menunggu jawaban user di chat tertentu (untuk konfirmasi tier 🔴).
// Pesan yang datang saat menunggu dikonsumsi sebagai jawaban (bukan diproses router).
func (g *Gateway) waitForReply(chatID int64, timeout time.Duration) string {
	ch := make(chan string, 1)
	g.pendingMu.Lock()
	g.pending[chatID] = ch
	g.pendingMu.Unlock()
	defer func() {
		g.pendingMu.Lock()
		delete(g.pending, chatID)
		g.pendingMu.Unlock()
	}()
	select {
	case resp := <-ch:
		return resp
	case <-time.After(timeout):
		return ""
	}
}

func (g *Gateway) handle(msg *tgbotapi.Message) {
	chatID := msg.Chat.ID
	userID := msg.From.ID

	// Admin-only: user lain diabaikan diam-diam (log saja)
	if !g.cfg.IsTelegramAdmin(userID) {
		log.Printf("[telegram] tolak user %d (%s) — bukan admin", userID, msg.From.UserName)
		return
	}

	text := strings.TrimSpace(msg.Text)
	// jangan pernah menulis sandi WiFi ke log: kalau chat sedang menunggu input sandi,
	// catat [REDACTED] saja.
	g.pendingMu.Lock()
	waitingPw := false
	if act, ok := g.pendingAct[chatID]; ok && act != nil && act.Kind == "ap-pass" {
		waitingPw = true
	}
	g.pendingMu.Unlock()
	if waitingPw {
		log.Printf("[telegram] chat %d: [REDACTED] (input sandi WiFi)", chatID)
	} else {
		log.Printf("[telegram] chat %d: %s", chatID, truncate(text, 80))
	}

	// G3: kalau user sedang diminta jawaban bebas utk ask_user → konsumsi teksnya
	if g.consumeFreeAsk(chatID, text) {
		return
	}

	// ==== /batal — batalkan pending apa pun ====
	if text == "/batal" {
		g.pendingMu.Lock()
		_, hadConfirm := g.pending[chatID]
		_, hadAct := g.pendingAct[chatID]
		_, hadAsk := g.freeAskWait[chatID]
		delete(g.pending, chatID)
		delete(g.pendingAct, chatID)
		delete(g.freeAskWait, chatID)
		// batalkan semua pertanyaan ask_user terbuka di chat ini
		var openAsks []int
		for id, p := range g.pendingAskUser {
			if p != nil {
				select {
				case p.Answer <- "(dibatalkan user)":
				default:
					openAsks = append(openAsks, id)
				}
			}
		}
		if len(openAsks) > 0 {
			for _, id := range openAsks {
				delete(g.pendingAskUser, id)
			}
		}
		g.pendingMu.Unlock()
		if hadConfirm || hadAct || hadAsk {
			g.reply(chatID, "↩️ Dibatalkan.")
		} else {
			g.reply(chatID, "Tidak ada aksi yang menunggu.")
		}
		return
	}

	// ==== PENDING ACT (Fase E): input command / konfirmasi aksi tombol ====
	g.pendingMu.Lock()
	act, hasAct := g.pendingAct[chatID]
	g.pendingMu.Unlock()
	if hasAct {
		// saat menunggu input sandi: command ("/x") TIDAK dianggap sandi
		if act != nil && act.Kind == "ap-pass" && strings.HasPrefix(text, "/") {
			if text == "/batal" {
				g.pendingMu.Lock()
				delete(g.pendingAct, chatID)
				g.pendingMu.Unlock()
				g.reply(chatID, "↩️ Perubahan sandi dibatalkan.")
			} else {
				g.reply(chatID, "⚠️ Saya sedang menunggu sandi WiFi. Ketik sandinya, atau /batal untuk membatalkan.")
			}
			return
		}
		if g.noteConsumeInput(chatID, act, text) {
			return
		}
		if g.apConsumeInput(chatID, act, text) {
			return
		}
		g.consumePendingAct(chatID, act, text)
		return
	}

	// ==== PENDING CONFIRM (Fase A): jawaban "ya/batal" dikonsumsi waitForReply ====
	g.pendingMu.Lock()
	if ch, ok := g.pending[chatID]; ok {
		g.pendingMu.Unlock()
		select {
		case ch <- text:
		default:
		}
		return
	}
	g.pendingMu.Unlock()

	// ==== ROUTER ====
	// Awalan "/" = FAST PATH (0 token, instan)
	if strings.HasPrefix(text, "/") {
		// /model & /model <nama> ditangani di sini (butuh state)
		if text == "/model" || strings.HasPrefix(text, "/model ") {
			g.reply(chatID, g.handleModel(text))
			return
		}
		// /files — daftar file upload (G1, instan tanpa LLM)
		if text == "/files" || strings.HasPrefix(text, "/files ") {
			g.reply(chatID, g.handleFiles())
			return
		}
		// /cfgweb — kirim URL + token web UI ke admin (G3)
		if text == "/cfgweb" {
			g.handleCfgWeb(chatID)
			return
		}
		// /reloadapi — reload endpoint/API key + admin tanpa restart (G11 H-3)
		if text == "/reloadapi" {
			g.handleReloadAPI(chatID)
			return
		}
		out := g.fast.Handle(text)
		// /menu ditangani khusus: kirim inline keyboard (Fase E)
		if out == "__MENU__" {
			m := tgbotapi.NewMessage(chatID, "🏠 *Menu aleph-agent*\nPilih aksi:")
			m.ParseMode = "Markdown"
			m.ReplyMarkup = mainMenuKeyboard()
			if _, err := g.api.Send(m); err != nil {
				m2 := tgbotapi.NewMessage(chatID, "🏠 Menu aleph-agent — pilih aksi:")
				m2.ReplyMarkup = mainMenuKeyboard()
				g.api.Send(m2)
			}
			return
		}
		g.reply(chatID, out)
		return
	}

	// Tanpa "/" = AGENT PATH (LLM)
	// Konteks REPLY: kalau user membalas pesan tertentu, sertakan isi pesan yang
	// dibalas sebagai konteks (cukup untuk "apa maksud ini?", "jelasin yang ini", dll.)
	if rc := replyContext(msg); rc != "" {
		text = rc + "\n\n" + text
	}
	// Goroutine terpisah — WAJIB: handleAgent memblokir menunggu keputusan izin
	// (askPermission). Kalau sinkron, loop utama macet dan klik tombol tidak
	// pernah terbaca → tombol terasa hang. Itu akar bug berulang ini.
	go g.handleAgent(chatID, msg.From.FirstName, text)
}

// replyContext — ekstrak isi pesan yang di-reply sebagai konteks untuk agent.
// Return "" kalau bukan reply / tidak ada isi yang bisa diambil.
func replyContext(msg *tgbotapi.Message) string {
	r := msg.ReplyToMessage
	if r == nil {
		return ""
	}
	// identitas pengirim pesan yang dibalas
	who := ""
	switch {
	case r.From != nil && r.From.FirstName != "":
		who = r.From.FirstName
		if r.From.IsBot {
			who += " (bot)"
		}
	case r.From != nil && r.From.UserName != "":
		who = "@" + r.From.UserName
	default:
		who = "seseorang"
	}

	// isi: teks / caption (foto/dokumen) / pesan bot (jawaban agent sebelumnya)
	body := ""
	if r.Text != "" {
		body = r.Text
	} else if r.Caption != "" {
		body = "[media: " + r.Caption + "]"
	}

	// pesan agent berisi jawaban AI — batasi panjangnya (jawaban bisa panjang)
	const maxLen = 1200
	if len(body) > maxLen {
		body = body[:maxLen] + "…(dipotong)"
	}
	// pesan user biasa biasanya pendek; batasi juga demi keamanan token
	const maxUserLen = 800
	if len(body) > maxUserLen && r.From != nil && !r.From.IsBot {
		body = body[:maxUserLen] + "…(dipotong)"
	}
	if body == "" {
		// reply ke foto/dokumen/sticker tanpa caption — sampaikan tipenya saja
		if r.Photo != nil {
			body = "[foto]"
		} else if r.Document != nil {
			body = "[dokumen: " + r.Document.FileName + "]"
		} else if r.Sticker != nil {
			body = "[sticker]"
		} else {
			return ""
		}
	}
	return fmt.Sprintf("(User membalas pesan dari %s: \"%s\")", who, body)
}

// handleAgent memproses pesan natural via LLM+tools, dengan "typing..." indicator.
func (g *Gateway) handleAgent(chatID int64, userName, text string) {
	if g.agent == nil {
		g.reply(chatID, "🤖 Agent path belum aktif (llm belum dikonfigurasi).")
		return
	}

	// indicator typing selama proses
	stopTyping := make(chan struct{})
	go func() {
		t := time.NewTicker(4 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-stopTyping:
				return
			case <-t.C:
				g.api.Send(tgbotapi.NewChatAction(chatID, "typing"))
			}
		}
	}()
	defer close(stopTyping)

	// timeout agent path — dinaikkan (fix "context deadline exceeded"):
	// 150s → 240s → 600s (prompt izin butuh waktu user memutuskan)
	ctx, cancel := context.WithTimeout(context.Background(), 600*time.Second)
	defer cancel()

	start := time.Now()

	// ==== LIVE PROGRESS (Fase B): 1 pesan yang terus di-edit ====
	progMsg, _ := g.api.Send(tgbotapi.NewMessage(chatID, "🤔 memproses..."))
	progBase := ""
	if progMsg.Text != "" {
		progBase = progMsg.Text
	}
	var steps []string
	var doneLines []string
	stepNo, stepMax := 0, 0
	var progMu sync.Mutex
	lastTxt := ""
	renderProgress := func() {
		view := steps
		if len(view) > 4 {
			view = view[len(view)-4:]
		}
		dv := doneLines
		if len(dv) > 4 {
			dv = dv[len(dv)-4:]
		}
		var b strings.Builder
		b.WriteString(progBase)
		if stepMax > 0 {
			fmt.Fprintf(&b, " (langkah %d/%d)", stepNo, stepMax)
		}
		for _, d := range dv {
			b.WriteString("\n" + d)
		}
		for _, s := range view {
			b.WriteString("\n" + s + " ⏳")
		}
		txt := b.String()
		if txt == lastTxt {
			return // anti "message is not modified" spam
		}
		lastTxt = txt
		edit := tgbotapi.NewEditMessageText(chatID, progMsg.MessageID, txt)
		if _, err := g.api.Send(edit); err != nil {
			log.Printf("[telegram] edit progress: %v", err)
		}
	}
	editProgress := func(current string) {
		progMu.Lock()
		steps = append(steps, current)
		progMu.Unlock()
		renderProgress()
	}
	g.agent.Progress = editProgress
	// G2b: baris selesai ✔/✖ + durasi; G2c: counter langkah di header.
	// line = "✔ <step asli> (durasi)" — gateway mencocokkan via prefix step
	// (agent memangkas prefix "💻 $ " supaya cocok), jadi multi-tool per round tetap akurat.
	g.agent.ProgressDone = func(line string) {
		progMu.Lock()
		// cari step berjalan yang cocok: baris done tanpa "✔ " dan " (durasi)"
		body := line
		if strings.HasPrefix(body, "✔ ") || strings.HasPrefix(body, "✖ ") {
			body = body[2:]
		}
		if i := strings.LastIndex(body, " ("); i > 0 {
			body = body[:i]
		}
		matched := -1
		for i := len(steps) - 1; i >= 0; i-- {
			if steps[i] == body {
				matched = i
				break
			}
		}
		if matched >= 0 {
			steps = append(steps[:matched], steps[matched+1:]...)
		}
		doneLines = append(doneLines, line)
		progMu.Unlock()
		renderProgress()
	}
	g.agent.StepCounter = func(done, total int) {
		progMu.Lock()
		stepNo, stepMax = done, total
		progMu.Unlock()
		renderProgress()
	}

	// ==== F-perm: prompt izin 4-pilihan utk command baru ====
	ctx = perm.WithAsk(ctx, g.askPermission)

	// ==== KONFIRMASI (Fase A tier 🔴): tanya user, tunggu jawaban "ya/batal" ====
	g.agent.ConfirmFn = func(action, detail string) bool {
		ask := fmt.Sprintf("⚠️ Butuh konfirmasi Anda:\n%s — %s\n\nBalas *ya* untuk lanjut, *batal* untuk tolak.", action, detail)
		g.reply(chatID, ask)
		resp := g.waitForReply(chatID, 90*time.Second)
		respL := strings.ToLower(strings.TrimSpace(resp))
		return respL == "ya" || respL == "y" || respL == "ok" || respL == "lanjut" || respL == "yes"
	}

	ans, err := g.agent.HandleChatNamed(ctx, chatID, userName, text)
	if err != nil {
		log.Printf("[agent] error: %v", err)
		g.reply(chatID, "⚠️ Terjadi kendala saat memproses ("+err.Error()+"). Coba ulangi atau pecah tugasnya lebih kecil ya.")
		return
	}

	// finalize progress message → selesai
	progMu.Lock()
	finalTxt := progBase + "\n✅ selesai dalam " + time.Since(start).Round(time.Second).String()
	progMu.Unlock()
	edit := tgbotapi.NewEditMessageText(chatID, progMsg.MessageID, finalTxt)
	if _, err := g.api.Send(edit); err != nil {
		log.Printf("[telegram] edit final: %v", err)
	}
	g.agent.Progress = nil

	// simpan session
	g.mu.Lock()
	g.sessions[chatID] = &session{LastUserMsg: text, LastTime: time.Now()}
	g.mu.Unlock()

	log.Printf("[agent] selesai dalam %s", time.Since(start).Round(time.Millisecond))
	g.reply(chatID, ans)
}

// handleModel mengelola /model [nama].
func (g *Gateway) handleModel(text string) string {
	if g.agent == nil {
		return "Agent belum aktif."
	}
	parts := strings.Fields(text)
	if len(parts) == 1 {
		return fmt.Sprintf("Model aktif: %s\nDaftar: %s\n\nGanti: /model <nama>", g.model, strings.Join(g.cfg.LLM.Models, ", "))
	}
	want := parts[1]
	for _, m := range g.cfg.LLM.Models {
		if m == want {
			g.mu.Lock()
			g.model = want
			g.mu.Unlock()
			g.agent.LLM.SetModel(want)
			return fmt.Sprintf("✅ Model diganti ke %s", want)
		}
	}
	return fmt.Sprintf("Model '%s' tidak ada di daftar. Pilihan: %s", want, strings.Join(g.cfg.LLM.Models, ", "))
}

// SendDirect mengirim pesan ke admin pertama (dipakai alert engine).
func (g *Gateway) SendDirect(text string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if len(g.cfg.Admins.TelegramIDs) == 0 {
		return
	}
	chatID := g.cfg.Admins.TelegramIDs[0]
	msg := tgbotapi.NewMessage(chatID, text)
	if _, err := g.api.Send(msg); err != nil {
		log.Printf("[telegram] alert kirim gagal: %v", err)
	}
}

func (g *Gateway) reply(chatID int64, text string) {
	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = "Markdown"
	if _, err := g.api.Send(msg); err != nil {
		// fallback tanpa parse mode (markdown error)
		msg2 := tgbotapi.NewMessage(chatID, text)
		if _, err2 := g.api.Send(msg2); err2 != nil {
			log.Printf("[telegram] kirim gagal: %v / %v", err, err2)
		}
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
