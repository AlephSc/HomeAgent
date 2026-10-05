// Package telegram — Fase E: inline keyboard menu + callback handler.
package telegram

import (
	"fmt"
	"log"
	"os/exec"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"aleph-agent/internal/perm"
	"aleph-agent/internal/tools"
)

// serviceList — shortcut service (label tampil, unit internal).
var serviceList = []struct {
	Label string
	Unit  string
}{
	{"9Router", "9router"},
	{"Tailscale", "tailscaled"},
	{"Docker", "docker"},
	{"AdGuard Home", "docker:adguardhome"},
	{"CasaOS", "casaos"},
	{"dnsmasq", "dnsmasq"},
	{"aleph-agent", "aleph-agent"},
}

// mainMenuKeyboard — menu utama.
func mainMenuKeyboard() tgbotapi.InlineKeyboardMarkup {
	return tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("📊 Status", "menu:status"),
			tgbotapi.NewInlineKeyboardButtonData("🏓 Ping", "menu:ping"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("⚡ Speedtest", "menu:speedtest"),
			tgbotapi.NewInlineKeyboardButtonData("💻 Run Command", "menu:run"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🔧 Services ▸", "menu:svc"),
			tgbotapi.NewInlineKeyboardButtonData("📡 AP WiFi ▸", "menu:ap"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🗂 Notes ▸", "note:home"),
			tgbotapi.NewInlineKeyboardButtonData("⚙️ Config AI ▸", "cfg:home"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("📊 Usage Resource", "menu:usage"),
		),
	)
}

// svcMenuKeyboard — submenu service: baris unit, baris aksi generik.
func svcMenuKeyboard() tgbotapi.InlineKeyboardMarkup {
	var grid [][]tgbotapi.InlineKeyboardButton
	for i := 0; i < len(serviceList); i += 2 {
		row := []tgbotapi.InlineKeyboardButton{}
		for j := i; j < i+2 && j < len(serviceList); j++ {
			row = append(row, tgbotapi.NewInlineKeyboardButtonData(serviceList[j].Label, "svcq:"+serviceList[j].Unit))
		}
		grid = append(grid, row)
	}
	// G5: service custom milik user (dengan 📌 penanda + status pantau)
	if svcMgr != nil {
		for _, cs := range svcMgr.List() {
			label := "📌 " + cs.Unit
			if cs.Watch {
				label += " 🔔"
			}
			grid = append(grid, tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData(label, "svcq:"+cs.Unit),
			))
		}
		grid = append(grid, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("➕ Tambah Service", "svcadd"),
		))
	}
	grid = append(grid, tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData("◂ Kembali", "menu:home"),
	))
	return tgbotapi.NewInlineKeyboardMarkup(grid...)
}

// svcActKeyboard — aksi utk satu unit: Status / Start / Stop / Restart (+ custom: enable/disable/pantau/hapus).
func svcActKeyboard(unit string) tgbotapi.InlineKeyboardMarkup {
	custom := false
	var watch bool
	if svcMgr != nil {
		for _, cs := range svcMgr.List() {
			if cs.Unit == unit {
				custom = true
				watch = cs.Watch
				break
			}
		}
	}
	rows := [][]tgbotapi.InlineKeyboardButton{
		{tgbotapi.NewInlineKeyboardButtonData("📊 Status", "svc:status:"+unit)},
		{tgbotapi.NewInlineKeyboardButtonData("▶️ Start", "svc:start:"+unit),
			tgbotapi.NewInlineKeyboardButtonData("⏹ Stop", "svc:stop:"+unit)},
		{tgbotapi.NewInlineKeyboardButtonData("🔄 Restart", "svc:restart:"+unit)},
	}
	if custom && !strings.HasPrefix(unit, "docker:") {
		watchLabel := "🔔 Pantau: ON"
		if !watch {
			watchLabel = "🔕 Pantau: OFF"
		}
		rows = append(rows,
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("🟢 Enable (auto-start)", "svc:enable:"+unit),
				tgbotapi.NewInlineKeyboardButtonData("🔴 Disable", "svc:disable:"+unit),
			),
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData(watchLabel, "svc:watch:"+unit),
				tgbotapi.NewInlineKeyboardButtonData("🗑 Hapus dari daftar", "svc:remove:"+unit),
			),
		)
	} else if custom {
		watchLabel := "🔔 Pantau: ON"
		if !watch {
			watchLabel = "🔕 Pantau: OFF"
		}
		rows = append(rows,
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData(watchLabel, "svc:watch:"+unit),
				tgbotapi.NewInlineKeyboardButtonData("🗑 Hapus dari daftar", "svc:remove:"+unit),
			),
		)
	}
	rows = append(rows, tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData("◂ Kembali", "menu:svc"),
	))
	return tgbotapi.NewInlineKeyboardMarkup(rows...)
}

