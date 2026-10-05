package telegram

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"aleph-agent/internal/extract"
	"aleph-agent/internal/memory"
)

// filesRoot — direktori penyimpanan file upload (di-set dari main.go).
// Struktur: <root>/chat_<id>/<nama-file>  dan  <root>/inbox/  (upload luar via web/scp)
var filesRoot string

// SetFilesRoot dipanggil main.go: lokasi penyimpanan file & inbox.
func SetFilesRoot(dir string) {
	filesRoot = dir
	if err := os.MkdirAll(filepath.Join(dir, "inbox"), 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "WARN: files root gagal: %v\n", err)
	}
}

// FilesRoot dipakai komponen lain (inbox watcher).
func FilesRoot() string { return filesRoot }

// SetStore — main.go menyuntikkan memory store (untuk metadata upload).
func (g *Gateway) SetStore(s *memory.Store) { g.store = s }

// handleDocument — G1: user kirim file → unduh streaming ke disk → antri ekstraksi.
func (g *Gateway) handleDocument(msg *tgbotapi.Message) {
	if !g.cfg.IsTelegramAdmin(msg.From.ID) {
		return
	}
	chatID := msg.Chat.ID
	doc := msg.Document
	if doc == nil || doc.FileID == "" {
		return
	}
	name := sanitizeFileName(doc.FileName)
	if name == "" {
		fid := doc.FileID
		if len(fid) > 8 {
			fid = fid[:8]
		}
		name = "file_" + fid
	}
	// konteks reply: file dikirim sebagai balasan → sertakan pesan yang dibalas
	caption := msg.Caption
	if rc := replyContext(msg); rc != "" {
		caption = rc + "\n\n" + caption
	}

	if !extract.IsSupported(name) {
		g.reply(chatID, fmt.Sprintf("📎 %s — tipe file ini belum bisa saya baca. Didukung: PDF, DOCX, TXT, MD, CSV, JSON, dan kode.", name))
		return
	}

	g.reply(chatID, fmt.Sprintf("📥 Menerima %s…", name))

	// unduh streaming — file TIDAK dimuat utuh di RAM
	fileCfg := tgbotapi.FileConfig{FileID: doc.FileID}
	file, err := g.api.GetFile(fileCfg)
	if err != nil {
		g.reply(chatID, "📎 Gagal mengambil info file dari Telegram: "+err.Error())
		return
	}
	url := "https://api.telegram.org/file/bot" + g.api.Token + "/" + file.FilePath
	client := &http.Client{Timeout: 10 * time.Minute}
	resp, err := client.Get(url)
	if err != nil {
		g.reply(chatID, "📎 Gagal mengunduh file: "+err.Error())
		return
	}
	defer resp.Body.Close()

	// cek ukuran dari header — hindari mengunduh file raksasa percuma
	if resp.ContentLength > 20<<20 {
		g.reply(chatID, fmt.Sprintf("📎 %s (%s) melebihi batas unduhan Telegram (20 MB).\n💡 Untuk file besar: upload lewat web UI atau taruh di folder inbox server — nanti saya deteksi otomatis.", name, memory.HumanSize(resp.ContentLength)))
		return
	}

	dir := filepath.Join(filesRoot, fmt.Sprintf("chat_%d", chatID))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		g.reply(chatID, "📎 Gagal menyiapkan penyimpanan: "+err.Error())
		return
	}
	dst := uniquePath(filepath.Join(dir, name))
	out, err := os.Create(dst)
	if err != nil {
		g.reply(chatID, "📎 Gagal membuat file: "+err.Error())
		return
	}
	size, err := io.Copy(out, resp.Body) // streaming — RAM aman
	closeErr := out.Close()
	if err != nil || closeErr != nil {
		g.reply(chatID, "📎 Gagal menyimpan file ke disk.")
		return
	}
	// Telegram bot API: maksimum 20 MB unduhan file
	if size >= 20<<20 {
		os.Remove(dst)
		g.reply(chatID, fmt.Sprintf("📎 %s (%s) melebihi batas unduhan Telegram (20 MB).\n💡 Untuk file besar: upload lewat web UI (http://100.72.168.93:8080, via Tailscale) atau taruh langsung di folder inbox server — nanti saya deteksi otomatis.", name, memory.HumanSize(size)))
		return
	}

	if g.store == nil {
		g.reply(chatID, "📎 File tersimpan di " + dst + " (database belum siap, ekstraksi dilewati).")
		return
	}
	id, err := g.store.AddUpload(chatID, filepath.Base(dst), dst, size)
	if err != nil {
		g.reply(chatID, "📎 File tersimpan, tapi gagal mendaftar ekstraksi: "+err.Error())
		return
	}
	g.queueExtract(id)
	g.reply(chatID, fmt.Sprintf("📄 %s (%s) diterima — saya baca pelan-pelan tanpa memberatkan server. Saya kabari saat selesai.", filepath.Base(dst), memory.HumanSize(size)))

	// kalau ada caption/instruksi, sampaikan ke agent sebagai tugas
	if caption != "" && g.agent != nil {
		go g.handleAgent(chatID, msg.From.FirstName, caption)
	}
}

