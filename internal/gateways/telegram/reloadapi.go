package telegram

// reloadapi.go — G11 H-3/H-4: reload endpoint & API key + admin IDs tanpa restart.
//
// /reloadapi (admin-only): baca ulang config.yaml → LLM klien baru (health-check
// dulu via /models); gagal → tetap pakai yang lama. Admin IDs ikut di-refresh.
// Telegram token tetap butuh restart (koneksi bot) — tidak dijanjikan hot.

import (
	"context"
	"fmt"
	"time"

	"aleph-agent/internal/config"
	"aleph-agent/internal/llm"
)

// handleReloadAPI — command /reloadapi.
func (g *Gateway) handleReloadAPI(chatID int64) {
	oldBase := g.cfg.LLM.BaseURL
	oldKey := g.cfg.LLM.APIKey

	// baca ulang config dari disk
	cfgNew, err := config.Load(g.cfgPath)
	if err != nil {
		g.reply(chatID, "⚠️ Gagal membaca config.yaml: "+err.Error()+"\nEndpoint lama tetap dipakai.")
		return
	}
	newBase := cfgNew.LLM.BaseURL
	newKey := cfgNew.LLM.APIKey
	if newBase == "" || newKey == "" {
		g.reply(chatID, "⚠️ base_url/api_key kosong di config.yaml — endpoint lama tetap dipakai.")
		return
	}
	if newBase == oldBase && newKey == oldKey {
		g.reply(chatID, "ℹ️ Tidak ada perubahan — endpoint & API key sudah sama dengan config.yaml.")
		return
	}

	// health-check endpoint baru (GET /models)
	probe := llm.New(newBase, newKey, g.activeModel())
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, _, err := probe.ListModels(ctx, ""); err != nil {
		g.reply(chatID, fmt.Sprintf("❌ Endpoint baru GAGAL health-check (%v).\nEndpoint lama tetap dipakai — bot tidak terganggu.", err))
		return
	}

	// swap klien LLM (agent-level, race-safe)
	if !g.agent.SwapLLMClient(probe) {
		g.reply(chatID, "⚠️ Swap klien gagal — endpoint lama tetap dipakai.")
		return
	}

	// refresh admin IDs
	g.cfg.Admins = cfgNew.Admins

	g.reply(chatID, fmt.Sprintf("✅ Endpoint LLM diperbarui:\n%s → %s\nModel aktif dipertahankan: %s",
		maskURL(oldBase), maskURL(newBase), g.activeModel()))
}

// maskURL sembunyikan path sensitif (hanya scheme://host:port).
func maskURL(u string) string {
	slashes := 0
	for i := 0; i < len(u); i++ {
		if u[i] == '/' {
			slashes++
			if slashes == 3 {
				return u[:i]
			}
		}
	}
	return u
}

// SetCfgPath — path config file (dipanggil main.go).
func (g *Gateway) SetCfgPath(p string) { g.cfgPath = p }
