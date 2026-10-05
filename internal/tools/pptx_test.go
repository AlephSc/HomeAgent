
package tools

import (
	"archive/zip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWritePPTX(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "uji.pptx")
	slides := []pptxSlide{
		{Title: "Judul Utama <&tes>"},
		{Title: "Slide 1", Bullets: []string{"poin a", "poin & b", "«unik»"}},
	}
	if err := writePPTX(path, slides); err != nil {
		t.Fatal(err)
	}
	f, err := zip.OpenReader(path)
	if err != nil {
		t.Fatal("bukan zip valid:", err)
	}
	defer f.Close()
	want := map[string]bool{
		"[Content_Types].xml":            false,
		"ppt/presentation.xml":          false,
		"ppt/slides/slide1.xml":         false,
		"ppt/slides/slide2.xml":         false,
		"ppt/slides/_rels/slide2.xml.rels": false,
	}
	for _, zf := range f.File {
		if _, ok := want[zf.Name]; ok {
			want[zf.Name] = true
		}
	}
	for name, found := range want {
		if !found {
			t.Errorf("part hilang: %s", name)
		}
	}
	// escape cek
	data, _ := os.ReadFile(path) // biner; cek via zip read
	_ = data
	for _, zf := range f.File {
		if zf.Name == "ppt/slides/slide1.xml" {
			rc, _ := zf.Open()
			buf := make([]byte, 4096)
			n, _ := rc.Read(buf)
			s := string(buf[:n])
			if !strings.Contains(s, "Judul Utama &lt;&amp;tes&gt;") {
				t.Error("escape XML salah:", s)
			}
			rc.Close()
		}
	}
	// batas 60 slide
	many := make([]pptxSlide, 100)
	for i := range many {
		many[i] = pptxSlide{Title: "s"}
	}
	p2 := filepath.Join(dir, "banyak.pptx")
	if err := writePPTX(p2, many); err != nil {
		t.Fatal(err)
	}
	zr, err := zip.OpenReader(p2)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	n := 0
	for _, zf := range zr.File {
		if strings.HasPrefix(zf.Name, "ppt/slides/slide") && strings.HasSuffix(zf.Name, ".xml") {
			n++
		}
	}
	if n != 60 {
		t.Errorf("slide = %d, ingin 60", n)
	}
}
