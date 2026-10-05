// Package tools — F5b: skill system. Skill = file markdown di <dataDir>/skills/*.md.
// Agent bisa recall (baca skill) & save (tulis skill). Index deskripsi disuntik ke system prompt.
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// SkillsDir — direktori skill (di-set main).
var SkillsDir string

// SetSkillsDir menentukan folder skills + pastikan ada.
func SetSkillsDir(dir string) error {
	SkillsDir = dir
	return os.MkdirAll(dir, 0o755)
}

// skillBrief: baris pertama file = deskripsi singkat (# Judul — deskripsi).
func skillBrief(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			if strings.HasPrefix(line, "# ") {
				return strings.TrimPrefix(line, "# ")
			}
			continue
		}
		if r := []rune(line); len(r) > 100 {
			line = string(r[:100]) + "…"
		}
		return line
	}
	return ""
}

// SkillIndex ringkasan semua skill (untuk system prompt).
func SkillIndex() string {
	if SkillsDir == "" {
		return ""
	}
	entries, err := os.ReadDir(SkillsDir)
	if err != nil {
		return ""
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") {
			names = append(names, e.Name())
		}
	}
	if len(names) == 0 {
		return ""
	}
	sort.Strings(names)
	var b strings.Builder
	b.WriteString("SKILL TERSEDIA (pakai tool recall_skill untuk membaca):\n")
	for _, n := range names {
		brief := skillBrief(filepath.Join(SkillsDir, n))
		fmt.Fprintf(&b, "- %s — %s\n", strings.TrimSuffix(n, ".md"), brief)
	}
	return b.String()
}

// RegisterMemorySkillTools — dihapus (implementasi konkret ada di RegisterMemoryTools/RegisterSkillTools).

// MemoryStore — interface minim yang dibutuhkan tools (dipenuhi *memory.Store).
type MemoryStore interface {
	Get(key string) (string, bool)
	Put(key, content string) error
	Search(q string, limit int) []string
}

// LessonStore — interface lesson (dipenuhi *memory.Store).
type LessonStore interface {
	AddLesson(topic, lesson string) error
}

// RegisterMemoryTools menambahkan tools memory (F5) — butuh *memory.Store via interface minim.
func RegisterMemoryTools(r *Registry, mem MemoryStore) {
	// === memory_save ===
	r.Register(Tool{
		Name:        "memory_save",
		Description: "Simpan fakta penting ke memory jangka panjang (key unik + isi). Contoh: preferensi user, info perangkat, keputusan.",
		Params: map[string]interface{}{
			"key":     map[string]interface{}{"type": "string", "description": "kunci unik, mis. 'preferensi-bahasa'"},
			"content": map[string]interface{}{"type": "string"},
		},
		Required: []string{"key", "content"},
		Fn: func(ctx context.Context, args string) Result {
			var a struct {
				Key     string `json:"key"`
				Content string `json:"content"`
			}
			if err := json.Unmarshal([]byte(args), &a); err != nil {
				return Fail("args tidak valid")
			}
			if mem == nil {
				return Fail("memory belum aktif")
			}
			if err := mem.Put(a.Key, a.Content); err != nil {
				return Fail("simpan gagal: %v", err)
			}
			return Ok(fmt.Sprintf("tersimpan ke memory: %s", a.Key))
		},
	})

	// === memory_recall ===
	r.Register(Tool{
		Name:        "memory_recall",
		Description: "Cari fakta di memory jangka panjang berdasarkan kata kunci.",
		Params: map[string]interface{}{
			"query": map[string]interface{}{"type": "string"},
		},
		Required: []string{"query"},
		Fn: func(ctx context.Context, args string) Result {
			var a struct {
				Query string `json:"query"`
			}
			if err := json.Unmarshal([]byte(args), &a); err != nil {
				return Fail("args tidak valid")
			}
			if mem == nil {
				return Fail("memory belum aktif")
			}
			if v, ok := mem.Get(a.Query); ok {
				return Ok(v)
			}
			hits := mem.Search(a.Query, 5)
			if len(hits) == 0 {
				return Ok("(tidak ada memory cocok)")
			}
			return Ok(strings.Join(hits, "\n"))
		},
	})
}

