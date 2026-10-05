// Package webui — G3: web UI internal aleph-agent via Tailscale.
// Halaman tunggal: Files (upload drag-and-drop + daftar + preview ekstraksi),
// Notes (kategori + catatan + backlink), Config (runtime AI tanpa restart).
//
// KEAMANAN:
//   - Bind ke 127.0.0.1:8080 + reverse auth Tailscale TIDAK dipakai — sebaliknya
//     server mendengarkan di tailscale0 jika ada; fallback localhost. BUKAN 0.0.0.0.
//   - Semua POST wajib membawa token sesi (di-generate saat start, log ke jurnal;
//     admin mengambilnya via /cfgweb di Telegram — single-admin).
//   - Upload dibatasi 2 GB (jauh di atas kebutuhan, mencegah request bodoh).
package webui

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"aleph-agent/internal/agent"
	"aleph-agent/internal/memory"
)

// Server web UI.
type Server struct {
	store    *memory.Store
	ag       *agent.Agent
	filesDir string
	admChat  int64
	token    string // sesi admin (regenerasi per start)
	mu       sync.Mutex
}

// New buat server UI + generate token.
func New(store *memory.Store, ag *agent.Agent, filesDir string, admChat int64) *Server {
	buf := make([]byte, 24)
	rand.Read(buf)
	return &Server{
		store:    store,
		ag:       ag,
		filesDir: filesDir,
		admChat:  admChat,
		token:    hex.EncodeToString(buf),
	}
}

// Token — diambil gateway Telegram (dikirim ke admin via /cfgweb).
func (s *Server) Token() string { return s.token }

// ListenAndServe mulai web UI. addr biasanya "127.0.0.1:8080" atau IP tailscale.
func (s *Server) ListenAndServe(addr string) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleHome)
	mux.HandleFunc("/api/files", s.requireAuth(s.handleFiles))
	mux.HandleFunc("/api/upload", s.requireAuth(s.handleUpload))
	mux.HandleFunc("/api/notes", s.requireAuth(s.handleNotes))
	mux.HandleFunc("/api/config", s.requireAuth(s.handleConfig))

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	srv := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	return srv.Serve(ln)
}

// PickAddr — pilih alamat aman: IP tailscale0 jika ada, else localhost.
func PickAddr() string {
	ifaces, err := net.Interfaces()
	if err == nil {
		for _, ifc := range ifaces {
			if !strings.Contains(strings.ToLower(ifc.Name), "tailscale") {
				continue
			}
			addrs, _ := ifc.Addrs()
			for _, a := range addrs {
				if ipnet, ok := a.(*net.IPNet); ok && ipnet.IP.To4() != nil {
					return ipnet.IP.String() + ":8080"
				}
			}
		}
	}
	return "127.0.0.1:8080"
}

// requireAuth middleware token sederhana (header X-Auth atau cookie).
func (s *Server) requireAuth(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tok := r.Header.Get("X-Auth")
		if tok == "" {
			if c, err := r.Cookie("aleph_token"); err == nil {
				tok = c.Value
			}
		}
		if tok != s.token {
			http.Error(w, "401 — token salah. Ambil token baru via Telegram: /cfgweb", http.StatusUnauthorized)
			return
		}
		h(w, r)
	}
}

// handleHome — halaman tunggal (HTML+JS inline, template html/template escape otomatis).
func (s *Server) handleHome(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	// token via query sekali → simpan cookie
	if q := r.URL.Query().Get("t"); q == s.token {
		http.SetCookie(w, &http.Cookie{Name: "aleph_token", Value: s.token, Path: "/", HttpOnly: true})
	} else if _, err := r.Cookie("aleph_token"); err != nil {
		http.Error(w, "401 — akses via http://IP:8080/?t=TOKEN (token dari /cfgweb di Telegram)", http.StatusUnauthorized)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	tmpl.Execute(w, pageData{Token: s.token})
}

type pageData struct {
	Token string
}

// ---------- API: files ----------

func (s *Server) handleFiles(w http.ResponseWriter, r *http.Request) {
	list := s.store.ListUploads(100)
	type fItem struct {
		ID     int64  `json:"id"`
		Name   string `json:"name"`
		Size   string `json:"size"`
		Status string `json:"status"`
		Pages  string `json:"pages"`
	}
	out := make([]fItem, 0)
	for _, u := range list {
		pages := ""
		if u.Pages > 0 {
			pages = fmt.Sprintf("%d/%d", u.PagesDone, u.Pages)
		}
		out = append(out, fItem{u.ID, u.Name, memory.HumanSize(u.Size), u.Status, pages})
	}
	writeJSON(w, out)
}

// handleUpload — multipart streaming ke folder inbox (RAM aman, tanpa batas praktis).
func (s *Server) handleUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 2<<30) // 2 GB
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		http.Error(w, "upload terlalu besar atau form salah: "+err.Error(), http.StatusBadRequest)
		return
	}
	file, hdr, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "file tidak ada: "+err.Error(), http.StatusBadRequest)
		return
	}
	defer file.Close()

	name := sanitizeUploadName(hdr.Filename)
	if name == "" {
		http.Error(w, "nama file tidak valid", http.StatusBadRequest)
		return
	}
	inbox := filepath.Join(s.filesDir, "inbox")
	os.MkdirAll(inbox, 0o755)
	dst := filepath.Join(inbox, name)
	if _, err := os.Stat(dst); err == nil {
		dst = dst + time.Now().Format("-150405")
	}
	out, err := os.Create(dst)
	if err != nil {
		http.Error(w, "gagal simpan: "+err.Error(), http.StatusInternalServerError)
		return
	}
	n, err := io.Copy(out, file)
	closeErr := out.Close()
	if err != nil || closeErr != nil {
		os.Remove(dst)
		http.Error(w, "gagal tulis", http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]string{
		"ok":   "1",
		"name": name,
		"size": memory.HumanSize(n),
		"info": "inbox — bot akan mengekstrak otomatis dalam ±1 menit",
	})
}

