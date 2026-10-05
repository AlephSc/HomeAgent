package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSelfToolSaveAndRun(t *testing.T) {
	dir := t.TempDir()
	reg := NewRegistry()
	if err := InitSelfTools(dir, reg); err != nil {
		t.Fatal(err)
	}
	defer func() { selfStore = nil }()
	def := SelfToolDef{Name: "x_echo", Description: "echo test", Params: map[string]string{"msg": "pesan"}, Lang: "bash", TimeoutSec: 10}
	script := "echo -n hallo\n"
	if err := selfStore.SaveDef(def, script, reg, map[string]bool{}); err != nil {
		t.Fatal("save gagal:", err)
	}
	if _, ok := reg.Get("x_echo"); !ok {
		t.Fatal("x_echo tidak terdaftar")
	}
	tool, _ := reg.Get("x_echo")
	res := tool.Fn(context.Background(), `{}`)
	if res.Err != "" || !strings.Contains(res.Output, "hallo") {
		t.Fatalf("run salah: out=%q err=%q", res.Output, res.Err)
	}
	if _, err := os.Stat(filepath.Join(dir, "tools", "manifest.json")); err != nil {
		t.Fatal("manifest tidak ada")
	}
	// simulasi restart
	reg2 := NewRegistry()
	if err := InitSelfTools(dir, reg2); err != nil {
		t.Fatal(err)
	}
	if _, ok := reg2.Get("x_echo"); !ok {
		t.Fatal("x_echo tidak di-load saat start")
	}
}

func TestSelfToolValidation(t *testing.T) {
	dir := t.TempDir()
	reg := NewRegistry()
	_ = InitSelfTools(dir, reg)
	defer func() { selfStore = nil }()
	cases := []struct {
		nm, script, lang string
		wantFail         bool
	}{
		{"ok", "#!/bin/bash\necho hi\n", "bash", false},
		{"A!", "#!/bin/bash\necho\n", "bash", true},
		{"ok", "dd if=/dev/zero of=x\n", "bash", true},
		{"ok", "def broken(:\n", "python", true},
		{"ok", "if true;then\n", "bash", true},
		{"exec_command", "#!/bin/bash\necho\n", "bash", true},
	}
	for _, c := range cases {
		def := SelfToolDef{Name: c.nm, Description: "d", Lang: c.lang}
		err := selfStore.SaveDef(def, c.script, reg, map[string]bool{"x_exec_command": true})
		if c.wantFail && err == nil {
			t.Errorf("case %q harus gagal", c.nm)
		}
		if !c.wantFail && err != nil {
			t.Errorf("case %q harus sukses: %v", c.nm, err)
		}
	}
}

func TestSelfToolQuota(t *testing.T) {
	dir := t.TempDir()
	reg := NewRegistry()
	_ = InitSelfTools(dir, reg)
	defer func() { selfStore = nil }()
	for i := 0; i < maxToolKustom; i++ {
		def := SelfToolDef{Name: fmt.Sprintf("x_tool%d", i), Description: "d", Lang: "bash"}
		if err := selfStore.SaveDef(def, "#!/bin/bash\necho ok\n", reg, map[string]bool{}); err != nil {
			t.Fatalf("save #%d gagal: %v", i, err)
		}
	}
	def := SelfToolDef{Name: "x_over", Description: "d", Lang: "bash"}
	if err := selfStore.SaveDef(def, "#!/bin/bash\necho\n", reg, map[string]bool{}); err == nil {
		t.Fatal("quota harus ditolak")
	}
}

func TestSelfToolToggleDelete(t *testing.T) {
	dir := t.TempDir()
	reg := NewRegistry()
	_ = InitSelfTools(dir, reg)
	defer func() { selfStore = nil }()
	def := SelfToolDef{Name: "x_t", Description: "d", Lang: "bash"}
	_ = selfStore.SaveDef(def, "#!/bin/bash\necho ok\n", reg, map[string]bool{})
	on, err := selfStore.Toggle("x_t", reg)
	if err != nil || on {
		t.Fatal("toggle off gagal")
	}
	tool, _ := reg.Get("x_t")
	res := tool.Fn(context.Background(), `{}`)
	if res.Err == "" {
		t.Fatal("tool disabled harus gagal saat dipanggil")
	}
	if err := selfStore.Delete("x_t", reg); err != nil {
		t.Fatal("delete gagal:", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "tools", "x_t.sh")); !os.IsNotExist(err) {
		t.Fatal("script tidak terhapus")
	}
}
