// Package extract — ekstraksi teks dokumen (PDF/DOCX/teks) untuk fitur upload.
// Prinsip: disk-first & ramah CPU — PDF diekstrak halaman-per-halaman dengan
// jeda antar halaman (throttle), checkpoint disimpan agar bisa dilanjutkan
// setelah restart. Hasil ditulis bertahap ke file .txt di sebelah file asli.
package extract

import (
	"archive/zip"
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	pdf "github.com/ledongthuc/pdf"
)

// Throttle jeda antar halaman PDF (aman untuk N2600; bisa dinaikkan jika perlu).
var Throttle = 150 * time.Millisecond

// Result ringkasan satu sesi ekstraksi.
type Result struct {
	Pages     int    // total halaman PDF (0 jika bukan pdf)
	PagesDone int    // halaman terekstrak kumulatif
	TextPath  string // file .txt hasil
	Chars     int64  // jumlah karakter teks
	Done      bool   // selesai penuh
}

// TextPathFor menghitung path cache teks untuk sebuah file.
func TextPathFor(src string) string {
	ext := filepath.Ext(src)
	if ext == "" {
		ext = filepath.Ext(strings.ToLower(src))
	}
	return strings.TrimSuffix(src, ext) + ".extract.txt"
}

// IsSupported apakah tipe file bisa diekstrak.
func IsSupported(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".pdf", ".docx", ".txt", ".md", ".csv", ".json", ".log", ".xml", ".html", ".htm", ".yaml", ".yml", ".go", ".py", ".sh", ".js", ".ts", ".c", ".cpp", ".java":
		return true
	}
	return false
}

// IsTextDirect — file teks polos yang tidak perlu ekstraksi (langsung dipakai).
func IsTextDirect(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".txt", ".md", ".csv", ".json", ".log", ".xml", ".html", ".htm", ".yaml", ".yml", ".go", ".py", ".sh", ".js", ".ts", ".c", ".cpp", ".java":
		return true
	}
	return false
}

// Extract mengekstrak teks dari file ke TextPathFor(src) dengan resume.
//   - teks polos → salin (murah)
//   - docx       → gabungkan teks paragraf (murah)
//   - pdf        → per halaman + Throttle; resume dari halaman setelah checkpoint
//
// maxPages > 0 membatasi halaman (0 = semua).
func Extract(src string, maxPages int) (Result, error) {
	res := Result{TextPath: TextPathFor(src)}
	if IsTextDirect(src) {
		// teks polos: cache = salinan file (streaming, tanpa muat penuh di RAM)
		if err := copyFile(src, res.TextPath); err != nil {
			return res, err
		}
		st, _ := os.Stat(res.TextPath)
		if st != nil {
			res.Chars = st.Size()
		}
		res.Done = true
		return res, nil
	}
	switch strings.ToLower(filepath.Ext(src)) {
	case ".docx":
		return extractDocx(src, res)
	case ".pdf":
		return extractPDF(src, res, maxPages)
	}
	return res, fmt.Errorf("tipe file tidak didukung: %s", filepath.Ext(src))
}

// ---------- PDF ----------

var pageNumRe = regexp.MustCompile(`(?i)(?:^|\s)(?:p\.?|page |hal\.? )?(\d{1,5})\s*/\s*(\d{1,5})(?:\s|$)`)

func extractPDF(src string, res Result, maxPages int) (Result, error) {
	// BUKA file sekali dari disk — per halaman dibaca dari reader yang sama.
	f, r, err := pdf.Open(src)
	if err != nil {
		return res, fmt.Errorf("buka pdf: %w", err)
	}
	defer f.Close()

	total := r.NumPage()
	res.Pages = total
	if maxPages > 0 && total > maxPages {
		total = maxPages
	}

	start := 1
	// resume: kalau cache sudah ada, lanjut setelah checkpoint
	if st, err := os.Stat(res.TextPath); err == nil && st.Size() > 0 {
		start = resumePage(res.TextPath)
		if start > total {
			res.PagesDone = total
			res.Done = true
			return res, nil
		}
	}

	out, err := os.OpenFile(res.TextPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return res, err
	}
	defer out.Close()
	w := bufio.NewWriter(out)

	for p := start; p <= total; p++ {
		text := pageText(r, p)
		fmt.Fprintf(w, "\n\n===== Halaman %d =====\n%s", p, text)
		w.Flush()
		res.PagesDone = p
		// checkpoint ringan di akhir cache (baris terakhir) utk resume
		fmt.Fprintf(w, "\n[[checkpoint:%d]]", p)
		w.Flush()
		if p < total {
			time.Sleep(Throttle) // jaga CPU tetap adem
		}
	}
	res.Done = true
	st, _ := os.Stat(res.TextPath)
	if st != nil {
		res.Chars = st.Size()
	}
	return res, nil
}