// uniquePath — hindari menimpa: file.txt → file (2).txt
func uniquePath(p string) string {
	if _, err := os.Stat(p); os.IsNotExist(err) {
		return p
	}
	ext := filepath.Ext(p)
	base := strings.TrimSuffix(p, ext)
	for i := 2; ; i++ {
		np := fmt.Sprintf("%s (%d)%s", base, i, ext)
		if _, err := os.Stat(np); os.IsNotExist(err) {
			return np
		}
	}
}

// queueExtract — antri ekstraksi (worker tunggal, ramah CPU).
func (g *Gateway) queueExtract(uploadID int64) {
	select {
	case g.extractQueue <- uploadID:
	default:
		// antre penuh — jalankan goroutine pemantau ringan agar tidak blokir
		go func() { g.extractQueue <- uploadID }()
	}
}

// extractWorker — SATU worker: ekstraksi berurutan, tidak pernah paralel (N2600).
func (g *Gateway) extractWorker() {
	for id := range g.extractQueue {
		g.runExtract(id)
	}
}

// runExtract ekstraksi satu upload + lapor progres ke chat.
func (g *Gateway) runExtract(id int64) {
	if g.store == nil {
		return
	}
	u, err := g.store.GetUpload(id)
	if err != nil || u == nil {
		return
	}
	g.store.UpdateUploadProgress(id, u.TextPath, u.PagesDone, u.Pages, "proses")

	startPage := u.PagesDone
	res, err := extract.Extract(u.Path, 0)
	if err != nil {
		g.store.FailUpload(id, err.Error())
		g.sendDirectChat(u.ChatID, fmt.Sprintf("❌ Gagal mengekstrak %s: %v", u.Name, err))
		return
	}

	g.store.UpdateUploadProgress(id, res.TextPath, res.PagesDone, res.Pages, "siap")

	// laporkan hanya jika: mulai dari nol (ekstraksi baru) ATAU progres berarti
	if startPage == 0 || res.PagesDone != startPage {
		var b strings.Builder
		if res.Pages > 0 {
			fmt.Fprintf(&b, "✅ %s siap — %d halaman diekstrak", u.Name, res.PagesDone)
		} else {
			fmt.Fprintf(&b, "✅ %s siap dibaca", u.Name)
		}
		fmt.Fprintf(&b, " (%s teks).\nTanya saja, mis: \"ringkas file %s\"", memory.HumanSize(res.Chars), strings.TrimSuffix(u.Name, filepath.Ext(u.Name)))
		g.sendDirectChat(u.ChatID, b.String())
	}
}

// handleFiles — /files: daftar file upload (instan, tanpa LLM).
func (g *Gateway) handleFiles() string {
	if g.store == nil {
		return "Database belum siap."
	}
	list := g.store.ListUploads(15)
	if len(list) == 0 {
		return "📎 Belum ada file. Kirim file (PDF/DOCX/TXT) langsung ke chat ini, atau taruh di inbox server untuk file besar."
	}
	var b strings.Builder
	b.WriteString("📎 File upload terbaru:\n")
	for _, u := range list {
		fmt.Fprintf(&b, "• %s — %s, %s", u.Name, memory.HumanSize(u.Size), u.Status)
		if u.Pages > 0 {
			fmt.Fprintf(&b, " (hal %d/%d)", u.PagesDone, u.Pages)
		}
		b.WriteString("\n")
	}
	b.WriteString("\nTanya agent: \"ringkas file <nama>\" atau \"baca file <nama> halaman 5\"")
	return b.String()
}

