package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"aleph-agent/internal/extract"
	"aleph-agent/internal/memory"
)

// RegisterUploadTools — tool untuk agent mengakses file yang diupload user.
// Dipanggil dari main.go setelah memStore siap.
func RegisterUploadTools(reg *Registry, memStore *memory.Store, filesRoot string) {
	// === list_uploads ===
	reg.Register(Tool{
		Name:        "list_uploads",
		Description: "Daftar file yang diupload user / ada di inbox (nama, ukuran, status ekstraksi, halaman).",
		Params:      map[string]interface{}{},
		Fn: func(ctx context.Context, args string) Result {
			list := memStore.ListUploads(25)
			if len(list) == 0 {
				return Ok("Belum ada file yang diupload.")
			}
			var b strings.Builder
			for _, u := range list {
				fmt.Fprintf(&b, "- %s | %s | status: %s", u.Name, memory.HumanSize(u.Size), u.Status)
				if u.Pages > 0 {
					fmt.Fprintf(&b, " | hal %d/%d", u.PagesDone, u.Pages)
				}
				if u.Err != "" {
					fmt.Fprintf(&b, " | err: %.120s", u.Err)
				}
				b.WriteString("\n")
			}
			return Ok(b.String())
		},
	})

	// === read_upload ===
	reg.Register(Tool{
		Name:        "read_upload",
		Description: "Baca teks hasil ekstraksi file yang pernah diupload user (PDF/DOCX/TXT). Gunakan list_uploads untuk melihat daftar.",
		Params: map[string]interface{}{
			"name":   map[string]interface{}{"type": "string", "description": "nama/parse nama file, mis. 'materi-kimia'"},
			"offset": map[string]interface{}{"type": "integer", "description": "opsional: mulai dari byte ke-N (default 0)"},
			"limit":  map[string]interface{}{"type": "integer", "description": "opsional: maks karakter yang dikembalikan (default 12000)"},
		},
		Required: []string{"name"},
		Fn: func(ctx context.Context, args string) Result {
			var a struct {
				Name   string `json:"name"`
				Offset int64  `json:"offset"`
				Limit  int    `json:"limit"`
			}
			if err := json.Unmarshal([]byte(args), &a); err != nil {
				return Fail("args tidak valid")
			}
			if a.Limit <= 0 || a.Limit > 20000 {
				a.Limit = 12000
			}
			u, err := memStore.FindUploadByName(a.Name)
			if err != nil || u == nil {
				return Ok("File tidak ditemukan. Gunakan list_uploads untuk melihat daftar nama.")
			}
			if u.Status != "siap" && u.Status != "proses" {
				return Ok(fmt.Sprintf("File '%s' belum siap dibaca (status: %s).", u.Name, u.Status))
			}
			if u.TextPath == "" {
				if _, err := os.Stat(extract.TextPathFor(u.Path)); err != nil {
					return Ok(fmt.Sprintf("File '%s' belum diekstrak.", u.Name))
				}
			}
			tp := u.TextPath
			if tp == "" {
				tp = extract.TextPathFor(u.Path)
			}
			f, err := os.Open(tp)
			if err != nil {
				return Fail("baca cache: %v", err)
			}
			defer f.Close()
			buf := make([]byte, a.Limit)
			n, err := f.ReadAt(buf, a.Offset)
			if err != nil && n == 0 {
				if a.Offset > 0 {
					return Ok("[EOF — konten habis]")
				}
				return Fail("baca: %v", err)
			}
			head := fmt.Sprintf("=== %s (offset %d) ===\n", u.Name, a.Offset)
			return Ok(head + string(buf[:n]))
		},
	})

	// === upload_info ===
	reg.Register(Tool{
		Name:        "upload_info",
		Description: "Detail satu file upload: ukuran, halaman, progres ekstraksi, preview awal teks.",
		Params: map[string]interface{}{
			"name": map[string]interface{}{"type": "string"},
		},
		Required: []string{"name"},
		Fn: func(ctx context.Context, args string) Result {
			var a struct {
				Name string `json:"name"`
			}
			if err := json.Unmarshal([]byte(args), &a); err != nil {
				return Fail("args tidak valid")
			}
			u, err := memStore.FindUploadByName(a.Name)
			if err != nil || u == nil {
				return Ok("File tidak ditemukan.")
			}
			msg := fmt.Sprintf("%s | %s | status: %s", u.Name, memory.HumanSize(u.Size), u.Status)
			if u.Pages > 0 {
				msg += fmt.Sprintf(" | hal %d/%d", u.PagesDone, u.Pages)
			}
			if u.Err != "" {
				msg += " | err: " + u.Err
			}
			tp := u.TextPath
			if tp == "" {
				tp = extract.TextPathFor(u.Path)
			}
			if pv := extract.Preview(tp, 500); pv != "" {
				msg += "\n\nPreview:\n" + pv
			}
			return Ok(msg)
		},
	})

	_ = filesRoot // disiapkan untuk fitur simpan file (G4)
	_ = filepath.Join
}
