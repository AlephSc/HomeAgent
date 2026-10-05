package agent

// G3 — Runtime config: ubah ContextBudget/MaxRounds/Model/Timeout saat bot hidup.
// Aman antar-goroutine (telegram gateway + web UI) via cfgMu.

import (
	"fmt"
	"math"
	"runtime"
	"runtime/debug"
	"time"
)

// SetContextBudget ubah budget verbatim token (min 2k, maks 128k).
func (a *Agent) SetContextBudget(n int) error {
	if n < 2048 || n > 1048576 { // G13: hingga 1M token (server besar)
		return fmt.Errorf("di luar rentang (2048-1048576)")
	}
	a.cfgMu.Lock()
	a.ContextBudget = n
	a.cfgMu.Unlock()
	return nil
}

// GetContextBudget baca budget saat ini.
func (a *Agent) GetContextBudget() int {
	a.cfgMu.Lock()
	defer a.cfgMu.Unlock()
	return a.ContextBudget
}

// ContextBudgetSnapshot — alias pembacaan internal (dipakai HandleChat; sinkron dgn cfgMu).
func (a *Agent) ContextBudgetSnapshot() int { return a.GetContextBudget() }

// MaxRoundsSnapshot — baca MaxRounds dengan mutex (dipakai ReAct loop).
func (a *Agent) MaxRoundsSnapshot() int { return a.GetMaxRounds() }

// SetMaxRounds ubah budget langkah tool per tugas (min 1, maks 100).
func (a *Agent) SetMaxRounds(n int) error {
	if n < 1 || n > 500 { // G13: server besar butuh budget lebih
		return fmt.Errorf("di luar rentang (1-500)")
	}
	a.cfgMu.Lock()
	a.MaxRounds = n
	a.cfgMu.Unlock()
	return nil
}

// GetMaxRounds baca nilai saat ini.
func (a *Agent) GetMaxRounds() int {
	a.cfgMu.Lock()
	defer a.cfgMu.Unlock()
	return a.MaxRounds
}

// SetModel ganti model LLM (runtime, sama seperti /model).
func (a *Agent) SetModel(model string) {
	if a.LLM == nil || model == "" {
		return
	}
	a.cfgMu.Lock()
	a.LLM.SetModel(model)
	a.cfgMu.Unlock()
}

// SetTimeout ganti timeout HTTP per request LLM (detik, 30-600).
func (a *Agent) SetTimeout(sec int) error {
	if sec < 30 || sec > 3600 { // G13: hingga 1 jam (model reasoning lambat)
		return fmt.Errorf("di luar rentang (30-3600 dtk)")
	}
	if a.LLM == nil || a.LLM.HTTP == nil {
		return fmt.Errorf("LLM belum aktif")
	}
	a.cfgMu.Lock()
	a.LLM.HTTP.Timeout = time.Duration(sec) * time.Second
	a.cfgMu.Unlock()
	return nil
}

// GetTimeout baca timeout saat ini (detik).
func (a *Agent) GetTimeout() int {
	if a.LLM == nil || a.LLM.HTTP == nil {
		return 0
	}
	a.cfgMu.Lock()
	defer a.cfgMu.Unlock()
	return int(a.LLM.HTTP.Timeout / time.Second)
}

// GetModel baca model aktif.
func (a *Agent) GetModel() string {
	if a.LLM == nil {
		return ""
	}
	a.cfgMu.Lock()
	defer a.cfgMu.Unlock()
	return a.LLM.Model()
}


// ===== B1: Max Memory (soft limit, MB) =====

// SetMaxMemoryMB set soft limit RAM proses (64–1024 MB; 0 = off).
// Go runtime: debug.SetMemoryLimit (soft — GC lebih agresif mendekati limit).
func (a *Agent) SetMaxMemoryMB(mb int) error {
	if mb != 0 && (mb < 64 || mb > 65536) { // G13: hingga 64 GB (scale-up)
		return fmt.Errorf("di luar rentang (64-65536 MB, 0=off)")
	}
	a.cfgMu.Lock()
	a.maxMemoryMB = mb
	a.cfgMu.Unlock()
	if mb > 0 {
		debug.SetMemoryLimit(int64(mb) << 20)
	} else {
		debug.SetMemoryLimit(math.MaxInt64) // off
	}
	return nil
}

// GetMaxMemoryMB baca soft limit aktif (0 = off).
func (a *Agent) GetMaxMemoryMB() int {
	a.cfgMu.Lock()
	defer a.cfgMu.Unlock()
	return a.maxMemoryMB
}

// MemoryPressure — persentase pemakaian terhadap limit (0 jika off).
// Dibaca dari runtime.MemStats (HeapAlloc + stack approx via Sys lebih konservatif:
// pakai Sys = total memori yang diminta dari OS).
func (a *Agent) MemoryPressure() float64 {
	mb := a.GetMaxMemoryMB()
	if mb <= 0 {
		return 0
	}
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	return float64(ms.Sys) / (float64(mb) * 1024 * 1024) * 100
}

// ===== B2: Max Sub-Agent (1-3) =====

// SetMaxSubAgents ubah batas delegasi paralel (1-3; default 2).
// Semaphore sub-agent dibangun ulang — aman karena hanya menambah kapasitas
// menunggu slot kosong; tidak ada goroutine yang "tertinggal" di semaphore lama.
func (a *Agent) SetMaxSubAgents(n int) error {
	if n < 1 || n > 16 { // G13: server besar bisa lebih banyak paralel
		return fmt.Errorf("di luar rentang (1-16)")
	}
	a.cfgMu.Lock()
	a.maxSubAgents = n
	a.cfgMu.Unlock()
	ResizeSubAgentSem(n)
	return nil
}

// GetMaxSubAgents baca batas delegasi aktif (0 = belum diset → default 2).
func (a *Agent) GetMaxSubAgents() int {
	a.cfgMu.Lock()
	defer a.cfgMu.Unlock()
	return a.maxSubAgents
}

// SubAgentLimit — nilai efektif (default 2 jika belum diset).
func (a *Agent) SubAgentLimit() int {
	if n := a.GetMaxSubAgents(); n > 0 {
		return n
	}
	return 2
}
