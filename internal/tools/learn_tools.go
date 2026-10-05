package tools

// learn_tools.go — G4: tools pembelajaran untuk agent.
//   summarize_upload — ambil teks upload (per bagian) sebagai bahan rangkuman
//   make_document    — tulis .md/.txt ke folder outbox & kirim path-nya
//   make_ppt         — buat .pptx beneran (judul + bullets per slide)
//   research         — panduan riset: web_search + web_fetch sudah cukup; tool ini
//                      menyusun "lembar riset" (kumpul hasil search utk agent analisis)
//
// Semua output ditulis ke <filesRoot>/outbox — user ambil via /files atau folder inbox.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// RegisterLearnTools — dipanggil dari main.go.
func RegisterLearnTools(reg *Registry, filesRoot string) {
	outbox := filepath.Join(filesRoot, "outbox")

	// === make_document ===
	reg.Register(Tool{
		Name:        "make_document",
		Description: "Buat dokumen .md atau .txt di server untuk user. Isi ditulis utuh olehmu. Return path file.",
		Params: map[string]interface{}{
			"name":    map[string]interface{}{"type": "string", "description": "nama file tanpa ekstensi, mis. 'rangkuman-kimia'"},
			"format":  map[string]interface{}{"type": "string", "description": "'md' atau 'txt' (default md)"},
			"content": map[string]interface{}{"type": "string", "description": "isi dokumen utuh (markdown)"},
		},
		Required: []string{"name", "content"},
		Fn: func(ctx context.Context, args string) Result {
			var a struct {
				Name    string `json:"name"`
				Format  string `json:"format"`
				Content string `json:"content"`
			}
			if err := json.Unmarshal([]byte(args), &a); err != nil {
				return Fail("args tidak valid")
			}
			name := sanitizeDocName(a.Name)
			if name == "" {
				return Fail("nama file tidak valid")
			}
			format := a.Format
			if format != "txt" {
				format = "md"
			}
			if len(a.Content) > 2_000_000 {
				return Fail("dokumen terlalu besar (maks 2 MB)")
			}
			if err := os.MkdirAll(outbox, 0o755); err != nil {
				return Fail("mkdir: %v", err)
			}
			path := filepath.Join(outbox, name+"."+format)
			if err := os.WriteFile(path, []byte(a.Content), 0o644); err != nil {
				return Fail("tulis: %v", err)
			}
			return Ok(fmt.Sprintf("Dokumen dibuat: %s (outbox). User bisa mengambilnya via menu Files / folder outbox.", path))
		},
	})

	// === make_ppt ===
	reg.Register(Tool{
		Name:        "make_ppt",
		Description: "Buat presentasi .pptx beneran. Kembalikan path file. Susun slide dari judul + bullet points.",
		Params: map[string]interface{}{
			"name":  map[string]interface{}{"type": "string", "description": "nama file tanpa ekstensi"},
			"toplevel_title": map[string]interface{}{"type": "string", "description": "judul halaman pembuka"},
			"slides": map[string]interface{}{
				"type": "array",
				"items": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"title":   map[string]interface{}{"type": "string"},
						"bullets": map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}},
					},
				},
			},
		},
		Required: []string{"name", "slides"},
		Fn: func(ctx context.Context, args string) Result {
			var a struct {
				Name          string `json:"name"`
				ToplevelTitle string `json:"toplevel_title"`
				Slides        []struct {
					Title   string   `json:"title"`
					Bullets []string `json:"bullets"`
				} `json:"slides"`
			}
			if err := json.Unmarshal([]byte(args), &a); err != nil {
				return Fail("args tidak valid: %v", err)
			}
			name := sanitizeDocName(a.Name)
			if name == "" {
				return Fail("nama file tidak valid")
			}
			if len(a.Slides) == 0 {
				return Fail("minimal 1 slide")
			}
			var slides []pptxSlide
			if t := strings.TrimSpace(a.ToplevelTitle); t != "" {
				slides = append(slides, pptxSlide{Title: t})
			}
			for _, s := range a.Slides {
				if len(slides) >= 60 {
					break
				}
				bullets := s.Bullets
				if len(bullets) > 12 {
					bullets = bullets[:12] // kepadatan slide wajar
				}
				for i, b := range bullets {
					bullets[i] = truncateRunes(b, 220)
				}
				slides = append(slides, pptxSlide{Title: truncateRunes(s.Title, 120), Bullets: bullets})
			}
			if err := os.MkdirAll(outbox, 0o755); err != nil {
				return Fail("mkdir: %v", err)
			}
			path := filepath.Join(outbox, name+".pptx")
			if err := writePPTX(path, slides); err != nil {
				return Fail("buat pptx: %v", err)
			}
			return Ok(fmt.Sprintf("Presentasi dibuat: %s (%d slide) — folder outbox.", path, len(slides)))
		},
	})

}

// sanitizeDocName — nama file output yang aman (anti path traversal).
func sanitizeDocName(name string) string {
	name = filepath.Base(strings.ReplaceAll(name, "\\", "/"))
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case strings.ContainsRune(" ._-", r):
			b.WriteRune(r)
		}
	}
	out := strings.Trim(strings.TrimSpace(b.String()), ".")
	if out == "" || len(out) > 100 {
		return ""
	}
	return out
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
