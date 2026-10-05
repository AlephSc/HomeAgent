// Package telegram — menu AP (hotspot WiFi): status, start/stop/restart, ubah SSID & channel.
// Target nyata: hostapd.service (AP) + dnsmasq-hotspot.service (DHCP utk wlp2s0b1).
package telegram

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"aleph-agent/internal/tools"
)

const (
	apWiFiIface = "wlp2s0b1"
	apHostapd   = "hostapd"
	apDnsmasq   = "dnsmasq-hotspot"
)

// apMenuKeyboard — tombol submenu AP.
func apMenuKeyboard() tgbotapi.InlineKeyboardMarkup {
	return tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("📊 Status AP", "ap:status"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("▶️ Start AP", "ap:start"),
			tgbotapi.NewInlineKeyboardButtonData("⏹ Stop AP", "ap:stop"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🔄 Restart AP", "ap:restart"),
			tgbotapi.NewInlineKeyboardButtonData("📶 Clients", "ap:clients"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("✏️ Ubah SSID", "ap:ssid"),
			tgbotapi.NewInlineKeyboardButtonData("📡 Ubah Channel", "ap:channel"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🔑 Ubah Sandi WiFi", "ap:pass"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("◂ Kembali", "menu:home"),
		),
	)
}

// apMenuText — teks + keyboard submenu AP.
func (g *Gateway) apMenuText(chatID int64, msgID int) {
	edit := tgbotapi.NewEditMessageTextAndMarkup(chatID, msgID,
		"📡 *Hotspot WiFi (AP)*\nKelola access point \u201cAleph-AP\u201d di "+apWiFiIface+".",
		apMenuKeyboard())
	edit.ParseMode = "Markdown"
	if _, err := g.api.Send(edit); err != nil {
		e2 := tgbotapi.NewEditMessageText(chatID, msgID, "📡 Hotspot WiFi (AP)")
		g.api.Send(e2)
	}
}

// apServiceList — unit yang membentuk AP.
var apUnits = []string{apHostapd, apDnsmasq}

// apStatus — ringkasan kondisi AP lengkap.
func apStatus() string {
	var b strings.Builder
	b.WriteString("📡 *Status Hotspot WiFi*\n\n")

	// SSID + channel dari hostapd.conf (bukan password!)
	ssid, channel, hwmode := apReadConf()
	b.WriteString("• SSID: " + ssid + "\n")
	b.WriteString("• Channel: " + channel + " (" + hwmode + ")\n")
	b.WriteString("• Interface: " + apWiFiIface + "\n\n")

	// status service
	for _, u := range apUnits {
		o, _ := exec.Command("systemctl", "is-active", u).Output()
		st := strings.TrimSpace(string(o))
		icon := "🔴"
		if st == "active" {
			icon = "🟢"
		}
		b.WriteString(fmt.Sprintf("%s %s: %s\n", icon, u, st))
	}

	// IP interface
	o, _ := exec.Command("ip", "-4", "-o", "addr", "show", "dev", apWiFiIface).Output()
	if line := strings.TrimSpace(string(o)); line != "" {
		f := strings.Fields(line)
		if len(f) >= 4 {
			b.WriteString("• IP: " + f[3] + "\n")
		}
	}

	// jumlah client
	n := apClientCount()
	b.WriteString(fmt.Sprintf("• Client terhubung: %d\n", n))
	if n > 0 {
		b.WriteString("\nCek detail: tombol *Clients*.")
	}
	return b.String()
}

// apReadConf — baca ssid/channel/hw_mode dari hostapd.conf (aman: tanpa password).
func apReadConf() (ssid, channel, hwmode string) {
	ssid, channel, hwmode = "?", "?", "?"
	o, err := exec.Command("grep", "-E", "^(ssid|channel|hw_mode)=", "/etc/hostapd/hostapd.conf").Output()
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(o), "\n") {
		kv := strings.SplitN(strings.TrimSpace(line), "=", 2)
		if len(kv) != 2 {
			continue
		}
		switch kv[0] {
		case "ssid":
			ssid = kv[1]
		case "channel":
			channel = kv[1]
		case "hw_mode":
			hwmode = kv[1]
		}
	}
	return
}

// apClientCount — hitung station terhubung.
func apClientCount() int {
	o, err := exec.Command("sh", "-c", "iw dev "+apWiFiIface+" station dump 2>/dev/null | grep -c Station").Output()
	if err != nil {
		return 0
	}
	n, _ := strconv.Atoi(strings.TrimSpace(string(o)))
	return n
}

// apClients — daftar client: MAC, sinyal, uptime.
func apClients() string {
	o, err := exec.Command("iw", "dev", apWiFiIface, "station", "dump").Output()
	if err != nil || strings.TrimSpace(string(o)) == "" {
		return "📶 Tidak ada client yang terhubung."
	}
	var b strings.Builder
	b.WriteString("📶 *Client terhubung:*\n")
	for _, block := range strings.Split(string(o), "Station ") {
		mac, signal, connected := "", "", ""
		for _, line := range strings.Split(block, "\n") {
			line = strings.TrimSpace(line)
			f := strings.Fields(line)
			if mac == "" && len(f) > 0 && strings.Count(f[0], ":") == 5 {
				mac = f[0] // baris pertama tiap station: MAC
			}
			if strings.HasPrefix(line, "signal:") && signal == "" {
				signal = strings.TrimSpace(strings.TrimPrefix(line, "signal:"))
			}
			if strings.HasPrefix(line, "connected time:") {
				connected = strings.TrimSpace(strings.TrimPrefix(line, "connected time:"))
			}
		}
		if mac != "" {
			b.WriteString(fmt.Sprintf("\n• `%s`", mac))
			if signal != "" {
				b.WriteString(" — sinyal " + signal + " dBm")
			}
			if connected != "" {
				b.WriteString(" — " + connected + "s")
			}
		}
	}
	if b.String() == "📶 *Client terhubung:*\n" {
		return "📶 Tidak ada client yang terhubung."
	}
	return b.String()
}

// apControl — start/stop/restart semua unit AP berurutan.
func (g *Gateway) apControl(action string) string {
	if action == "stop" {
		// urutan mundur: DHCP dulu, baru AP
		for _, u := range []string{apDnsmasq, apHostapd} {
			exec.Command("systemctl", "stop", u).Run()
		}
		time.Sleep(1200 * time.Millisecond)
		if permEng := tools.PermEngine(); permEng != nil {
			permEng.Audit("menu-ap", "stop")
		}
		return "⏹ AP dihentikan.\n\n" + apStatus()
	}
	// start/restart: reset-failed dulu (dnsmasq-hotspot sering failed beruntun)
	exec.Command("systemctl", "reset-failed", apDnsmasq).Run()
	for _, u := range apUnits {
		if action == "restart" {
			exec.Command("systemctl", "restart", u).Run()
		} else {
			exec.Command("systemctl", "start", u).Run()
		}
	}
	time.Sleep(1500 * time.Millisecond)
	if permEng := tools.PermEngine(); permEng != nil {
		permEng.Audit("menu-ap", action)
	}
	log.Printf("[telegram] menu AP %s", action)
	return fmt.Sprintf("✅ AP %s.\n\n%s", action, apStatus())
}

// apApplyRestart — setelah konfigurasi diubah, restart AP + verifikasi.
func (g *Gateway) apApplyRestart() string {
	exec.Command("systemctl", "restart", apHostapd).Run()
	exec.Command("systemctl", "reset-failed", apDnsmasq).Run()
	exec.Command("systemctl", "restart", apDnsmasq).Run()
	time.Sleep(1500 * time.Millisecond)
	return apStatus()
}

// apWriteConfField — ubah satu field di hostapd.conf (sed sederhana, aman utk nilai tervalidasi).
func apWriteConfField(field, value string) error {
	o, err := exec.Command("sh", "-c",
		fmt.Sprintf("sed -i 's/^%s=.*/%s=%s/' /etc/hostapd/hostapd.conf", field, field, value)).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%v: %s", err, strings.TrimSpace(string(o)))
	}
	return nil
}