// pageText mengekstrak teks satu halaman (aman panik-proof).
func pageText(r *pdf.Reader, pageNum int) (text string) {
	defer func() {
		if rec := recover(); rec != nil {
			text = "[halaman tidak terbaca]"
		}
	}()
	p := r.Page(pageNum)
	if p.V.IsNull() {
		return ""
	}
	rows, err := p.GetTextByRow()
	if err != nil {
		return ""
	}
	var b strings.Builder
	for _, row := range rows {
		for _, word := range row.Content {
			b.WriteString(word.S)
		}
		b.WriteString("\n")
	}
	return b.String()
}

// resumePage membaca checkpoint terakhir dari file cache.
func resumePage(path string) int {
	f, err := os.Open(path)
	if err != nil {
		return 1
	}
	defer f.Close()
	st, _ := f.Stat()
	if st == nil || st.Size() < 64 {
		return 1
	}
	// baca 256 byte terakhir
	buf := make([]byte, 256)
	n := int64(256)
	if st.Size() < n {
		n = st.Size()
	}
	if _, err := f.ReadAt(buf[:n], st.Size()-n); err != nil {
		return 1
	}
	var last int
	if ms := checkpointRe.FindAllSubmatch(buf[:n], -1); len(ms) > 0 {
		fmt.Sscanf(string(ms[len(ms)-1][1]), "%d", &last)
	}
	if last == 0 {
		// fallback: cari "===== Halaman N =====" terakhir
		last = lastPageMarker(buf[:n])
	}
	return last + 1
}

var pageMarkerRe = regexp.MustCompile(`(?m)^===== Halaman (\d+) =====`)
var checkpointRe = regexp.MustCompile("\\[\\[checkpoint:(\\d+)\\]\\]")

func lastPageMarker(b []byte) int {
	last := 0
	for _, m := range pageMarkerRe.FindAllSubmatch(b, -1) {
		var n int
		fmt.Sscanf(string(m[1]), "%d", &n)
		if n > last {
			last = n
		}
	}
	return last
}

// ---------- DOCX ----------

func extractDocx(src string, res Result) (Result, error) {
	text, err := docxText(src)
	if err != nil {
		return res, err
	}
	if err := os.WriteFile(res.TextPath, []byte(text), 0o644); err != nil {
		return res, err
	}
	res.Done = true
	res.Chars = int64(len(text))
	return res, nil
}

var xmlTagRe = regexp.MustCompile(`<[^>]+>`)

func docxText(src string) (string, error) {
	zr, err := zip.OpenReader(src)
	if err != nil {
		return "", fmt.Errorf("buka docx: %w", err)
	}
	defer zr.Close()
	for _, zf := range zr.File {
		if zf.Name != "word/document.xml" {
			continue
		}
		rc, err := zf.Open()
		if err != nil {
			return "", err
		}
		defer rc.Close()
		// N2600 RAM kecil: batasi 32 MB XML di memori (document.xml biasanya << ini)
		data, err := io.ReadAll(io.LimitReader(rc, 32<<20))
		if err == nil && len(data) == 32<<20 {
			return "", fmt.Errorf("docx terlalu besar (>32MB teks XML)")
		}
		if err != nil {
			return "", err
		}
		// paragraf </w:p> → baris baru, tab → spasi
		s := string(data)
		s = strings.ReplaceAll(s, "</w:p>", "\n")
		s = strings.ReplaceAll(s, "<w:tab/>", " ")
		s = xmlTagRe.ReplaceAllString(s, "")
		s = strings.NewReplacer("&amp;", "&", "&lt;", "<", "&gt;", ">", "&quot;", "\"", "&apos;", "'").Replace(s)
		return s, nil
	}
	return "", fmt.Errorf("docx tidak berisi word/document.xml")
}

// ---------- util ----------

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}

// Preview ambil N karakter pertama dari cache teks (streaming — RAM aman).
func Preview(textPath string, n int) string {
	if n <= 0 {
		n = 600
	}
	f, err := os.Open(textPath)
	if err != nil {
		return ""
	}
	defer f.Close()
	buf := make([]byte, n+1)
	rd, err := io.ReadFull(f, buf)
	if err != nil && err != io.ErrUnexpectedEOF && err != io.EOF {
		return ""
	}
	s := string(buf[:rd])
	if rd > n {
		s = s[:n] + "…"
	} else if rd == n+1 {
		s = s[:n] + "…"
	}
	return s
}