// handleCallback menangani semua tombol inline.
func (g *Gateway) handleCallback(cb *tgbotapi.CallbackQuery) {
	chatID := cb.Message.Chat.ID
	msgID := cb.Message.MessageID
	data := cb.Data
	log.Printf("[callback] chat=%d msg=%d data=%q", chatID, msgID, data)

	answer := func(text string) {
		g.api.Request(tgbotapi.NewCallback(cb.ID, text))
	}

	switch {
	case data == "menu:home":
		edit := tgbotapi.NewEditMessageTextAndMarkup(chatID, msgID,
			"🏠 *Menu aleph-agent*\nPilih aksi:", mainMenuKeyboard())
		edit.ParseMode = "Markdown"
		g.api.Send(edit)
		answer("menu utama")

	case data == "menu:status":
		answer("mengambil status...")
		out := g.fast.Handle("/status")
		edit := tgbotapi.NewEditMessageTextAndMarkup(chatID, msgID, out, mainMenuKeyboard())
		edit.ParseMode = "Markdown"
		if _, err := g.api.Send(edit); err != nil { // markdown bisa gagal → plain
			e2 := tgbotapi.NewEditMessageText(chatID, msgID, out)
			g.api.Send(e2)
		}

	case data == "menu:ping":
		answer("ping...")
		out := g.cmdPingButtons()
		edit := tgbotapi.NewEditMessageTextAndMarkup(chatID, msgID, out, mainMenuKeyboard())
		g.api.Send(edit)

	case data == "menu:speedtest":
		answer("speedtest mulai — tunggu ±30 dtk")
		go func() {
			out := g.cmdSpeedtest()
			edit := tgbotapi.NewEditMessageTextAndMarkup(chatID, msgID, out, mainMenuKeyboard())
			g.api.Send(edit)
		}()

	case data == "menu:run":
		answer("ketik command-nya di chat")
		g.pendingMu.Lock()
		g.pendingAct[chatID] = &pendingAct{Kind: "run", MsgID: msgID}
		g.pendingMu.Unlock()
		e := tgbotapi.NewEditMessageText(chatID, msgID,
			"💻 *Run Command*\nKetik command yang mau dijalankan (contoh: `df -h`).\nKetik /batal untuk membatalkan.")
		e.ParseMode = "Markdown"
		g.api.Send(e)

	case data == "menu:svc":
		edit := tgbotapi.NewEditMessageTextAndMarkup(chatID, msgID, "🔧 *Pilih service:*", svcMenuKeyboard())
		edit.ParseMode = "Markdown"
		g.api.Send(edit)
		answer("submenu service")

	case data == "menu:ap":
		answer("menu AP")
		g.apMenuText(chatID, msgID)

	case strings.HasPrefix(data, "ap:"):
		answer("AP...")
		g.handleAPCallback(cb.ID, chatID, msgID, data)

	case strings.HasPrefix(data, "svcq:"):
		unit := strings.TrimPrefix(data, "svcq:")
		answer(unit)
		edit := tgbotapi.NewEditMessageTextAndMarkup(chatID, msgID,
			fmt.Sprintf("🔧 *%s*\nStatus saat ini diambil via tombol Status.", unit), svcActKeyboard(unit))
		edit.ParseMode = "Markdown"
		g.api.Send(edit)

	case strings.HasPrefix(data, "perm:"):
		// F-perm: keputusan izin command (once/always/session/deny)
		parts := strings.Split(strings.TrimPrefix(data, "perm:"), ":")
		if len(parts) != 2 {
			answer("data tombol tidak valid")
			return
		}
		var reqID int
		fmt.Sscanf(parts[0], "%d", &reqID)
		answer("keputusan dicatat")
		g.handlePermCallback(cb.ID, chatID, msgID, reqID, parts[1])

	case strings.HasPrefix(data, "ask:"):
		// G3: jawaban pertanyaan ask_user
		answer("jawaban dicatat")
		g.handleAskCallback(cb.ID, chatID, msgID, data)

	case data == "menu:usage":
		// G12: Usage Resource — semua app (host + docker + top proses)
		answer("cek usage…")
		text := g.usageText()
		e := tgbotapi.NewEditMessageText(chatID, msgID, text)
		e.ParseMode = "Markdown"
		if _, err := g.api.Send(e); err != nil {
			// fallback tanpa parse mode (tabel monospace bisa gagal Markdown)
			e2 := tgbotapi.NewEditMessageText(chatID, msgID, text)
			g.api.Send(e2)
		}

	case strings.HasPrefix(data, "note:"):
		g.handleNoteCallback(answer, chatID, msgID, data)
		answer("notes")

	case strings.HasPrefix(data, "cfg:"):
		// G7a: menu Config AI
		g.handleCfgCallback(answer, chatID, msgID, data)

	case data == "svcadd":
		// G5: user mau menambah service — minta ketik nama unit
		answer("ketik nama unit")
		g.pendingMu.Lock()
		g.pendingAct[chatID] = &pendingAct{Kind: "svc-add"}
		g.pendingMu.Unlock()
		e := tgbotapi.NewEditMessageText(chatID, msgID,
			"➕ *Tambah Service\n\nKetik nama unit systemd* (mis. `nginx` atau `docker:redis`), lalu kirim.\n/batal untuk membatalkan.")
		g.api.Send(e)

	case strings.HasPrefix(data, "svc:"):
		parts := strings.SplitN(strings.TrimPrefix(data, "svc:"), ":", 2)
		if len(parts) != 2 {
			answer("data tombol tidak valid")
			return
		}
		action, unit := parts[0], parts[1]

		// G5: aksi manajemen daftar custom (bukan kontrol systemd)
		switch action {
		case "watch":
			answer("diubah")
			if svcMgr != nil {
				watch := false
				for _, cs := range svcMgr.List() {
					if cs.Unit == unit {
						watch = !cs.Watch
						break
					}
				}
				_ = svcMgr.SetWatch(unit, watch)
			}
			txt := fmt.Sprintf("📌 %s — pantau diubah.\n\n📊 Status:", unit)
			if st, err := isActive(unit); err == nil {
				txt += " " + st
			}
			e := tgbotapi.NewEditMessageTextAndMarkup(chatID, msgID, txt, svcActKeyboard(unit))
			g.api.Send(e)
			return
		case "remove":
			answer("dihapus")
			if svcMgr != nil {
				_ = svcMgr.Remove(unit)
			}
			e := tgbotapi.NewEditMessageTextAndMarkup(chatID, msgID, "🗑 "+unit+" dihapus dari daftar.", svcMenuKeyboard())
			g.api.Send(e)
			return
		case "enable", "disable":
			// jalankan + verifikasi is-enabled
			answer(action + "...")
			go func() {
				var out string
				if strings.HasPrefix(unit, "docker:") {
					out = "❌ enable/disable hanya untuk service systemd (bukan docker)."
				} else {
					var sub string
					if action == "enable" {
						sub = "enable --now"
					} else {
						sub = "disable --now"
					}
					cmd := exec.Command("systemctl", strings.Fields(sub)[0], "--now", unit)
					if o, err := cmd.CombinedOutput(); err != nil {
						out = fmt.Sprintf("❌ %s %s gagal: %v\n%s", action, unit, err, strings.TrimSpace(string(o)))
					} else {
						time.Sleep(800 * time.Millisecond)
						en, _ := execOutput("systemctl", "is-enabled", unit)
						out = fmt.Sprintf("✅ %s %s OK — auto-start: %s", action, unit, en)
					}
				}
				if permEng := tools.PermEngine(); permEng != nil {
					permEng.Audit("service", action+" "+unit)
				}
				e := tgbotapi.NewEditMessageTextAndMarkup(chatID, msgID, out, svcActKeyboard(unit))
				g.api.Send(e)
			}()
			return
		}
		// Cek tier dulu — kritis wajib konfirmasi teks
		if permEng := tools.PermEngine(); permEng != nil {
			tier := permEng.ClassifyService(action, unit)
			switch tier {
			case perm.Confirm:
				answer("butuh konfirmasi")
				g.pendingMu.Lock()
				g.pendingAct[chatID] = &pendingAct{Kind: "svc", Unit: unit, Action: action, MsgID: msgID}
				g.pendingMu.Unlock()
				e := tgbotapi.NewEditMessageText(chatID, msgID,
					fmt.Sprintf("⚠️ *%s %s* adalah aksi KRITIS.\n\nBalas *ya* untuk lanjut, /batal untuk tolak.", action, unit))
				e.ParseMode = "Markdown"
				g.api.Send(e)
				return
			case perm.Denied:
				answer("ditolak")
				e := tgbotapi.NewEditMessageText(chatID, msgID, "⛔ Aksi ini tidak diizinkan.")
				g.api.Send(e)
				return
			default:
				answer("menjalankan " + action + "...")
			}
		}
		go func() {
			out := g.runServiceAction(action, unit)
			edit := tgbotapi.NewEditMessageTextAndMarkup(chatID, msgID, out, svcActKeyboard(unit))
			g.api.Send(edit)
		}()
	}
}

