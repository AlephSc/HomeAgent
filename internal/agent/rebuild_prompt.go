package agent

// rebuild_prompt.go — G11 H-1: rebuild system prompt saat daftar tools berubah.
// Agent "sadar" tool baru (G10) tanpa restart. Race-safe via promptMu;
// HandleChat membaca PromptSnapshot().

import (
	"aleph-agent/internal/llm"
	"time"
)

// ToolsSummaryer — sumber ringkasan tools (dipenuhi *tools.Registry).
type ToolsSummaryer interface {
	Summary() string
}

// SetToolsRef — dipanggil main.go: sumber summary tools untuk rebuild.
func (a *Agent) SetToolsRef(t ToolsSummaryer) {
	a.toolsMu.Lock()
	a.toolsRef = t
	a.toolsMu.Unlock()
}

// PromptSnapshot — prompt aktif (race-safe).
func (a *Agent) PromptSnapshot() string {
	a.promptMu.Lock()
	defer a.promptMu.Unlock()
	if a.promptCur != "" {
		return a.promptCur
	}
	return a.Prompt // fallback field awal
}

// storePrompt internal.
func (a *Agent) storePrompt(p string) {
	a.promptMu.Lock()
	a.promptCur = p
	a.promptMu.Unlock()
}

// RebuildPrompt — susun ulang system prompt dari registry + extra saat ini.
// Dipanggil setelah tool kustom save/del/toggle (G10) & cron berubah.
func (a *Agent) RebuildPrompt() {
	a.cfgMu.Lock()
	server := a.ServerName
	extra := a.PromptExtra
	rounds := a.MaxRounds
	a.cfgMu.Unlock()
	a.toolsMu.Lock()
	ts := a.toolsRef
	a.toolsMu.Unlock()
	if ts == nil {
		return // belum ada sumber — jangan rusak prompt lama
	}
	summary := ts.Summary()
	if summary == "" {
		return
	}
	// refresh extra (skill/lesson/preferensi bisa berubah juga)
	p := BuildPromptExtra(server, summary, time.Now().Format("2006-01-02 15:04 MST"), rounds, extra)
	a.storePrompt(p)
}

// RefreshPromptExtra — panggil saat skill/lesson/preferensi berubah.
func (a *Agent) RefreshPromptExtra(extra string) {
	a.cfgMu.Lock()
	a.PromptExtra = extra
	a.cfgMu.Unlock()
	a.RebuildPrompt()
}


// SwapLLMClient — ganti klien LLM (endpoint/API key baru) tanpa restart.
// Race-safe; return false jika newClient nil. Model aktif dipertahankan.
func (a *Agent) SwapLLMClient(nc *llm.Client) bool {
	if nc == nil {
		return false
	}
	a.cfgMu.Lock()
	defer a.cfgMu.Unlock()
	if a.LLM != nil {
		nc.SetModel(a.LLM.Model())
	}
	a.LLM = nc
	return true
}