func sanitizeUploadName(name string) string {
	name = filepath.Base(strings.ReplaceAll(name, "\\", "/"))
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case strings.ContainsRune(" ._-()[]", r):
			b.WriteRune(r)
		}
	}
	out := strings.TrimSpace(b.String())
	out = strings.TrimLeft(out, ".")
	if out == "" || len(out) > 120 {
		return ""
	}
	return out
}

// ---------- API: notes ----------

func (s *Server) handleNotes(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		q := r.URL.Query()
		var notes []memory.Note
		if cat := q.Get("category"); cat != "" {
			notes = s.store.NotesByCategory(cat, 100)
		} else if search := q.Get("q"); search != "" {
			notes = s.store.SearchNotes(search, 50)
		} else {
			notes = s.store.SearchNotes("", 50) // semua (terbaru dulu)
		}
		type nItem struct {
			Slug     string `json:"slug"`
			Title    string `json:"title"`
			Category string `json:"category"`
			Preview  string `json:"preview"`
			Updated  string `json:"updated"`
		}
		out := make([]nItem, 0)
		for _, n := range notes {
			prev := strings.ReplaceAll(n.Content, "\n", " ")
			if r := []rune(prev); len(r) > 120 {
				prev = string(r[:120]) + "…"
			}
			out = append(out, nItem{n.Slug, n.Title, n.Category, prev, n.UpdatedAt})
		}
		writeJSON(w, out)
	case http.MethodPost:
		r.Body = http.MaxBytesReader(w, r.Body, 2<<20) // 2 MB (UpsertNote membatasi 1 MB)
		var body struct {
			Title    string `json:"title"`
			Category string `json:"category"`
			Content  string `json:"content"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "body salah", http.StatusBadRequest)
			return
		}
		n, _, err := s.store.UpsertNote(body.Title, body.Category, body.Content)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, map[string]string{"ok": "1", "slug": n.Slug})
	case http.MethodDelete:
		slug := r.URL.Query().Get("slug")
		if slug == "" {
			http.Error(w, "slug wajib", http.StatusBadRequest)
			return
		}
		if err := s.store.DeleteNote(slug); err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		writeJSON(w, map[string]string{"ok": "1"})
	default:
		http.Error(w, "method tidak didukung", http.StatusMethodNotAllowed)
	}
}

// ---------- API: config ----------

func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		writeJSON(w, map[string]interface{}{
			"context_budget": s.ag.GetContextBudget(),
			"max_rounds":     s.ag.GetMaxRounds(),
			"timeout_sec":    s.ag.GetTimeout(),
			"model":          s.ag.GetModel(),
		})
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method tidak didukung", http.StatusMethodNotAllowed)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10) // 64 KB cukup untuk config
	var body struct {
		ContextBudget int    `json:"context_budget"`
		MaxRounds     int    `json:"max_rounds"`
		TimeoutSec    int    `json:"timeout_sec"`
		Model         string `json:"model"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "body salah", http.StatusBadRequest)
		return
	}
	// hanya field yang diisi (>0) yang diubah
	if body.ContextBudget > 0 {
		if err := s.ag.SetContextBudget(body.ContextBudget); err != nil {
			http.Error(w, "context_budget: "+err.Error(), http.StatusBadRequest)
			return
		}
	}
	if body.MaxRounds > 0 {
		if err := s.ag.SetMaxRounds(body.MaxRounds); err != nil {
			http.Error(w, "max_rounds: "+err.Error(), http.StatusBadRequest)
			return
		}
	}
	if body.TimeoutSec > 0 {
		if err := s.ag.SetTimeout(body.TimeoutSec); err != nil {
			http.Error(w, "timeout: "+err.Error(), http.StatusBadRequest)
			return
		}
	}
	if body.Model != "" {
		m := strings.TrimSpace(body.Model)
		if len(m) > 80 || strings.ContainsAny(m, "\r\n\x00") {
			http.Error(w, "model tidak valid", http.StatusBadRequest)
			return
		}
		s.ag.SetModel(m)
	}
	writeJSON(w, map[string]string{"ok": "1"})
}

func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

