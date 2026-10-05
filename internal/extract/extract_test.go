package extract

import (
	"archive/zip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTextPathFor(t *testing.T) {
	if got := TextPathFor("a/b/c.pdf"); got != "a/b/c.extract.txt" {
		t.Fatalf("TextPathFor = %q", got)
	}
	if got := TextPathFor("noext"); !strings.HasSuffix(got, ".extract.txt") {
		t.Fatalf("TextPathFor noext = %q", got)
	}
}

func TestResumePage(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "x.extract.txt")
	os.WriteFile(p, []byte("\n\n===== Halaman 1 =====\nabc\n[[checkpoint:1]]\n\n===== Halaman 2 =====\ndef\n[[checkpoint:2]]"), 0o644)
	if got := resumePage(p); got != 3 {
		t.Fatalf("resumePage = %d, want 3", got)
	}
}

func TestDocxText(t *testing.T) {
	dir := t.TempDir()
	docx := filepath.Join(dir, "t.docx")
	f, _ := os.Create(docx)
	zw := zip.NewWriter(f)
	w, _ := zw.Create("word/document.xml")
	w.Write([]byte(`<w:document><w:body><w:p><w:r><w:t>Halo</w:t></w:r></w:p><w:p><w:r><w:t>Dunia &amp; seterusnya</w:t></w:r></w:p></w:body></w:document>`))
	zw.Close()
	f.Close()
	s, err := docxText(docx)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(s, "Halo") || !strings.Contains(s, "Dunia & seterusnya") {
		t.Fatalf("docxText = %q", s)
	}
}

func TestIsSupported(t *testing.T) {
	for _, n := range []string{"a.pdf", "b.docx", "c.txt", "d.md"} {
		if !IsSupported(n) {
			t.Fatalf("%s harus didukung", n)
		}
	}
	if IsSupported("x.exe") {
		t.Fatal("exe tidak boleh didukung")
	}
}
