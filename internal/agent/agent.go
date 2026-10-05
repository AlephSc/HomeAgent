// Package agent — ReAct loop: LLM + tools + verify.
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"aleph-agent/internal/chatlog"
	"aleph-agent/internal/llm"
	"aleph-agent/internal/tools"
)

const defaultMaxRounds = 10

// SessionStore — interface riwayat percakapan (dipenuhi *memory.Store).
type SessionStore interface {
	RecentSession(chatID int64, n int) []string
	AppendSession(chatID int64, role, content string)
}

// SummarizerStore — penyimpanan running summary per chat (dipenuhi *memory.Store).
type SummarizerStore interface {
	GetSummary(chatID int64) (string, bool)
	SetSummary(chatID int64, summary string)
}

// MemoryRecall — auto-recall memory (dipenuhi *memory.Store).
type MemoryRecall interface {
	AutoRecall(userMsg string, max int) []string
}

// Agent — otak AI dengan akses tools.
type Agent struct {
	LLM       *llm.Client
	Tools     *tools.Registry
	Prompt    string // system prompt
	MaxRounds int    // budget langkah tool (dari config llm.max_tool_calls)

	// Progress dipanggil sebelum tiap tool call (live narration, Fase B).
	// step: deskripsi singkat apa yang akan dikerjakan (dgn argumen nyata, G2a).
	Progress func(step string)
	// ProgressDone dipanggil setelah tool selesai (G2b): step + durasi + status
	ProgressDone func(step string)
	// StepCounter — langkah berjalan / maks (G2c). Nil = tanpa counter.
	StepCounter func(done, total int)
	// ConfirmFn: konfirmasi user untuk aksi tier 🔴 (Fase A). Nil = tanpa konfirmasi.
	ConfirmFn tools.ConfirmFunc
	// AskUserFn (G3): tanya user dgn pilihan; return jawaban user. Nil = tool tak aktif.
	// question: teks pertanyaan; options: 2-4 pilihan; allowFree: user boleh jawab bebas.
	AskUserFn func(question string, options []string, allowFree bool) (string, error)
	// Session: riwayat percakapan per chat (opsional).
	Session SessionStore
	// F6a — compacting
	Summarizer    SummarizerStore
	ContextBudget int // token budget verbatim (default 65536 dari config)

	// B1/B2/B4 — config tambahan (mutex cfgMu)
	DataDir      string // untuk persist overlay (agent-config.json); kosong = persist off
	maxMemoryMB  int    // B1: soft limit RAM (0 = off)
	maxSubAgents int    // B2: maks sub-agent paralel (0 = default 2)

	// G11 H-1 — bahan rebuild prompt (diset sekali di start; Prompt di-rebuild runtime)
	ServerName  string         // nama server (arg BuildPromptExtra)
	PromptExtra string         // extra (skill index + lessons + preferensi)
	toolsRef    ToolsSummaryer // sumber daftar tools (reg.Summary())
	promptMu    sync.Mutex     // lindungi promptCur
	promptCur   string         // prompt aktif ter-rebuild
	toolsMu     sync.Mutex     // lindungi toolsRef
	// F6b — memory auto-recall (opsional)
	Memory MemoryRecall
	// ChatLog — log percakapan utk debugging (opsional, nil = off)
	Log    *chatlog.Logger
	ChatID int64

	// internal state compacting (per HandleChat; JANGAN diakses antar-goroutine)
	promptTokCache int
	lastKeptHist   []string

	// G4b state: chatID → jumlah pesan hist yang sudah dicakup ringkasan proaktif.
	// Antarn-goroutine (updateSummary async) → akses hanya dari HandleChatNamed + mutex.
	summaryMu       sync.Mutex
	summarizedCount map[int64]int

	// G3 runtime config: lindungi ContextBudget/MaxRounds/LLM dari akses paralel
	// (gateway telegram + web UI bisa mengubah bersamaan).
	cfgMu sync.Mutex
}

