package tools

// G2 — tools Notes "Obsidian" untuk agent: create/list/search/read/delete notes
// berkategori dengan wiki-link [[judul]].

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"aleph-agent/internal/memory"
)

// RegisterNoteTools — dipanggil dari main.go setelah memStore siap.
func RegisterNoteTools(reg *Registry, memStore *memory.Store) {
	// === note_save ===
	reg.Register(Tool{
		Name:        "note_save",
		Description: "Simpan/buat catatan milik user (seperti Obsidian): kategori bebas (mis. Pembelajaran, Pemrograman), isi markdown. Pakai [[Judul catatan lain]] untuk menautkan antar catatan. Menyimpan dengan judul yang sama = memperbarui.",
		Params: map[string]interface{}{
			"title":    map[string]interface{}{"type": "string", "description": "judul catatan"},
			"category": map[string]interface{}{"type": "string", "description": "kategori bebas, mis. Pembelajaran / Pemrograman / Resep"},
			"content":  map[string]interface{}{"type": "string", "description": "isi catatan (markdown), boleh memakai [[Judul]] untuk link"},
		},
		Required: []string{"title", "content"},
		Fn: func(ctx context.Context, args string) Result {
			var a struct {
				Title    string `json:"title"`
				Category string `json:"category"`
				Content  string `json:"content"`
			}
			if err := json.Unmarshal([]byte(args), &a); err != nil {
				return Fail("args tidak valid")
			}
			n, created, err := memStore.UpsertNote(a.Title, a.Category, a.Content)
			if err != nil {
				return Fail("simpan gagal: %v", err)
			}
			verb := "dibuat"
			if !created {
				verb = "diperbarui"
			}
			// info tautan yang terdeteksi
			msg := fmt.Sprintf("✅ Catatan '%s' %s (kategori: %s).", n.Title, verb, n.Category)
			if ln := countLinks(a.Content); ln > 0 {
				msg += fmt.Sprintf(" %d tautan [[]] terdeteksi.", ln)
			}
			return Ok(msg)
		},
	})

	// === note_get ===
	reg.Register(Tool{
		Name:        "note_get",
		Description: "Baca satu catatan berdasar judul (atau parse judul), beserta backlink & catatan terhubung.",
		Params: map[string]interface{}{
			"title": map[string]interface{}{"type": "string"},
		},
		Required: []string{"title"},
		Fn: func(ctx context.Context, args string) Result {
			var a struct {
				Title string `json:"title"`
			}
			if err := json.Unmarshal([]byte(args), &a); err != nil {
				return Fail("args tidak valid")
			}
			n, err := memStore.GetNoteBySlug(memory.Slugify(a.Title))
			if err != nil || n == nil {
				hits := memStore.SearchNotes(a.Title, 1)
				if len(hits) == 0 {
					return Ok("Catatan tidak ditemukan. Coba note_search dulu.")
				}
				n = &hits[0]
			}
			back, fwd := memStore.LinkedNotes(n.Slug)
			var b strings.Builder
			fmt.Fprintf(&b, "📄 %s (kategori: %s, diubah %s)\n\n%s", n.Title, n.Category, n.UpdatedAt, n.Content)
			if len(back) > 0 {
				b.WriteString("\n\n🔗 Dirujuk oleh: " + strings.Join(back, ", "))
			}
			if len(fwd) > 0 {
				b.WriteString("\n\n↗️ Merujuk ke: " + strings.Join(fwd, ", "))
			}
			return Ok(b.String())
		},
	})

	// === note_search ===
	reg.Register(Tool{
		Name:        "note_search",
		Description: "Cari catatan user berdasar kata kunci (judul/isi/kategori).",
		Params: map[string]interface{}{
			"query":    map[string]interface{}{"type": "string"},
			"category": map[string]interface{}{"type": "string", "description": "opsional: filter kategori"},
		},
		Required: []string{"query"},
		Fn: func(ctx context.Context, args string) Result {
			var a struct {
				Query    string `json:"query"`
				Category string `json:"category"`
			}
			if err := json.Unmarshal([]byte(args), &a); err != nil {
				return Fail("args tidak valid")
			}
			var notes []memory.Note
			if a.Category != "" {
				notes = memStore.NotesByCategory(a.Category, 20)
				// filter manual by query
				var filtered []memory.Note
				for _, n := range notes {
					if strings.Contains(strings.ToLower(n.Title+" "+n.Content), strings.ToLower(a.Query)) {
						filtered = append(filtered, n)
					}
				}
				notes = filtered
			} else {
				notes = memStore.SearchNotes(a.Query, 20)
			}
			if len(notes) == 0 {
				return Ok("Tidak ada catatan cocok.")
			}
			var b strings.Builder
			for _, n := range notes {
				fmt.Fprintf(&b, "- %s [%s] — %s\n", n.Title, n.Category, previewLine(n.Content, 80))
			}
			return Ok(b.String())
		},
	})

	// === note_categories ===
	reg.Register(Tool{
		Name:        "note_categories",
		Description: "Daftar kategori catatan + jumlah catatan per kategori.",
		Params:      map[string]interface{}{},
		Fn: func(ctx context.Context, args string) Result {
			cats := memStore.Categories()
			if len(cats) == 0 {
				return Ok("Belum ada kategori catatan.")
			}
			var b strings.Builder
			for _, c := range cats {
				fmt.Fprintf(&b, "- %s: %d catatan\n", c.Name, c.Count)
			}
			return Ok(b.String())
		},
	})

	// === note_delete ===
	reg.Register(Tool{
		Name:        "note_delete",
		Description: "Hapus satu catatan user by judul. Hanya jika user meminta penghapusan eksplisit.",
		Params: map[string]interface{}{
			"title": map[string]interface{}{"type": "string"},
		},
		Required: []string{"title"},
		Fn: func(ctx context.Context, args string) Result {
			var a struct {
				Title string `json:"title"`
			}
			if err := json.Unmarshal([]byte(args), &a); err != nil {
				return Fail("args tidak valid")
			}
			slug := memory.Slugify(a.Title)
			n, err := memStore.GetNoteBySlug(slug)
			if err != nil || n == nil {
				// fallback: pencarian longgar, tapi WAJIB unik
				hits := memStore.SearchNotes(a.Title, 2)
				if len(hits) == 0 {
					return Ok("Catatan tidak ditemukan.")
				}
				if len(hits) > 1 {
					return Ok("Ada beberapa catatan cocok. Sebutkan judulnya lebih spesifik:\n- " +
						strings.Join([]string{hits[0].Title, hits[1].Title}, "\n- "))
				}
				n = &hits[0]
			}
			if err := memStore.DeleteNote(n.Slug); err != nil {
				return Fail("hapus gagal: %v", err)
			}
			return Ok("🗑 Catatan '" + n.Title + "' dihapus.")
		},
	})
}

func countLinks(content string) int {
	return strings.Count(content, "[[") - strings.Count(content, "[[[")
}

func previewLine(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if r := []rune(s); len(r) > n {
		s = string(r[:n]) + "…"
	}
	return s
}