// runServiceAction eksekusi aksi service via docker/systemctl langsung (dari tombol).
func (g *Gateway) runServiceAction(action, unit string) string {
	var out string
	var err error
	if strings.HasPrefix(unit, "docker:") {
		name := strings.TrimPrefix(unit, "docker:")
		if action == "status" {
			o, e := runOutput(0, "docker", "inspect", "-f", "{{.State.Status}}", name)
			if e != nil {
				return fmt.Sprintf("❌ container %s tidak ditemukan", name)
			}
			return fmt.Sprintf("📊 docker:%s: %s", name, strings.TrimSpace(string(o)))
		}
		_, err = runOutput(0, "docker", action, name)
	} else {
		if action == "status" {
			o, _ := runOutput(0, "systemctl", "is-active", unit)
			return fmt.Sprintf("📊 %s: %s", unit, strings.TrimSpace(o))
		}
		_, err = runOutput(0, "systemctl", action, unit)
	}
	if err != nil {
		out = fmt.Sprintf("❌ %s %s gagal: %v", action, unit, err)
	} else {
		time.Sleep(1200 * time.Millisecond)
		// verifikasi
		var st string
		if strings.HasPrefix(unit, "docker:") {
			o, _ := runOutput(0, "docker", "inspect", "-f", "{{.State.Status}}", strings.TrimPrefix(unit, "docker:"))
			st = strings.TrimSpace(o)
		} else {
			o, _ := runOutput(0, "systemctl", "is-active", unit)
			st = strings.TrimSpace(o)
		}
		out = fmt.Sprintf("✅ %s %s OK — status: %s", action, unit, st)
	}
	if permEng := tools.PermEngine(); permEng != nil {
		permEng.Audit("button-svc", action+" "+unit)
	}
	log.Printf("[telegram] button %s %s", action, unit)
	return out
}