// New membuat agent. maxRounds <= 0 → default 10.
func New(c *llm.Client, reg *tools.Registry, systemPrompt string, maxRounds int) *Agent {
	if maxRounds <= 0 {
		maxRounds = defaultMaxRounds
	}
	return &Agent{LLM: c, Tools: reg, Prompt: systemPrompt, MaxRounds: maxRounds,
		summarizedCount: map[int64]int{}}
}

// progressStep menerjemahkan nama tool + args jadi narasi singkat (template, 0 token).
// execAskUser — G3: kirim pertanyaan + inline keyboard; tunggu jawaban user
// (blokir goroutine agent, bukan loop Telegram; callback tombol diproses loop utama).
func (a *Agent) execAskUser(ctx context.Context, args string) string {
	var q struct {
		Question  string   `json:"question"`
		Options   []string `json:"options"`
		AllowFree bool     `json:"allow_free"`
	}
	if err := json.Unmarshal([]byte(args), &q); err != nil {
		return "ERROR: arguments bukan JSON valid"
	}
	q.Question = strings.TrimSpace(q.Question)
	if q.Question == "" {
		return "ERROR: question kosong"
	}
	if len(q.Options) < 2 || len(q.Options) > 4 {
		return "ERROR: options harus 2-4 item"
	}
	if a.AskUserFn == nil {
		return "ERROR: fitur bertanya tidak tersedia di gateway ini"
	}
	// timeout mengikuti sisa ctx agent (max 60s seperti tool lain agar konsisten)
	tCtx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()
	ansCh := make(chan struct {
		ans string
		err error
	}, 1)
	go func() {
		ans, err := a.AskUserFn(q.Question, q.Options, q.AllowFree)
		select {
		case ansCh <- struct {
			ans string
			err error
		}{ans, err}:
		case <-tCtx.Done():
		}
	}()
	select {
	case r := <-ansCh:
		if r.err != nil {
			return "user tidak menjawab: " + r.err.Error()
		}
		return "jawaban user: " + r.ans
	case <-tCtx.Done():
		return "user tidak menjawab dalam waktu tunggu (120s) — lanjutkan dengan asumsi terbaik dan sebutkan asumsimu."
	}
}

// argSummary — ekstrak field dari args JSON (aman, "" kalau gagal).
func argSummary(args string, keys ...string) string {
	var m map[string]interface{}
	if err := json.Unmarshal([]byte(args), &m); err != nil {
		return ""
	}
	for _, k := range keys {
		if v, ok := m[k].(string); ok && v != "" {
			return v
		}
	}
	return ""
}

// shorten — potong teks panjang utk tampilan progress.
func shorten(s string, max int) string {
	s = strings.TrimSpace(s)
	for i := 0; i < len(s); i++ { // rapikan newline/tab
		if s[i] == '\n' || s[i] == '\r' || s[i] == '	' {
			s = s[:i] + " " + s[i+1:]
		}
	}
	if len(s) > max {
		return s[:max] + "…"
	}
	return s
}

// maskSensitive — sembunyikan nilai yang tampak seperti token/key di progress.
func maskSensitive(s string) string {
	for _, pat := range []string{"api_key", "token", "password", "secret", "apikey"} {
		low := strings.ToLower(s)
		if i := strings.Index(low, pat); i >= 0 {
			// potong mulai dari kata kunci
			s = s[:i] + pat + "=[REDACTED]"
		}
	}
	return s
}