// apSetSsid — ubah SSID lalu restart AP.
func (g *Gateway) apSetSsid(ssid string) string {
	ssid = strings.TrimSpace(ssid)
	if ssid == "" || len(ssid) > 32 {
		return "❌ SSID harus 1-32 karakter."
	}
	if strings.ContainsAny(ssid, "/&;'\"\\") {
		return "❌ SSID memuat karakter yang tidak didukung."
	}
	if err := apWriteConfField("ssid", ssid); err != nil {
		return "❌ Gagal ubah SSID: " + err.Error()
	}
	if permEng := tools.PermEngine(); permEng != nil {
		permEng.Audit("menu-ap", "set ssid "+ssid)
	}
	return "✅ SSID diubah menjadi \u201c" + ssid + "\u201d. Restart AP...\n\n" + g.apApplyRestart()
}

// apSetChannel — ubah channel (1-13 utk 2.4GHz) lalu restart AP.
func (g *Gateway) apSetChannel(chStr string) string {
	ch, err := strconv.Atoi(strings.TrimSpace(chStr))
	if err != nil || ch < 1 || ch > 13 {
		return "❌ Channel harus angka 1-13."
	}
	if err := apWriteConfField("channel", strconv.Itoa(ch)); err != nil {
		return "❌ Gagal ubah channel: " + err.Error()
	}
	if permEng := tools.PermEngine(); permEng != nil {
		permEng.Audit("menu-ap", "set channel "+chStr)
	}
	return fmt.Sprintf("✅ Channel diubah ke %d. Restart AP...\n\n%s", ch, g.apApplyRestart())
}