// RegisterSkillTools menambahkan tools skill (F5b).
func RegisterSkillTools(r *Registry, les LessonStore) {
	// === recall_skill ===
	r.Register(Tool{
		Name:        "recall_skill",
		Description: "Baca isi skill (pengetahuan/prosedur tersimpan) berdasarkan nama, mis. 'router-recovery'.",
		Params: map[string]interface{}{
			"name": map[string]interface{}{"type": "string", "description": "nama skill tanpa .md"},
		},
		Required: []string{"name"},
		Fn: func(ctx context.Context, args string) Result {
			var a struct {
				Name string `json:"name"`
			}
			if err := json.Unmarshal([]byte(args), &a); err != nil {
				return Fail("args tidak valid")
			}
			name := filepath.Base(a.Name) // cegah path traversal
			if SkillsDir == "" {
				return Fail("skill system belum aktif")
			}
			data, err := os.ReadFile(filepath.Join(SkillsDir, name+".md"))
			if err != nil {
				return Ok("skill '" + name + "' tidak ditemukan. Skill tersedia:\n" + SkillIndex())
			}
			if len(data) > 12<<10 {
				data = data[:12<<10]
			}
			return Ok(string(data))
		},
	})

	// === save_skill ===
	r.Register(Tool{
		Name:        "save_skill",
		Description: "Simpan/buat skill baru (markdown) agar bisa dipakai lagi. Gunakan untuk prosedur yang berhasil kamu kerjakan.",
		Params: map[string]interface{}{
			"name":    map[string]interface{}{"type": "string", "description": "nama skill, format kecil-strip, mis. 'docker-recovery'"},
			"content": map[string]interface{}{"type": "string", "description": "isi markdown; baris pertama '# Judul — deskripsi singkat'"},
		},
		Required: []string{"name", "content"},
		Fn: func(ctx context.Context, args string) Result {
			var a struct {
				Name    string `json:"name"`
				Content string `json:"content"`
			}
			if err := json.Unmarshal([]byte(args), &a); err != nil {
				return Fail("args tidak valid")
			}
			if SkillsDir == "" {
				return Fail("skill system belum aktif")
			}
			name := strings.ToLower(strings.TrimSpace(a.Name))
			name = strings.Map(func(r rune) rune {
				if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
					return r
				}
				return '-'
			}, name)
			name = strings.Trim(name, "-")
			if name == "" {
				return Fail("nama skill tidak valid")
			}
			p := filepath.Join(SkillsDir, name+".md")
			if err := os.WriteFile(p, []byte(a.Content), 0o644); err != nil {
				return Fail("tulis skill gagal: %v", err)
			}
			return Ok(fmt.Sprintf("skill tersimpan: %s (%d bytes)", p, len(a.Content)))
		},
	})

	// === skill_patch (G6) — edit skill yang ada (find & replace) ===
	r.Register(Tool{
		Name:        "skill_patch",
		Description: "Perbaiki skill yang sudah ada: ganti teks lama dengan baru (exact match, satu lokasi). Gunakan setelah membaca skill dengan recall_skill.",
		Params: map[string]interface{}{
			"name":  map[string]interface{}{"type": "string", "description": "nama skill yang akan diedit"},
			"old":   map[string]interface{}{"type": "string", "description": "teks lama yang dicari (harus unik)"},
			"new":   map[string]interface{}{"type": "string", "description": "teks pengganti"},
		},
		Required: []string{"name", "old", "new"},
		Fn: func(ctx context.Context, args string) Result {
			var a struct {
				Name string `json:"name"`
				Old  string `json:"old"`
				New  string `json:"new"`
			}
			if err := json.Unmarshal([]byte(args), &a); err != nil {
				return Fail("args tidak valid")
			}
			if SkillsDir == "" {
				return Fail("skill system belum aktif")
			}
			name := strings.ToLower(strings.TrimSpace(a.Name))
			name = strings.Map(func(r rune) rune {
				if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
					return r
				}
				return '-'
			}, name)
			name = strings.Trim(name, "-")
			if name == "" {
				return Fail("nama skill tidak valid")
			}
			p := filepath.Join(SkillsDir, name+".md")
			data, err := os.ReadFile(p)
			if err != nil {
				return Fail("skill tidak ditemukan: %s", name)
			}
			if len(a.New) > 64<<10 {
				return Fail("pengganti terlalu besar")
			}
			content := string(data)
			count := strings.Count(content, a.Old)
			if count == 0 {
				return Fail("teks lama tidak ditemukan — baca dulu dengan recall_skill")
			}
			if count > 1 {
				return Fail("teks lama muncul %d kali — berikan potongan yang lebih spesifik", count)
			}
			updated := strings.Replace(content, a.Old, a.New, 1)
			if err := os.WriteFile(p, []byte(updated), 0o644); err != nil {
				return Fail("tulis gagal: %v", err)
			}
			return Ok(fmt.Sprintf("skill %s diperbarui (%d bytes)", name, len(updated)))
		},
	})

	// === add_lesson (F5c — masuk antrian review, belum aktif sampai dikonfirmasi) ===
	r.Register(Tool{
		Name:        "add_lesson",
		Description: "Catat pelajaran dari kegagalan/kesuksesan (topic singkat + lesson 1 kalimat). Lesson baru menunggu konfirmasi admin sebelum dipakai.",
		Params: map[string]interface{}{
			"topic":  map[string]interface{}{"type": "string", "description": "topik singkat, mis. 'docker-restart'"},
			"lesson": map[string]interface{}{"type": "string", "description": "pelajaran 1 kalimat"},
		},
		Required: []string{"topic", "lesson"},
		Fn: func(ctx context.Context, args string) Result {
			var a struct {
				Topic  string `json:"topic"`
				Lesson string `json:"lesson"`
			}
			if err := json.Unmarshal([]byte(args), &a); err != nil {
				return Fail("args tidak valid")
			}
			if les == nil {
				return Fail("lesson store belum aktif")
			}
			if err := les.AddLesson(a.Topic, a.Lesson); err != nil {
				return Fail("simpan lesson gagal: %v", err)
			}
			return Ok("lesson dicatat (menunggu konfirmasi admin via /lesson): " + a.Topic)
		},
	})
}