// cmdPingButtons — ping google + cloudflare untuk tombol.
func (g *Gateway) cmdPingButtons() string {
	var b strings.Builder
	b.WriteString("🏓 *Ping:*\n")
	for _, host := range []string{"google.com", "cloudflare.com"} {
		out, _ := exec.Command("ping", "-c", "3", "-W", "3", host).CombinedOutput()
		lat, loss := parsePing(string(out))
		b.WriteString(fmt.Sprintf("• %s — %s\n", host, lat))
		_ = loss
	}
	return b.String()
}

// parsePing ekstrak avg latency & loss dari output ping.
func parsePing(out string) (string, string) {
	avg, loss := "timeout", "?"
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "avg") && strings.Contains(line, "=") {
			// rtt min/avg/max/mdev
			fields := strings.Fields(line)
			if len(fields) >= 4 {
				avg = fields[len(fields)-2] + " ms"
			}
		}
		if strings.Contains(line, "packet loss") {
			fields := strings.Fields(line)
			for i, f := range fields {
				if strings.Contains(f, "loss") && i > 0 {
					loss = fields[i-1]
				}
			}
		}
	}
	return avg, loss
}

// cmdSpeedtest — jalankan speedtest-cli (atau fallback curl ringan).
func (g *Gateway) cmdSpeedtest() string {
	if _, err := exec.LookPath("speedtest-cli"); err == nil {
		out, err := runOutput(120*time.Second, "speedtest-cli", "--simple")
		if err == nil {
			return "⚡ *Speedtest*\n```\n" + strings.TrimSpace(string(out)) + "\n```"
		}
	}
	// fallback: ukur latency ke cloudflare (ringan, tanpa download besar)
	start := time.Now()
	if _, err := runOutput(20*time.Second, "curl", "-s", "-o", "/dev/null", "-m", "10", "https://speed.cloudflare.com/__down?bytes=1000000"); err != nil {
		return "⚡ Speedtest: speedtest-cli tidak terpasang dan fallback gagal.\nInstall: `apt install speedtest-cli`"
	}
	d := time.Since(start).Round(time.Millisecond)
	mbps := float64(1000000*8) / d.Seconds() / 1e6
	return fmt.Sprintf("⚡ *Speedtest (fallback cloudflare 1MB)*\n• ±%.1f Mbps (estimasi kasar)\n\nUntuk hasil akurat: `apt install speedtest-cli`", mbps)
}
