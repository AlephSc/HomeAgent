// Package fastpath — slash commands tanpa LLM (instan, 0 token)
package fastpath

import (
	"fmt"
	"strings"
	"time"

	"aleph-agent/internal/config"
	"aleph-agent/internal/server"
	"aleph-agent/internal/tools"
)

var version = "0.1.0-F1"

// Handler menangani semua slash command.
type Handler struct {
	cfg *config.Config
	srv *server.Server
	mem MemoryProvider // F5 (boleh nil)
}

// New membuat handler.
func New(cfg *config.Config, srv *server.Server) *Handler {
	return &Handler{cfg: cfg, srv: srv}
}

// SetMemory memasang memory provider (F5) untuk /memory & /lesson.
func (h *Handler) SetMemory(m MemoryProvider) { h.mem = m }

// Handle mengeksekusi command; mengembalikan teks balasan.
// isAuthorized sudah dicek gateway sebelum memanggil sini.
func (h *Handler) Handle(text string) string {
	cmd, _ := splitCommand(text)
	switch cmd {
	case "/ping":
		return fmt.Sprintf("pong — %s v%s, %s", h.cfg.Server.Name, version, time.Now().Format("15:04:05"))
	case "/status":
		m, err := h.srv.Collect()
		if err != nil {
			return "⚠️ gagal kumpulkan metrik: " + err.Error()
		}
		return "📊 Status " + h.cfg.Server.Name + "\n" + m.Summary()
	case "/services":
		return h.cmdServices()
	case "/audit":
		return h.cmdAudit()
	case "/menu":
		return "__MENU__" // ditangani gateway (kirim inline keyboard)
	case "/memory":
		return h.cmdMemory(h.Args(text))
	case "/lesson":
		return h.cmdLesson(h.Args(text))
	case "/help":
		return helpText
	default:
		// Pesan non-command (agent path belum ada di F1) — beri tahu dengan ramah
		return "🤖 Saya masih mode F1 — fitur percakapan AI hadir di F2.\n" +
			"Untuk saat ini pakai perintah: /ping /status /services /help\n" +
			"Pesan anda: \"" + text + "\""
	}
}

// Args dipakai command berikutnya (/log, /restart di F2+).
func (h *Handler) Args(text string) []string {
	_, args := splitCommand(text)
	return args
}

func (h *Handler) cmdServices() string {
	m, err := h.srv.Collect()
	if err != nil {
		return "⚠️ gagal: " + err.Error()
	}
	var b strings.Builder
	b.WriteString("🔧 Services:\n")
	for _, s := range m.Services {
		mark := "✅"
		if !s.Active {
			mark = "⛔"
		}
		fmt.Fprintf(&b, "  %s %s — %s\n", mark, s.Name, s.Status)
	}
	return b.String()
}

// cmdAudit menampilkan 15 baris terakhir audit log (Fase A).
func (h *Handler) cmdAudit() string {
	eng := tools.PermEngine()
	if eng == nil {
		return "Audit engine belum aktif."
	}
	lines := eng.TailAudit(15)
	if len(lines) == 0 {
		return "📜 Audit log kosong (belum ada aksi gated/confirm)."
	}
	return "📜 15 aksi terakhir:\n" + strings.Join(lines, "\n")
}

// MemoryProvider — interface fastpath ke memory.Store (F5).
type MemoryProvider interface {
	Stats() string
	Search(q string, limit int) []string
	Get(key string) (string, bool)
	PendingLessons() []string
	ConfirmLesson(id int) error
	DeleteLesson(id int) error
}

// cmdMemory: /memory [query] — stats atau cari isi memory.
func (h *Handler) cmdMemory(args []string) string {
	if h.mem == nil {
		return "🧠 Memory belum aktif."
	}
	if len(args) == 0 {
		return h.mem.Stats() + "\n\nCari: /memory <kata kunci>"
	}
	q := strings.Join(args, " ")
	hits := h.mem.Search(q, 8)
	if len(hits) == 0 {
		return "🧠 Tidak ada memory cocok untuk \"" + q + "\""
	}
	return "🧠 Hasil untuk \"" + q + "\":\n" + strings.Join(hits, "\n")
}

// cmdLesson: /lesson [id] — daftar pending atau konfirmasi lesson (F5c).
func (h *Handler) cmdLesson(args []string) string {
	if h.mem == nil {
		return "🧠 Memory belum aktif."
	}
	if len(args) == 0 {
		pend := h.mem.PendingLessons()
		if len(pend) == 0 {
			return "📚 Tidak ada lesson menunggu review."
		}
		return "📚 Lesson menunggu review:\n" + strings.Join(pend, "\n") +
			"\n\nSetujui: /lesson <id> · Hapus: /lesson hapus <id>"
	}
	if args[0] == "hapus" && len(args) >= 2 {
		var id int
		if _, err := fmt.Sscanf(args[1], "%d", &id); err != nil {
			return "ID tidak valid."
		}
		if err := h.mem.DeleteLesson(id); err != nil {
			return "hapus gagal: " + err.Error()
		}
		return fmt.Sprintf("🗑 Lesson #%d dihapus.", id)
	}
	var id int
	if _, err := fmt.Sscanf(args[0], "%d", &id); err != nil {
		return "Pakai: /lesson (daftar) · /lesson <id> (setujui) · /lesson hapus <id>"
	}
	if err := h.mem.ConfirmLesson(id); err != nil {
		return "konfirmasi gagal: " + err.Error()
	}
	return fmt.Sprintf("✅ Lesson #%d disetujui — masuk prompt agent.", id)
}

// splitCommand memecah "/cmd arg1 arg2".
func splitCommand(text string) (string, []string) {
	fields := strings.Fields(strings.TrimSpace(text))
	if len(fields) == 0 {
		return "", nil
	}
	return fields[0], fields[1:]
}

const helpText = `🤖 *aleph-agent* — perintah tersedia

*Server*
/status — kondisi server (RAM, disk, load, service)
/services — daftar service + status
/audit — 15 aksi terakhir yang ter-audit
/menu — ☰ menu tombol pintar (status, ping, speedtest, run command, services)

*AI & Memory*
/memory <kata kunci> — cari memory bot
/lesson — review lesson (approve/hapus)
/model [nama] — lihat/ganti model AI

*File & Dokumen*
Kirim file (PDF/DOCX/TXT) ke chat ini — saya baca & bisa ditanya
/files — daftar file yang sudah diupload
File >20 MB: taruh di /opt/aleph-agent/data/files/inbox (server)

*Lainnya*
/ping — tes bot hidup
/batal — batalkan aksi yang menunggu
/help — bantuan ini

💬 Atau bicara natural saja: "berapa RAM bebas?", "restart 9router", "9Router dijadikan auto startup"`