// progressStep — narasi detail per tool dengan ARGUMEN NYATA (G2a).
func progressStep(name, args string) string {
	switch name {
	case "server_status":
		return "🔍 cek kondisi server"
	case "service_control":
		if a := argSummary(args, "action"); a != "" {
			if u := argSummary(args, "unit"); u != "" {
				return "⚙️ " + a + " " + u
			}
			return "⚙️ " + a + " service"
		}
		return "⚙️ kontrol service"
	case "list_files":
		if p := argSummary(args, "path"); p != "" {
			return "📂 lihat " + shorten(p, 50)
		}
		return "📂 lihat isi direktori"
	case "read_file":
		if p := argSummary(args, "path"); p != "" {
			return "📄 baca " + shorten(p, 50)
		}
		return "📄 baca file"
	case "write_file":
		p := argSummary(args, "path")
		if p != "" {
			return "📝 tulis " + shorten(p, 50)
		}
		return "📝 tulis file"
	case "exec_command":
		if c := argSummary(args, "command"); c != "" {
			return "💻 $ " + shorten(maskSensitive(c), 60)
		}
		return "💻 jalankan command"
	case "web_search":
		if q := argSummary(args, "query"); q != "" {
			return "🌐 cari: " + shorten(q, 50)
		}
		return "🌐 cari di web"
	case "web_fetch":
		if u := argSummary(args, "url"); u != "" {
			return "🌐 baca " + shorten(u, 50)
		}
		return "🌐 baca halaman web"
	case "memory_recall":
		if q := argSummary(args, "query"); q != "" {
			return "🧠 ingat: " + shorten(q, 40)
		}
		return "🧠 cari ingatan"
	case "recall_skill":
		if n := argSummary(args, "name"); n != "" {
			return "📚 skill: " + n
		}
		return "📚 baca skill"
	case "add_lesson":
		return "💡 catat pelajaran"
	case "ask_user":
		if q := argSummary(args, "question"); q != "" {
			return "❓ tanya: " + shorten(q, 45)
		}
		return "❓ bertanya ke user"
	}
	return "🔧 " + name
}

// HandleChat — Handle + session context per chat + language guard (pemakaian utama gateway).
func (a *Agent) HandleChat(ctx context.Context, chatID int64, userMsg string) (string, error) {
	return a.HandleChatNamed(ctx, chatID, "", userMsg)
}

// HandleChatNamed — HandleChat dengan nama user untuk chatlog (G2d).
func (a *Agent) HandleChatNamed(ctx context.Context, chatID int64, userName, userMsg string) (string, error) {
	start := time.Now()
	a.ChatID = chatID
	if a.Log != nil {
		a.Log.User(chatID, userName, userMsg)
	}
	// F6b: auto-recall memory relevan (diam-diam suntik)
	if a.Memory != nil {
		if hits := a.Memory.AutoRecall(userMsg, 3); len(hits) > 0 {
			userMsg = "⬇ Catatan Anda sendiri yang relevan (dari memori jangka panjang Anda):\n" +
				strings.Join(hits, "\n") +
				"\n⬆\n\nPesan user: " + userMsg
		}
	}
	// F6a: compacting — summary + verbatim sesuai budget
	hist := []string{}
	if a.Session != nil {
		hist = a.Session.RecentSession(chatID, 200)
	}
	if cb := a.ContextBudgetSnapshot(); cb > 0 {
		_, userMsg = a.CompactContext(chatID, userMsg, hist, cb)
	} else if a.Session != nil {
		// fallback lama: 6 pesan terakhir
		if h := a.Session.RecentSession(chatID, 6); len(h) > 0 {
			userMsg = "Konteks percakapan sebelumnya (paling lama dulu):\n" +
				strings.Join(h, "\n") + "\n\nPesan user sekarang: " + userMsg
		}
	}
	ans, err := a.Handle(ctx, userMsg)
	if err != nil {
		if a.Log != nil {
			a.Log.Err(chatID, "agent", err)
		}
		return "", err
	}
	if a.Log != nil {
		a.Log.Agent(chatID, ans, time.Since(start))
	}
	// simpan ke session
	if a.Session != nil {
		a.Session.AppendSession(chatID, "user", userMsg)
		a.Session.AppendSession(chatID, "assistant", ans)
	}
	// language guard level kode: output mengandung blok Han/CJK → re-ask sekali
	if hasCJK(ans) {
		if fix, err2 := a.forceIndonesian(ctx, ans); err2 == nil && strings.TrimSpace(fix) != "" {
			ans = fix
			if a.Session != nil {
				a.Session.AppendSession(chatID, "assistant", ans)
			}
		}
	}
	return ans, nil
}

// RunPrompt — jalankan prompt tanpa chat session (dipakai cron F7).
func (a *Agent) RunPrompt(ctx context.Context, prompt string) (string, error) {
	return a.Handle(ctx, prompt)
}

