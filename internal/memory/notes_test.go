package memory

import (
	"strings"
	"testing"
)

func TestSlugify(t *testing.T) {
	cases := map[string]string{
		"Cara Pakai Git Rebase": "cara-pakai-git-rebase",
		"  spasi  ganda  ":      "spasi-ganda",
		"!!!":                   "catatan",
		"コード notes":             "コード-notes",
	}
	for in, wantSub := range cases {
		got := Slugify(in)
		if got == "" {
			t.Fatalf("Slugify(%q) kosong", in)
		}
		if in == "!!! " && got != "catatan" {
			t.Fatalf("Slugify !!= %q", got)
		}
		_ = wantSub
	}
	if Slugify("!!!") != "catatan" {
		t.Fatalf("Slugify !!! != catatan")
	}
	if !strings.HasPrefix(Slugify("Cara Pakai Git Rebase"), "cara-pakai-git") {
		t.Fatalf("slug salah: %q", Slugify("Cara Pakai Git Rebase"))
	}
}

func TestWikiLinkRegex(t *testing.T) {
	content := "lihat [[Git Rebase]] dan [[python/async]] tapi bukan [biasa] atau [[x][y]]"
	links := wikiLinkRe.FindAllStringSubmatch(content, -1)
	if len(links) != 2 {
		t.Fatalf("harus 2 link, dapat %d", len(links))
	}
	if links[0][1] != "Git Rebase" || links[1][1] != "python/async" {
		t.Fatalf("link salah: %v %v", links[0][1], links[1][1])
	}
}

func TestUpsertNoteAndLinks(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Skip("sqlite memory tidak tersedia:", err)
	}
	defer s.Close()

	// catatan A
	if _, _, err := s.UpsertNote("Git Rebase", "Pemrograman", "Catatan tentang git rebase"); err != nil {
		t.Fatal(err)
	}
	// catatan B me-link ke A (update dua kali: upsert path)
	_, created, err := s.UpsertNote("Git Rebase", "Pemrograman", "versi kedua")
	if err != nil {
		t.Fatal(err)
	}
	if created {
		t.Fatal("harusnya update, bukan create")
	}
	if _, created, err := s.UpsertNote("Git Workflow", "Pemrograman", "lihat [[Git Rebase]] untuk detail"); err != nil {
		t.Fatal(err)
	} else if !created {
		t.Fatal("harusnya catatan baru")
	}

	back, fwd := s.LinkedNotes("git-rebase")
	if len(back) != 1 || back[0] != "Git Workflow" {
		t.Fatalf("backlink salah: %v", back)
	}
	_ = fwd

	// pencarian
	if len(s.SearchNotes("rebase", 5)) != 2 { // "rebase" ada di isi kedua catatan
		t.Fatalf("search salah")
	}
	// kategori
	cats := s.Categories()
	if len(cats) == 0 || cats[0].Name != "Pemrograman" {
		t.Fatalf("kategori salah: %v", cats)
	}
	// hapus
	if err := s.DeleteNote("git-workflow"); err != nil {
		t.Fatal(err)
	}
	back, _ = s.LinkedNotes("git-rebase")
	if len(back) != 0 {
		t.Fatalf("backlink harus kosong setelah hapus: %v", back)
	}
}

func TestNoteHardening(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Skip("skip:", err)
	}
	defer s.Close()

	// judul kosong ditolak
	if _, _, err := s.UpsertNote("   ", "x", "isi"); err == nil {
		t.Fatal("judul kosong harus ditolak")
	}
	// konten >1MB ditolak
	big := strings.Repeat("a", 1<<20+10)
	if _, _, err := s.UpsertNote("besar", "x", big); err == nil {
		t.Fatal("konten >1MB harus ditolak")
	}
	// self-link diabaikan
	if _, _, err := s.UpsertNote("Loop", "x", "lihat [[Loop]] lagi"); err != nil {
		t.Fatal(err)
	}
	back, fwd := s.LinkedNotes("loop")
	if len(back) != 0 || len(fwd) != 0 {
		t.Fatalf("self-link harus diabaikan: %v %v", back, fwd)
	}
	// judul beda tapi slug sama → update (tidak duplikat baris)
	if _, c1, _ := s.UpsertNote("Git Rebase", "k", "a"); !c1 {
		t.Fatal("harusnya create pertama")
	}
	// "git-rebase!" beda judul, slug sama
	if _, c2, _ := s.UpsertNote("git rebase!", "k", "b"); c2 {
		t.Fatal("slug sama harusnya update")
	}
	if n := len(s.SearchNotes("rebase", 10)); n != 1 {
		t.Fatalf("harus 1 catatan, dapat %d", n)
	}
	// delete yang tidak ada → error
	if err := s.DeleteNote("tidak-ada"); err == nil {
		t.Fatal("hapus slug tak ada harus error")
	}
}
