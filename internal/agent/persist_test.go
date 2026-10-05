package agent

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestPersistRoundTrip(t *testing.T) {
	dir := t.TempDir()
	a := &Agent{DataDir: dir}
	a.ContextBudget = 16384
	a.MaxRounds = 30
	a.maxMemoryMB = 200
	a.maxSubAgents = 3

	a.SavePersist()

	// file ada & 0600?
	p := filepath.Join(dir, "agent-config.json")
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal("overlay tidak tertulis:", err)
	}
	if runtime.GOOS != "windows" && fi.Mode().Perm() != 0o600 {
		t.Fatalf("perm harus 0600, dapat %v", fi.Mode().Perm())
	}

	// load balikan nilai sama?
	pc := a.LoadPersist()
	if pc.ContextBudget != 16384 || pc.MaxRounds != 30 || pc.MaxMemoryMB != 200 || pc.MaxSubAgents != 3 {
		t.Fatalf("round-trip salah: %+v", pc)
	}

	// ClearPersist menghapus
	a.ClearPersist()
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Fatal("ClearPersist tidak menghapus file")
	}
}

func TestPersistCorruptFile(t *testing.T) {
	dir := t.TempDir()
	a := &Agent{DataDir: dir}
	p := filepath.Join(dir, "agent-config.json")
	os.WriteFile(p, []byte("bukan json!!!"), 0o600)
	pc := a.LoadPersist() // harus zero-value tanpa panic
	if pc.ContextBudget != 0 || pc.Model != "" {
		t.Fatalf("file rusak harus zero: %+v", pc)
	}
}

func TestMaxSubAgentsValidation(t *testing.T) {
	a := &Agent{}
	if err := a.SetMaxSubAgents(17); err == nil { // G13: batas 16
		t.Fatal("n=17 harus ditolak")
	}
	if err := a.SetMaxSubAgents(0); err == nil {
		t.Fatal("n=0 harus ditolak")
	}
	if err := a.SetMaxSubAgents(3); err != nil {
		t.Fatal("n=3 harus diterima:", err)
	}
	if a.SubAgentLimit() != 3 {
		t.Fatalf("limit harus 3, dapat %d", a.SubAgentLimit())
	}
}

func TestMaxMemoryValidation(t *testing.T) {
	a := &Agent{}
	if err := a.SetMaxMemoryMB(10); err == nil {
		t.Fatal("10MB harus ditolak")
	}
	if err := a.SetMaxMemoryMB(65537); err == nil { // G13: batas 64GB
		t.Fatal("65537MB harus ditolak")
	}
	if err := a.SetMaxMemoryMB(200); err != nil {
		t.Fatal("200MB harus diterima:", err)
	}
	if a.GetMaxMemoryMB() != 200 {
		t.Fatal("getter salah")
	}
	if a.MemoryPressure() <= 0 {
		t.Fatal("memory pressure harus >0 saat limit aktif")
	}
}