// forceIndonesian minta model menerjemahkan/menulis ulang jawaban ke Bahasa Indonesia.
func (a *Agent) forceIndonesian(ctx context.Context, original string) (string, error) {
	msgs := []llm.Message{
		{Role: "system", Content: "Terjemahkan / tulis ulang teks berikut ke Bahasa Indonesia yang natural. Keluarkan HANYA hasilnya, tanpa penjelasan."},
		{Role: "user", Content: original},
	}
	msg, err := a.LLM.Chat(ctx, msgs, nil)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(msg.Content), nil
}

// hasCJK mendeteksi karakter Han/Hangana/Kana (indikasi bahasa asing tak diminta).
func hasCJK(s string) bool {
	count := 0
	for _, r := range s {
		if (r >= 0x4E00 && r <= 0x9FFF) || (r >= 0x3040 && r <= 0x30FF) || (r >= 0xAC00 && r <= 0xD7AF) {
			count++
			if count >= 5 { // >5 karakter CJK = bukan kebetulan
				return true
			}
		}
	}
	return false
}

// HasCJKPublic — versi publik untuk paket lain (gateway).
func HasCJKPublic(s string) bool { return hasCJK(s) }

// Handle memproses satu pesan user, mengembalikan jawaban final.
func (a *Agent) Handle(ctx context.Context, userMsg string) (string, error) {
	// konfirmasi tier 🔴 (Fase A) disuntikkan ke tools via context
	if a.ConfirmFn != nil {
		ctx = tools.WithConfirm(ctx, a.ConfirmFn)
	}
	messages := []llm.Message{
		{Role: "system", Content: a.PromptSnapshot()},
		{Role: "user", Content: userMsg},
	}

	toolDefs := a.Tools.LLMDefs()
	if a.AskUserFn != nil {
		var askDef llm.ToolDef
		askDef.Type = "function"
		askDef.Function.Name = "ask_user"
		askDef.Function.Description = "Ajukan pertanyaan pilihan ganda ke user via tombol Telegram. Gunakan saat butuh keputusan/pilihan user sebelum melanjutkan."
		askDef.Function.Parameters = map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"question":   map[string]interface{}{"type": "string", "description": "pertanyaan singkat"},
				"options":    map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}, "description": "2-4 pilihan singkat"},
				"allow_free": map[string]interface{}{"type": "boolean", "description": "true = user juga boleh menjawab bebas (teks)"},
			},
			"required": []string{"question", "options"},
		}
		toolDefs = append(toolDefs, askDef)
	}

	stepCount := 0
	maxRounds := a.MaxRoundsSnapshot()
	for round := 0; round < maxRounds; round++ {
		// Deadline-aware: sisa waktu < 90s → JANGAN mulai LLM call ber-tools lagi
		// (rawan deadline). Satu call penutup tanpa tools: rangkum temuan sekarang.
		if dl, ok := ctx.Deadline(); ok {
			remain := time.Until(dl)
			if remain < 90*time.Second && round > 0 {
				messages = append(messages, llm.Message{
					Role: "user",
					Content: "Waktu hampir habis. JANGAN panggil tool lagi. " +
						"Rangkum temuan sejauh ini + langkah yang tersisa, jawab sekarang.",
				})
				msg, err := a.LLM.Chat(ctx, messages, nil)
				if err == nil && strings.TrimSpace(msg.Content) != "" {
					return strings.TrimSpace(msg.Content), nil
				}
				break
			}
		}
		msg, err := a.LLM.Chat(ctx, messages, toolDefs)
		if err != nil {
			if a.Log != nil {
				a.Log.Err(a.ChatID, "llm round "+fmt.Sprint(round), err)
			}
			return "", err
		}

		// Tidak ada tool call → jawaban final
		if len(msg.ToolCalls) == 0 {
			ans := strings.TrimSpace(msg.Content)
			if ans == "" {
				// beberapa thinking model taruh jawaban dengan reasoning; minta ulang ringkas
				return "(model tidak mengirim jawaban teks — coba ulangi pertanyaan)", nil
			}
			return ans, nil
		}

		// simpan assistant message dengan tool_calls
		messages = append(messages, *msg)

		// eksekusi semua tool calls
		for _, tc := range msg.ToolCalls {
			stepCount++
			if a.StepCounter != nil {
				a.StepCounter(stepCount, maxRounds)
			}
			if a.Progress != nil {
				a.Progress(progressStep(tc.Function.Name, tc.Function.Arguments))
			}
			tStart := time.Now()
			result := a.execTool(ctx, tc)
			if a.ProgressDone != nil {
				st := progressStep(tc.Function.Name, tc.Function.Arguments)
				st = strings.TrimPrefix(st, "💻 $ ")
				st = strings.TrimPrefix(st, "💻 ")
				ok := !strings.HasPrefix(result, "exit error") && !strings.HasPrefix(result, "⛔")
				mark := "✔"
				if !ok {
					mark = "✖"
				}
				a.ProgressDone(fmt.Sprintf("%s %s (%s)", mark, st, time.Since(tStart).Round(100*time.Millisecond)))
			}
			if a.Log != nil {
				a.Log.Tool(a.ChatID, tc.Function.Name, tc.Function.Arguments, result)
			}
			messages = append(messages, llm.Message{
				Role:       "tool",
				Content:    result,
				ToolCallID: tc.ID,
				Name:       tc.Function.Name,
			})
		}
	}
	// Budget tool habis: minta model meringkas temuan sejauh ini (bukan dead-end).
	messages = append(messages, llm.Message{
		Role: "user",
		Content: "Budget langkah tool sudah habis. JANGAN panggil tool lagi. " +
			"Ringkas jawaban terbaik dari data yang sudah kamu kumpulkan di atas. " +
			"Kalau tugas belum selesai, jelaskan langkah apa saja yang sudah dilakukan dan apa yang tersisa.",
	})
	msg, err := a.LLM.Chat(ctx, messages, nil)
	if err == nil && strings.TrimSpace(msg.Content) != "" {
		return strings.TrimSpace(msg.Content), nil
	}
	return "(mencapai batas langkah tool — coba pertanyaan yang lebih spesifik)", nil
}