// apSetPassphrase — ubah wpa_passphrase di hostapd.conf lalu restart AP.
// Aman: sandi TIDAK pernah lewat argv shell (tak terlihat di ps) — file dibaca ke memori,
// barisnya diganti di Go, lalu ditulis ulang lewat stdin `cat > file` (mode 600).
func (g *Gateway) apSetPassphrase(pw string) string {
	pw = strings.TrimSpace(pw)
	if len(pw) < 8 || len(pw) > 63 {
		return "❌ Sandi WPA harus 8-63 karakter."
	}
	if strings.ContainsAny(pw, "\"'\n\r") {
		return "❌ Sandi tidak boleh memuat kutip atau baris baru."
	}
	conf := "/etc/hostapd/hostapd.conf"
	old, err := os.ReadFile(conf)
	if err != nil {
		return "❌ Gagal baca hostapd.conf: " + err.Error()
	}
	lines := strings.Split(string(old), "\n")
	found := false
	for i, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "wpa_passphrase=") {
			lines[i] = "wpa_passphrase=" + pw
			found = true
		}
	}
	if !found {
		return "❌ Baris wpa_passphrase tidak ditemukan di hostapd.conf."
	}
	// tulis: stdin → cat > file (argv hanya "cat > path"; isi aman dari ps & log)
	write := exec.Command("sh", "-c", "cat > "+conf+" && chmod 600 "+conf)
	write.Stdin = strings.NewReader(strings.Join(lines, "\n"))
	if out, err := write.CombinedOutput(); err != nil {
		return "❌ Gagal tulis hostapd.conf: " + err.Error() + " " + strings.TrimSpace(string(out))
	}
	if permEng := tools.PermEngine(); permEng != nil {
		permEng.Audit("menu-ap", "set passphrase (panjang "+strconv.Itoa(len(pw))+")")
	}
	return "✅ Sandi WiFi diubah. Restart AP...\n\n" + g.apApplyRestart()
}

// wifiNet — jaringan hasil scan.
type wifiNet struct {
	ssid    string
	channel int
	signal  int // dBm, negatif (mis. -60)
}

// apScanNetworks — scan tetangga TANPA mematikan AP. Chip Broadcom BCM4313 dalam mode AP
// menolak scan biasa ("Operation not supported") → wajib flag `ap-force`.
// Urutan: trigger (memicu pemindaian ±2-4 dtk) lalu dump (ambil hasil).
func apScanNetworks() ([]wifiNet, error) {
	if out, err := exec.Command("sh", "-c",
		"iw dev "+apWiFiIface+" scan trigger ap-force 2>&1").CombinedOutput(); err != nil {
		return nil, fmt.Errorf("scan trigger: %s", strings.TrimSpace(string(out)))
	}
	time.Sleep(3 * time.Second) // tunggu pemindaian selesai
	cmd := exec.Command("sh", "-c",
		"iw dev "+apWiFiIface+" scan dump ap-force 2>/dev/null | grep -E 'freq:|signal:|SSID:'")
	out, err := cmd.Output()
	if err != nil && len(out) == 0 {
		return nil, err
	}
	var nets []wifiNet
	cur := wifiNet{}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "freq:"):
			if cur.channel != 0 { // BSS baru mulai — simpan yang lama
				nets = append(nets, cur)
				cur = wifiNet{}
			}
			// freq (MHz) → channel 2.4GHz: ch = (freq - 2407) / 5
			f := strings.Fields(line)
			if len(f) >= 2 {
				if mhz, e := strconv.Atoi(f[1]); e == nil && mhz >= 2412 && mhz <= 2472 {
					cur.channel = (mhz - 2407) / 5
				}
			}
		case strings.HasPrefix(line, "signal:"):
			f := strings.Fields(line)
			if len(f) >= 2 {
				if dbm, e := strconv.Atoi(f[1]); e == nil {
					cur.signal = dbm
				} else if fl, e2 := strconv.ParseFloat(f[1], 64); e2 == nil {
					cur.signal = int(fl)
				}
			}
		case strings.HasPrefix(line, "SSID:"):
			cur.ssid = strings.TrimSpace(strings.TrimPrefix(line, "SSID:"))
		}
	}
	if cur.channel != 0 {
		nets = append(nets, cur)
	}
	return nets, nil
}

