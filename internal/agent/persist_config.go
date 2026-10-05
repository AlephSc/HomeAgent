package agent

// persist_config.go — B4: persist config runtime (overlay).
//
// Perubahan config via menu Telegram / Web UI / /model ditulis atomik ke
// data/agent-config.json. Saat start, bot load config.yaml lalu menimpanya
// dengan overlay ini (config.yaml tidak pernah disentuh bot).
//
// Format:
//
//	{"context_budget":16384,"max_rounds":30,"timeout_sec":180,"model":"...","max_memory_mb":200,"max_subagents":2}
//
// Hardening: tulis atomik (tmp+rename), perm 0600, validasi nilai sebelum apply,
// file rusak → diabaikan (fallback config.yaml) + log warning.

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
)

// PersistedConfig — field yang di-persist (0 = pakai default config.yaml).
type PersistedConfig struct {
	ContextBudget int    `json:"context_budget,omitempty"`
	MaxRounds     int    `json:"max_rounds,omitempty"`
	TimeoutSec    int    `json:"timeout_sec,omitempty"`
	Model         string `json:"model,omitempty"`
	MaxMemoryMB   int    `json:"max_memory_mb,omitempty"`
	MaxSubAgents  int    `json:"max_subagents,omitempty"`
}

// persistPath — path file overlay (dataDir kosong → persist off).
func persistPath(dataDir string) string {
	if dataDir == "" {
		return ""
	}
	return filepath.Join(dataDir, "agent-config.json")
}

// SavePersist tulis snapshot config saat ini ke overlay (atomik).
func (a *Agent) SavePersist() {
	path := persistPath(a.DataDir)
	if path == "" {
		return
	}
	a.cfgMu.Lock()
	pc := PersistedConfig{
		ContextBudget: a.ContextBudget,
		MaxRounds:     a.MaxRounds,
		Model:         a.GetModel(),
		MaxMemoryMB:   a.maxMemoryMB,
		MaxSubAgents:  a.maxSubAgents,
	}
	if a.LLM != nil && a.LLM.HTTP != nil {
		pc.TimeoutSec = int(a.LLM.HTTP.Timeout.Seconds())
	}
	a.cfgMu.Unlock()

	b, err := json.MarshalIndent(pc, "", "  ")
	if err != nil {
		log.Printf("[persist] marshal gagal: %v", err)
		return
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		log.Printf("[persist] tulis gagal: %v", err)
		return
	}
	if err := os.Rename(tmp, path); err != nil {
		log.Printf("[persist] rename gagal: %v", err)
		_ = os.Remove(tmp)
	}
}

// LoadPersist baca overlay (tanpa apply). Return zero-value jika tidak ada/rusak.
func (a *Agent) LoadPersist() PersistedConfig {
	path := persistPath(a.DataDir)
	if path == "" {
		return PersistedConfig{}
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return PersistedConfig{} // belum ada — normal
	}
	var pc PersistedConfig
	if err := json.Unmarshal(b, &pc); err != nil {
		log.Printf("[persist] overlay rusak, diabaikan: %v", err)
		return PersistedConfig{}
	}
	return pc
}

// ApplyPersist terapkan overlay ke agent (panggil sekali saat start, setelah
// agent siap + sebelum gateway melayani request). Field 0 = skip (pakai default).
func (a *Agent) ApplyPersist() {
	pc := a.LoadPersist()
	applied := []string{}
	if pc.ContextBudget > 0 {
		if err := a.SetContextBudget(pc.ContextBudget); err == nil {
			applied = append(applied, "context")
		}
	}
	if pc.MaxRounds > 0 {
		if err := a.SetMaxRounds(pc.MaxRounds); err == nil {
			applied = append(applied, "rounds")
		}
	}
	if pc.TimeoutSec > 0 {
		if err := a.SetTimeout(pc.TimeoutSec); err == nil {
			applied = append(applied, "timeout")
		}
	}
	if pc.Model != "" {
		a.SetModel(pc.Model)
		applied = append(applied, "model")
	}
	if pc.MaxMemoryMB > 0 {
		if err := a.SetMaxMemoryMB(pc.MaxMemoryMB); err == nil {
			applied = append(applied, "maxmem")
		}
	}
	if pc.MaxSubAgents > 0 {
		if err := a.SetMaxSubAgents(pc.MaxSubAgents); err == nil {
			applied = append(applied, "maxsub")
		}
	}
	if len(applied) > 0 {
		log.Printf("[persist] config dipulihkan: %v", applied)
	}
}

// ClearPersist hapus overlay → kembali ke config.yaml saat restart berikutnya.
func (a *Agent) ClearPersist() {
	path := persistPath(a.DataDir)
	if path == "" {
		return
	}
	_ = os.Remove(path)
}