// execTool mengeksekusi satu tool call dengan guard (timeout + recover).
func (a *Agent) execTool(ctx context.Context, tc llm.ToolCall) string {
	// G3: ask_user — pertanyaan pilihan ganda inline ke user, jawaban kembali ke model.
	if tc.Function.Name == "ask_user" {
		res := a.execAskUser(ctx, tc.Function.Arguments)
		if a.ProgressDone != nil {
			q := argSummary(tc.Function.Arguments, "question")
			a.ProgressDone("❓ " + shorten(q, 45) + " → " + shorten(res, 30))
		}
		if a.Log != nil {
			a.Log.Tool(a.ChatID, "ask_user", tc.Function.Arguments, res)
		}
		return res
	}
	tool, ok := a.Tools.Get(tc.Function.Name)
	if !ok {
		return fmt.Sprintf("ERROR: tool '%s' tidak dikenal. Tool tersedia: %s",
			tc.Function.Name, strings.Join(a.Tools.Names(), ", "))
	}

	// guard: args harus JSON object valid
	trimmed := strings.TrimSpace(tc.Function.Arguments)
	if trimmed == "" {
		trimmed = "{}"
	}
	if !json.Valid([]byte(trimmed)) {
		return "ERROR: arguments bukan JSON valid"
	}

	// timeout per tool 60s
	tCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	var (
		result   tools.Result
		panicked bool
	)
	func() {
		defer func() {
			if r := recover(); r != nil {
				panicked = true
				result = tools.Fail("tool panic: %v", r)
			}
		}()
		result = tool.Fn(tCtx, trimmed)
	}()
	_ = panicked

	if result.Err != "" {
		return "ERROR: " + result.Err
	}
	if result.Output == "" {
		return "(tool selesai, tanpa output)"
	}
	return result.Output
}
