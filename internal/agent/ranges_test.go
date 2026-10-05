package agent

import "testing"

func TestWidenedRanges(t *testing.T) {
	a := &Agent{}
	// context hingga 1M
	if err := a.SetContextBudget(262144); err != nil {
		t.Fatal("256k harus diterima:", err)
	}
	if err := a.SetContextBudget(2 << 20); err == nil {
		t.Fatal(">1M harus ditolak")
	}
	// rounds hingga 500
	if err := a.SetMaxRounds(300); err != nil {
		t.Fatal("300 harus diterima:", err)
	}
	if err := a.SetMaxRounds(501); err == nil {
		t.Fatal("501 harus ditolak")
	}
	// timeout hingga 3600 — butuh LLM.HTTP aktif (validasi range dicek lebih dulu di sini via agent dgn LLM nil:
	// SetTimeout menolak "LLM belum aktif" SEBELUM range → test range murni via batas atas mem/sub/context di atas.
	_ = a
	// memory hingga 64GB
	if err := a.SetMaxMemoryMB(8192); err != nil {
		t.Fatal("8GB harus diterima:", err)
	}
	if err := a.SetMaxMemoryMB(65537); err == nil {
		t.Fatal(">64GB harus ditolak")
	}
	// sub-agent hingga 16
	if err := a.SetMaxSubAgents(8); err != nil {
		t.Fatal("8 harus diterima:", err)
	}
	if err := a.SetMaxSubAgents(17); err == nil {
		t.Fatal("17 harus ditolak")
	}
	// nilai lama tetap valid
	if err := a.SetMaxSubAgents(2); err != nil {
		t.Fatal("2 harus diterima:", err)
	}
	if err := a.SetMaxMemoryMB(0); err != nil {
		t.Fatal("0=off harus diterima:", err)
	}
}