// sendDirectChat kirim pesan ke chat tertentu (perlu karena reply() butuh msg).
func (g *Gateway) sendDirectChat(chatID int64, text string) {
	m := tgbotapi.NewMessage(chatID, text)
	if _, err := g.api.Send(m); err != nil {
		m2 := tgbotapi.NewMessage(chatID, text)
		g.api.Send(m2)
	}
}

// watchInbox — pantau folder inbox: file baru → daftar + ekstraksi + kabari admin.
// File besar (PDF materi >20MB) masuk lewat sini: scp/SFTP/web UI → inbox.
func (g *Gateway) watchInbox() {
	inbox := filepath.Join(filesRoot, "inbox")
	os.MkdirAll(inbox, 0o755)
	seen := map[string]int64{} // nama → ukuran terakhir (stabil = selesai disalin)
	t := time.NewTicker(15 * time.Second)
	defer t.Stop()
	for range t.C {
		entries, err := os.ReadDir(inbox)
		if err != nil {
			continue
		}
		// anti memory-leak: map seen dibatasi (file yang tak pernah stabil dibuang dari pelacakan)
		if len(seen) > 256 {
			seen = map[string]int64{}
		}
		for _, e := range entries {
			if e.IsDir() || strings.HasPrefix(e.Name(), ".") {
				continue
			}
			full := filepath.Join(inbox, e.Name())
			st, err := os.Stat(full)
			if err != nil || st.IsDir() {
				continue
			}
			if prev, ok := seen[e.Name()]; ok && prev == st.Size() {
				// ukuran stabil 2x berturut → selesai disalin
				delete(seen, e.Name())
				g.adoptInboxFile(full, st.Size())
			} else {
				seen[e.Name()] = st.Size()
			}
		}
	}
}

// adoptInboxFile — file inbox siap → daftar + ekstraksi + kabari.
func (g *Gateway) adoptInboxFile(path string, size int64) {
	name := filepath.Base(path)
	if !extract.IsSupported(name) {
		// bukan dokumen teks — abaikan tanpa drama (bisa berupa file lain untuk G4)
		return
	}
	if g.store == nil {
		return
	}
	admin := g.firstAdminID()
	if admin == 0 {
		return
	}
	// pindahkan ke folder chat admin supaya watcher tidak mengadopsi ulang
	dir := filepath.Join(filesRoot, fmt.Sprintf("chat_%d", admin))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	dst := uniquePath(filepath.Join(dir, name))
	if err := os.Rename(path, dst); err != nil {
		// lintas-filesystem? fallback salin+hapus
		if data, err2 := os.ReadFile(path); err2 == nil {
			if err3 := os.WriteFile(dst, data, 0o644); err3 == nil {
				os.Remove(path)
			} else {
				return
			}
		} else {
			return
		}
	}
	name = filepath.Base(dst)
	path = dst
	id, err := g.store.AddUpload(admin, name, path, size)
	if err != nil {
		return
	}
	g.sendDirectChat(admin, fmt.Sprintf("📥 File baru di inbox: %s (%s) — mulai saya baca.", name, memory.HumanSize(size)))
	g.queueExtract(id)
}

// firstAdminID — chat admin pertama (tujuan notifikasi).
func (g *Gateway) firstAdminID() int64 {
	if len(g.cfg.Admins.TelegramIDs) > 0 {
		return g.cfg.Admins.TelegramIDs[0]
	}
	return 0
}

// resumePendingExtracts — saat bot start: lanjutkan ekstraksi yang tertunda.
func (g *Gateway) resumePendingExtracts() {
	if g.store == nil {
		return
	}
	for _, u := range g.store.PickResume() {
		if _, err := os.Stat(u.Path); err == nil {
			g.queueExtract(u.ID)
		} else {
			g.store.FailUpload(u.ID, "file sumber hilang")
		}
	}
}

// sanitizeFileName — buang path traversal & karakter berbahaya dari nama file.
func sanitizeFileName(name string) string {
	name = filepath.Base(strings.ReplaceAll(name, "\\", "/")) // buang direktori
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case strings.ContainsRune(" ._-()[]", r):
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	out := strings.TrimSpace(b.String())
	out = strings.TrimLeft(out, ".") // sembunyikan file dot
	if len(out) > 120 {
		out = out[:120]
	}
	return out
}
