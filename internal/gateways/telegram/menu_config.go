package telegram

// menu_config.go — G7a: menu ⚙️ Config AI di Telegram.
// Ubah runtime config agent tanpa restart: max context, max tool calls,
// timeout LLM, model. Semua setter mutex-protected (hardening H-A/H-B).
// Callback routing "cfg:*"; TIDAK persist ke disk (config file tetap sumber
// default saat restart — perubahan runtime bersifat sesi, sama seperti /model).

import (
	"fmt"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// cfgMenuKeyboard — menu utama config AI.
func (g *Gateway) cfgMenuKeyboard() tgbotapi.InlineKeyboardMarkup {
	cb := g.agent.GetContextBudget()
	to := g.agent.GetTimeout()
	mr := g.agent.GetMaxRounds()
	grid := [][]tgbotapi.InlineKeyboardButton{
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(fmt.Sprintf("🧠 Max Context: %s ▸", humanTokens(cb)), "cfg:ctx"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(fmt.Sprintf("🔁 Max Tool Calls: %d ▸", mr), "cfg:rounds"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(fmt.Sprintf("⏱ Timeout LLM: %ds ▸", to), "cfg:to"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(fmt.Sprintf("🤖 Model: %s ▸", g.model), "cfg:model"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(fmt.Sprintf("🧲 Max Memory: %s ▸", memoryLabel(g.agent.GetMaxMemoryMB())), "cfg:mem"),
			tgbotapi.NewInlineKeyboardButtonData(fmt.Sprintf("🤖👥 Sub-Agent: %d ▸", g.agent.SubAgentLimit()), "cfg:sub"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(fmt.Sprintf("🖥 Systemd RAM: %s ▸", g.sysdMemLabel()), "cfg:sysd"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("♻️ Reset ke Default", "cfg:reset"),
			tgbotapi.NewInlineKeyboardButtonData("◂ Menu Utama", "menu:home"),
		),
	}
	return tgbotapi.NewInlineKeyboardMarkup(grid...)
}

func humanTokens(n int) string {
	switch {
	case n >= 1024*1024:
		return fmt.Sprintf("%dM", n/(1024*1024))
	case n >= 1024:
		return fmt.Sprintf("%dk", n/1024)
	default:
		return fmt.Sprintf("%d", n)
	}
}

// cfgSubKeyboard — submenu pilihan per parameter.
func (g *Gateway) cfgSubKeyboard(kind string) tgbotapi.InlineKeyboardMarkup {
	var grid [][]tgbotapi.InlineKeyboardButton
	addRow := func(label, data string) {
		grid = append(grid, tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonData(label, data)))
	}
	switch kind {
	case "ctx":
		addRow("✏️ Custom token…", "cfg:customctx")
		for _, v := range []struct {
			label string
			val   int
		}{{"4k", 4096}, {"8k", 8192}, {"16k", 16384}, {"32k", 32768}, {"64k", 65536}, {"128k", 131072}} {
			warn := ""
			if v.val >= 32768 {
				warn = " ⚠️ lambat di N2600"
			}
			addRow(fmt.Sprintf("%s%s", v.label, warn), fmt.Sprintf("cfg:setctx:%d", v.val))
		}
	case "rounds":
		addRow("✏️ Custom…", "cfg:customrounds")
		for _, v := range []int{5, 10, 20, 50} {
			addRow(fmt.Sprintf("%d langkah", v), fmt.Sprintf("cfg:setrounds:%d", v))
		}
	case "to":
		addRow("✏️ Custom detik…", "cfg:customto")
		for _, v := range []int{60, 120, 180, 300, 600} {
			addRow(fmt.Sprintf("%d detik", v), fmt.Sprintf("cfg:setto:%d", v))
		}
	case "model":
		// G8: daftar model paged — dibangun oleh modelPageKeyboard (menu_models.go)
		ids, err := g.loadModelIDs()
		if err != nil {
			addRow("⚠️ gagal ambil daftar", "cfg:modelnoop")
		} else {
			_ = ids // keyboard dibangun di handleCfgModels
		}
	case "mem":
		addRow("✏️ Custom MB…", "cfg:custommem")
		for _, v := range []int{100, 150, 200, 300} {
			mark := ""
			if g.agent.GetMaxMemoryMB() == v {
				mark = " ✅"
			}
			addRow(fmt.Sprintf("%d MB%s", v, mark), fmt.Sprintf("cfg:setmem:%d", v))
		}
		addRow("⛔ Off (tanpa limit)", "cfg:setmem:0")
	case "sub":
		addRow("✏️ Custom…", "cfg:customsub")
	case "sysd":
		addRow("✏️ Custom MB… / ∞", "cfg:customsysd")
		for _, v := range []struct {
			label string
			val   string
		}{{"200M", "200"}, {"400M", "400"}, {"1G", "1024"}, {"2G", "2048"}, {"∞ tanpa batas", "infinity"}} {
			mark := ""
			if g.sysdMemLabel() == v.label {
				mark = " ✅"
			}
			addRow(fmt.Sprintf("%s%s", v.label, mark), "cfg:setsysd:"+v.val)
		}
		for _, v := range []int{1, 2, 3} {
			mark := ""
			if g.agent.SubAgentLimit() == v {
				mark = " ✅"
			}
			note := map[int]string{1: "hemat", 2: "rekomendasi", 3: "RAM naik ~8-16 MB"}[v]
			addRow(fmt.Sprintf("%d paralel (%s)%s", v, note, mark), fmt.Sprintf("cfg:setsub:%d", v))
		}
	}
	grid = append(grid, tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData("◂ Config AI", "cfg:home"),
	))
	return tgbotapi.NewInlineKeyboardMarkup(grid...)
}

// cfgHeaderText — ringkasan config aktif.
func (g *Gateway) cfgHeaderText() string {
	var b strings.Builder
	b.WriteString("⚙️ *Config AI* (runtime, tanpa restart)\n\n")
	fmt.Fprintf(&b, "🧠 Max Context: %s token (verbatim sebelum compacting)\n", humanTokens(g.agent.GetContextBudget()))
	fmt.Fprintf(&b, "🔁 Max Tool Calls: %d langkah per tugas\n", g.agent.GetMaxRounds())
	fmt.Fprintf(&b, "⏱ Timeout LLM: %d detik per ronde\n", g.agent.GetTimeout())
	fmt.Fprintf(&b, "🤖 Model: %s\n", g.model)
	fmt.Fprintf(&b, "🧲 Max Memory: %s (tolak delegasi ≥85%%)\n", memoryLabel(g.agent.GetMaxMemoryMB()))
	fmt.Fprintf(&b, "🤖👥 Max Sub-Agent: %d paralel\n", g.agent.SubAgentLimit())
	b.WriteString("\n⚠️ Konteks besar (32k/64k) membuat tiap ronde lambat di Atom N2600.\n")
	b.WriteString("💾 Perubahan TERSIMPAN permanen (tahan restart).")
	return b.String()
}

// sysdMemLabel — baca MemoryMax efektif dari systemd (cache 30 dtk).
func (g *Gateway) sysdMemLabel() string {
	out, err := runOutput(10*time.Second, "systemctl", "show", "aleph-agent", "-p", "MemoryMax", "--value")
	if err != nil {
		return "?"
	}
	v := strings.TrimSpace(out)
	if v == "infinity" {
		return "∞"
	}
	var n int64
	if _, err := fmt.Sscanf(v, "%d", &n); err != nil {
		return "?"
	}
	mb := n / (1024 * 1024)
	return fmt.Sprintf("%dM", mb)
}

// handleSysdMem — set MemoryMax via helper root aleph-memctl.sh (whitelist sudo).
func (g *Gateway) handleSysdMem(chatID int64, valMB string) {
	arg := valMB
	if arg != "infinity" {
		arg = valMB + "M"
	}
	out, err := runOutput(60*time.Second, "sudo", "-n", "/usr/local/sbin/aleph-memctl.sh", arg)
	if err != nil {
		g.reply(chatID, "❌ Gagal: "+err.Error()+"\n"+truncStr(out, 300))
		return
	}
	if strings.Contains(out, "OK status=active") {
		g.reply(chatID, "✅ Systemd MemoryMax = "+g.sysdMemLabel()+" (bot sudah restart otomatis)")
		return
	}
	g.reply(chatID, "⚠️ Hasil tak terduga:\n"+truncStr(out, 400))
}

// truncStr potong string rune-safe.
func truncStr(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// memoryLabel — tampilan max memory.
func memoryLabel(mb int) string {
	if mb <= 0 {
		return "off"
	}
	return fmt.Sprintf("%dM", mb)
}

// cfgSave — simpan config persist (dipanggil setelah tiap perubahan sukses).
func (g *Gateway) cfgSave() {
	g.agent.SavePersist()
}

// handleCfgCallback — dispatch callback "cfg:*".
func (g *Gateway) handleCfgCallback(answer func(string), chatID int64, msgID int, data string) {
	if g.agent == nil {
		answer("agent belum aktif")
		g.reply(chatID, "⚠️ Agent path belum aktif (LLM belum dikonfigurasi).")
		return
	}
	op := strings.TrimPrefix(data, "cfg:")
	arg := ""
	if i := strings.Index(op, ":"); i >= 0 {
		arg = op[i+1:]
		op = op[:i]
	}

	// helper edit menu
	editMenu := func(text string, kb tgbotapi.InlineKeyboardMarkup) {
		e := tgbotapi.NewEditMessageTextAndMarkup(chatID, msgID, text, kb)
		e.ParseMode = "Markdown"
		if _, err := g.api.Send(e); err != nil {
			e2 := tgbotapi.NewEditMessageText(chatID, msgID, text)
			g.api.Send(e2)
		}
	}

	switch op {
	case "home":
		answer("config AI")
		editMenu(g.cfgHeaderText(), g.cfgMenuKeyboard())

	case "ctx":
		answer("pilih max context")
		editMenu("🧠 *Max Context* — token verbatim sebelum compacting.\n⚠️ 32k/64k lambat di N2600.", g.cfgSubKeyboard("ctx"))
	case "setctx":
		var v int
		if _, err := fmt.Sscanf(arg, "%d", &v); err != nil {
			answer("nilai salah")
			return
		}
		if err := g.agent.SetContextBudget(v); err != nil {
			answer("ditolak: " + err.Error())
			return
		}
		answer("max context = " + humanTokens(v))
		g.cfgSave()
		editMenu(g.cfgHeaderText(), g.cfgMenuKeyboard())

	case "rounds":
		answer("pilih max tool calls")
		editMenu("🔁 *Max Tool Calls* — budget langkah tool per tugas.", g.cfgSubKeyboard("rounds"))
	case "setrounds":
		var v int
		if _, err := fmt.Sscanf(arg, "%d", &v); err != nil {
			answer("nilai salah")
			return
		}
		if err := g.agent.SetMaxRounds(v); err != nil {
			answer("ditolak: " + err.Error())
			return
		}
		answer(fmt.Sprintf("max tool calls = %d", v))
		g.cfgSave()
		editMenu(g.cfgHeaderText(), g.cfgMenuKeyboard())

	case "to":
		answer("pilih timeout")
		editMenu("⏱ *Timeout LLM* — batas waktu tiap ronde LLM (detik).", g.cfgSubKeyboard("to"))
	case "setto":
		var v int
		if _, err := fmt.Sscanf(arg, "%d", &v); err != nil {
			answer("nilai salah")
			return
		}
		if err := g.agent.SetTimeout(v); err != nil {
			answer("ditolak: " + err.Error())
			return
		}
		answer(fmt.Sprintf("timeout = %ds", v))
		g.cfgSave()
		editMenu(g.cfgHeaderText(), g.cfgMenuKeyboard())

	case "model", "modelpage", "modelrefresh", "modsearch", "modsearchpage", "modelnoop", "setmodel":
		// G8: seluruh navigasi model → handler paged (menu_models.go)
		g.handleCfgModels(answer, chatID, msgID, op, arg, editMenu)
		return

	// ===== B1/B2/B4: Max Memory, Max Sub-Agent, Reset =====
	case "mem":
		answer("pilih max memory")
		editMenu("🧲 *Max Memory* — soft limit RAM proses bot.\nSaat ≥85%% dari limit, delegasi sub-agent ditolak sementara.", g.cfgSubKeyboard("mem"))
	case "setmem":
		var v int
		if _, err := fmt.Sscanf(arg, "%d", &v); err != nil {
			answer("nilai salah")
			return
		}
		if err := g.agent.SetMaxMemoryMB(v); err != nil {
			answer("ditolak: " + err.Error())
			return
		}
		g.cfgSave()
		answer("max memory = " + memoryLabel(v))
		editMenu(g.cfgHeaderText(), g.cfgMenuKeyboard())

	case "sub":
		answer("pilih max sub-agent")
		editMenu("🤖👥 *Max Sub-Agent* — delegasi paralel.\n3 = RAM naik ~8–16 MB saat penuh.", g.cfgSubKeyboard("sub"))
	case "setsub":
		var v int
		if _, err := fmt.Sscanf(arg, "%d", &v); err != nil {
			answer("nilai salah")
			return
		}
		if err := g.agent.SetMaxSubAgents(v); err != nil {
			answer("ditolak: " + err.Error())
			return
		}
		g.cfgSave()
		answer(fmt.Sprintf("max sub-agent = %d", v))
		editMenu(g.cfgHeaderText(), g.cfgMenuKeyboard())

	case "customctx", "customrounds", "customto", "custommem", "customsub":
		// G13: minta angka bebas dari user
		answer("ketik angka")
		g.pendingMu.Lock()
		g.pendingAct[chatID] = &pendingAct{Kind: "cfg-num", Action: op}
		g.pendingMu.Unlock()
		var hint string
		switch op {
		case "customctx":
			hint = "2048–1048576 token (mis. 24576)"
		case "customrounds":
			hint = "1–500 langkah"
		case "customto":
			hint = "30–3600 detik"
		case "custommem":
			hint = "64–65536 MB, atau 0 = off"
		case "customsub":
			hint = "1–16 paralel"
		}
		e := tgbotapi.NewMessage(chatID, "✏️ *Custom* — ketik angka:\n"+hint+"\n\n/batal untuk membatalkan.")
		e.ParseMode = "Markdown"
		g.api.Send(e)

	case "sysd":
		answer("systemd RAM")
		editMenu(fmt.Sprintf("🖥 *Systemd MemoryMax* (hard cap cgroup)\nAktif: %s\n\nMengubah ini me-restart bot otomatis (via helper aman aleph-memctl.sh).\nUntuk server besar: pilih angka besar atau ∞.", g.sysdMemLabel()), g.cfgSubKeyboard("sysd"))
	case "setsysd":
		var v int
		if _, err := fmt.Sscanf(arg, "%d", &v); err != nil {
			answer("nilai salah")
			return
		}
		g.handleSysdMem(chatID, fmt.Sprintf("%d", v))
	case "customsysd":
		answer("ketik angka")
		g.pendingMu.Lock()
		g.pendingAct[chatID] = &pendingAct{Kind: "cfg-num", Action: "customsysd"}
		g.pendingMu.Unlock()
		e := tgbotapi.NewMessage(chatID, "✏️ *Custom Systemd RAM* — ketik MB (64–65536) atau `infinity` untuk tanpa batas.\n⚠️ Bot akan restart otomatis setelah perubahan.\n/batal untuk membatalkan.")
		e.ParseMode = "Markdown"
		g.api.Send(e)

	case "reset":
		answer("reset config")
		g.agent.ClearPersist()
		editMenu("♻️ Config direset ke default `config.yaml` saat restart berikutnya.\n\n(Perubahan runtime saat ini masih aktif sampai bot restart.)",
			tgbotapi.NewInlineKeyboardMarkup(tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("◂ Config AI", "cfg:home"),
			)))
	}
}