// apChannelAnalysis — analisa kepadatan channel + rekomendasi.
// Skor: setiap jaringan memberi beban ke channel-nya DAN tetangga overlapping
// (2.4GHz: channel overlap ±4, bobot menurun dengan jarak + kekuatan sinyal).
func apChannelAnalysis() string {
	nets, err := apScanNetworks()
	if err != nil {
		return "❌ Scan gagal: " + err.Error()
	}
	if len(nets) == 0 {
		return "📡 Tidak ada jaringan WiFi lain terdeteksi. Channel mana pun aman."
	}

	// AP sendiri ikut terdeteksi (sinyal paling kuat) — tampil tapi TIDAK dihitung beban
	mySSID, _, _ := apReadConf()

	// beban per channel 1..13
	load := make([]float64, 14)
	for _, n := range nets {
		if n.channel < 1 || n.channel > 13 {
			continue
		}
		if n.ssid == mySSID {
			continue // bukan tetangga
		}
		// kekuatan: sinyal -30(sangat kuat)..-90(sangat lemah) → bobot 1..0.1
		strength := 1.0
		d := -90 - n.signal // -60 → 30
		if d < 0 {
			strength = 1.0
		} else {
			strength = 1.0 - float64(d)/80.0*0.9 // -90dBm → 0.1
			if strength < 0.05 {
				strength = 0.05
			}
		}
		for ch := 1; ch <= 13; ch++ {
			overlap := 5 - abs(ch-n.channel) // 0..4; >0 berarti overlap
			if n.channel == ch {
				load[ch] += 1.0 * strength
			} else if overlap > 0 {
				load[ch] += float64(overlap) / 4.0 * 0.5 * strength
			}
		}
	}

	// channel terbaik: beban terkecil (utk non-AP device kita scan dgn AP sendiri aktif —
	// channel AP sendiri ikut terdeteksi, beri catatan)
	best, bestLoad := 6, 1e9
	for ch := 1; ch <= 13; ch++ {
		if load[ch] < bestLoad {
			bestLoad = load[ch]
			best = ch
		}
	}

	var b strings.Builder
	b.WriteString("📡 *Analisa Channel WiFi (2.4GHz)*\n")
	b.WriteString(fmt.Sprintf("Terdeteksi %d jaringan:\n\n", len(nets)))
	for _, n := range nets {
		name := n.ssid
		if name == "" {
			name = "(tersembunyi)"
		}
		if name == mySSID {
			name += " (AP Anda)"
		}
		b.WriteString(fmt.Sprintf("• `%s` ch %d — %d dBm\n", name, n.channel, n.signal))
	}

	b.WriteString("\n*Kepadatan channel:* (makin sedikit blok = makin lega)\n")
	for ch := 1; ch <= 13; ch++ {
		if load[ch] <= 0 {
			b.WriteString(fmt.Sprintf("ch %2d ▁\n", ch))
			continue
		}
		nBlocks := int(load[ch]*2 + 0.5)
		if nBlocks > 10 {
			nBlocks = 10
		}
		if nBlocks < 1 {
			nBlocks = 1
		}
		bar := strings.Repeat("▇", nBlocks)
		mark := ""
		if ch == best {
			mark = " ← terbaik"
		}
		b.WriteString(fmt.Sprintf("ch %2d %s%s\n", ch, bar, mark))
	}

	// rekomendasi non-overlap klasik: 1 / 6 / 11
	var bestStd int
	bestStdLoad := 1e9
	for _, ch := range []int{1, 6, 11} {
		if load[ch] < bestStdLoad {
			bestStdLoad = load[ch]
			bestStd = ch
		}
	}
	b.WriteString(fmt.Sprintf("\n💡 *Rekomendasi:* channel **%d** paling lega.\n", best))
	if bestStd != best {
		b.WriteString(fmt.Sprintf("   Dari trio standar (1/6/11): channel **%d**.\n", bestStd))
	}
	b.WriteString("   Ubah via tombol *📡 Ubah Channel*.")
	return b.String()
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// handleAPCallback — data "ap:<aksi>".
func (g *Gateway) handleAPCallback(cbID string, chatID int64, msgID int, data string) {
	switch strings.TrimPrefix(data, "ap:") {
	case "status":
		g.api.Request(tgbotapi.NewCallback(cbID, "mengambil status..."))
		g.sendWithKeyboard(chatID, msgID, apStatus(), apMenuKeyboard())
	case "start", "restart":
		g.api.Request(tgbotapi.NewCallback(cbID, "memproses..."))
		out := g.apControl(strings.TrimPrefix(data, "ap:"))
		g.sendWithKeyboard(chatID, msgID, out, apMenuKeyboard())
	case "stop":
		g.api.Request(tgbotapi.NewCallback(cbID, "menghentikan..."))
		out := g.apControl("stop")
		g.sendWithKeyboard(chatID, msgID, out, apMenuKeyboard())
	case "clients":
		g.api.Request(tgbotapi.NewCallback(cbID, "menghitung..."))
		g.sendWithKeyboard(chatID, msgID, apClients(), apMenuKeyboard())
	case "scan":
		g.api.Request(tgbotapi.NewCallback(cbID, "memindai (2-4 dtk)..."))
		go func() {
			out := apChannelAnalysis()
			g.sendWithKeyboard(chatID, msgID, out, apMenuKeyboard())
		}()
	case "ssid":
		g.api.Request(tgbotapi.NewCallback(cbID, "ketik SSID baru"))
		g.pendingMu.Lock()
		g.pendingAct[chatID] = &pendingAct{Kind: "ap-ssid", MsgID: msgID}
		g.pendingMu.Unlock()
		g.editText(chatID, msgID,
			"✏️ *Ubah SSID*\nKetik nama WiFi baru (1-32 karakter), contoh:\n`Aleph-AP2`\nKetik /batal untuk membatalkan.")
	case "channel":
		g.api.Request(tgbotapi.NewCallback(cbID, "ketik channel baru"))
		g.pendingMu.Lock()
		g.pendingAct[chatID] = &pendingAct{Kind: "ap-channel", MsgID: msgID}
		g.pendingMu.Unlock()
		g.editText(chatID, msgID,
			"📡 *Ubah Channel*\nKetik angka channel 1-13 (2.4GHz), contoh:\n`11`\nKetik /batal untuk membatalkan.")
	case "pass":
		g.api.Request(tgbotapi.NewCallback(cbID, "ketik sandi baru"))
		g.pendingMu.Lock()
		g.pendingAct[chatID] = &pendingAct{Kind: "ap-pass", MsgID: msgID}
		g.pendingMu.Unlock()
		g.editText(chatID, msgID,
			"🔑 *Ubah Sandi WiFi*\nKetik sandi baru (min. 8 karakter).\n⚠️ Sandi TIDAK disimpan ke log dan TIDAK dikirim balik.\nPesan yang berisi sandi akan tetap ada di riwayat chat Telegram Anda — hapus manual bila perlu.\nKetik /batal untuk membatalkan.")
	}
}

// sendWithKeyboard — edit pesan jadi teks+keyboard (fallback plain bila markdown gagal).
func (g *Gateway) sendWithKeyboard(chatID int64, msgID int, text string, kb tgbotapi.InlineKeyboardMarkup) {
	edit := tgbotapi.NewEditMessageTextAndMarkup(chatID, msgID, text, kb)
	edit.ParseMode = "Markdown"
	if _, err := g.api.Send(edit); err != nil {
		e2 := tgbotapi.NewEditMessageTextAndMarkup(chatID, msgID, text, kb)
		g.api.Send(e2)
	}
}

// consumePendingAct — dukung kind baru "ap-ssid" / "ap-channel" (dipanggil dari telegram.go).
func (g *Gateway) apConsumeInput(chatID int64, act *pendingAct, text string) bool {
	switch act.Kind {
	case "ap-ssid":
		out := g.apSetSsid(text)
		g.pendingMu.Lock()
		delete(g.pendingAct, chatID)
		g.pendingMu.Unlock()
		g.reply(chatID, out)
		return true
	case "ap-channel":
		out := g.apSetChannel(text)
		g.pendingMu.Lock()
		delete(g.pendingAct, chatID)
		g.pendingMu.Unlock()
		g.reply(chatID, out)
		return true
	case "ap-pass":
		out := g.apSetPassphrase(text)
		g.pendingMu.Lock()
		delete(g.pendingAct, chatID)
		g.pendingMu.Unlock()
		// hapus juga pesan yang berisi sandi dari chat (semampunya)
		g.reply(chatID, out)
		return true
	}
	return false
}
