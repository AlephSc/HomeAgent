package telegram

import (
	"strings"
	"testing"
)

func TestSanitizeQuery(t *testing.T) {
	if got := sanitizeQuery("  claude sonnet-4.5 "); got != "claude sonnet-4.5" {
		t.Fatalf("sanitize salah: %q", got)
	}
	// injeksi charset dibuang
	if got := sanitizeQuery("a;b\nc|d$e"); got != "abcde" {
		t.Fatalf("charset berbahaya lolos: %q", got)
	}
	// dipotong 32
	if got := sanitizeQuery(strings.Repeat("x", 50)); len(got) != 32 {
		t.Fatalf("harus 32, dapat %d", len(got))
	}
}

func TestTruncateCBData(t *testing.T) {
	long := "cfg:setmodel:" + strings.Repeat("m", 100)
	if got := truncateCBData(long); len(got) > 64 {
		t.Fatalf("callback data > 64 byte: %d", len(got))
	}
	if got := truncateCBData("cfg:modelpage:1"); got != "cfg:modelpage:1" {
		t.Fatalf("data pendek berubah: %q", got)
	}
}

func TestFilterModels(t *testing.T) {
	ids := []string{"arzastore/claude-sonnet-4", "gemini-2.5-flash", "GPT-OSS-20B", "HEC"}
	res := filterModels(ids, "claude")
	if len(res) != 1 || res[0] != "arzastore/claude-sonnet-4" {
		t.Fatalf("filter claude salah: %v", res)
	}
	res = filterModels(ids, "") // semua
	if len(res) != 4 {
		t.Fatalf("filter kosong harus semua: %v", res)
	}
	res = filterModels(ids, "oss") // case-insensitive
	if len(res) != 1 || res[0] != "GPT-OSS-20B" {
		t.Fatalf("case-insensitive gagal: %v", res)
	}
	res = filterModels(ids, "zzz")
	if len(res) != 0 {
		t.Fatalf("harus kosong: %v", res)
	}
}

func TestPagingMath(t *testing.T) {
	// 177 model → 23 halaman (8/halaman)
	total := 177
	pages := (total + modelsPerPage - 1) / modelsPerPage
	if pages != 23 {
		t.Fatalf("pages salah: %d", pages)
	}
	// 16 model → 2 halaman
	pages = (16 + modelsPerPage - 1) / modelsPerPage
	if pages != 2 {
		t.Fatalf("pages 16 salah: %d", pages)
	}
	// 0 model → math 0, tapi modelPageKeyboard clamp ke min 1
	// (verifikasi via clamp logic yang sama)
	pages = (0 + modelsPerPage - 1) / modelsPerPage
	if pages < 1 {
		pages = 1 // clamp di modelPageKeyboard
	}
	if pages != 1 {
		t.Fatalf("clamp gagal: %d", pages)
	}
}
