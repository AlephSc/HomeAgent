// Package agent — F6a: auto context compacting (ambang 64k configurable).
// Riwayat > budget → pesan lama diringkas jadi running summary (async, user tak menunggu).
package agent

import (
	"context"
	"log"
	"strings"

	"aleph-agent/internal/llm"
)

// tokenEstimate — estimasi token kasar (chars/4; cukup akurat utk heuristik).
func tokenEstimate(s string) int { return len(s) / 4 }

// CompactContext menyusun pesan user + konteks: [summary] + [verbatim ≤ budget].
// Dipanggil sebelum Handle; running summary lama diambil dari store.
// contextBudget dikirim dari pemanggil (snapshot mutex — hindari race dgn SetContextBudget web UI).
func (a *Agent) CompactContext(chatID int64, userMsg string, hist []string, contextBudget int) (summary, verbatimMsg string) {
	// anggaran: budget total - system prompt - jawaban (maks 4k) - margin 2k
	budget := contextBudget - (a.promptTokens()) - 6144
	if budget < 4096 {
		budget = 4096
	}

	// ambil summary lama
	summary = ""
	if a.Summarizer != nil {
		if s, ok := a.Summarizer.GetSummary(chatID); ok {
			summary = s
		}
	}

	// kumpulkan riwayat dari TERBARU ke lama sampai budget habis
	var kept []string // verbatim lama→baru
	used := tokenEstimate(userMsg) + 128
	for i := len(hist) - 1; i >= 0; i-- {
		t := tokenEstimate(hist[i]) + 24
		if used+t > budget {
			break
		}
		used += t
		kept = append([]string{hist[i]}, kept...) // prepend
	}
	// pesan yang TIDAK muat → kandidat ringkasan
	var overflow []string
	if len(kept) > 0 {
		// cari index kept pertama dalam hist
		firstKept := hist[len(hist)-len(kept)]
		for _, h := range hist {
			if h == firstKept {
				break
			}
			overflow = append(overflow, h)
		}
	} else {
		overflow = hist // semuanya overflow
	}

	// simpan verbatim yang muat utk HandleChat
	a.lastKeptHist = kept

	// kalau ada overflow, jadwalk ringkas async (pakai summary lama + overflow)
	if len(overflow) > 0 && a.Summarizer != nil {
		go a.updateSummary(chatID, summary, overflow)
	} else if len(hist) > 20 && a.Summarizer != nil {
		// G4b: summary PROAKTIF — sesi panjang tapi semuanya masih muat verbatim.
		// Ringkas pesan yang BELUM tercakup ringkasan (de-dupe via summarizedCount)
		// supaya saat budget penuh, summary sudah siap dan tidak hilang mendadak.
		a.summaryMu.Lock()
		done := a.summarizedCount[chatID]
		a.summaryMu.Unlock()
		old := len(hist) - 10
		if old > done {
			fresh := hist[done:old]
			a.summaryMu.Lock()
			a.summarizedCount[chatID] = old
			a.summaryMu.Unlock()
			go a.updateSummary(chatID, summary, fresh)
		}
	}

	// verbatimMsg: userMsg + verbatim yang muat
	// G4a: framing eksplisit — model harus memperlakukan blok ini sebagai INGATANNYA SENDIRI.
	verbatimMsg = userMsg
	if len(kept) > 0 {
		verbatimMsg = "⬇ INGATAN PERCAKAPAN ANDA sendiri dengan user ini (lama→baru). " +
			"Anda SUDAH mengatakannya dan user SUDAH menjawabnya — ini bukan kutipan orang lain:\n" +
			strings.Join(kept, "\n") + "\n⬆ akhir ingatan.\n\nPesan user sekarang: " + userMsg
	}
	if summary != "" {
		verbatimMsg = "⬇ RINGKASAN ingatan Anda atas percakapan yang lebih lama dengan user ini " +
			"(tetap ingatan Anda, bukan info eksternal):\n" + summary + "\n⬆\n\n" + verbatimMsg
	}
	return summary, verbatimMsg
}

// updateSummary merge summary lama + pesan overflow jadi ringkasan baru (async).
func (a *Agent) updateSummary(chatID int64, oldSummary string, overflow []string) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*1000*1000*1000) // 90s
	defer cancel()
	prompt := "Ringkas percakapan berikut menjadi poin-poin penting (maks 200 kata, Bahasa Indonesia). "
	if oldSummary != "" {
		prompt += "Gabungkan dengan ringkasan lama ini:\n---\n" + oldSummary + "\n---\n"
	}
	prompt += "\nPercakapan:\n---\n" + strings.Join(overflow, "\n") + "\n---\nKeluarkan HANYA ringkasan."
	msgs := []llm.Message{
		{Role: "system", Content: "Kamu pemadat konteks. Ringkas tanpa kehilangan fakta penting: angka, nama, keputusan, hasil."},
		{Role: "user", Content: prompt},
	}
	msg, err := a.LLM.Chat(ctx, msgs, nil)
	if err != nil {
		log.Printf("[compact] gagal ringkas chat %d: %v", chatID, err)
		return
	}
	s := strings.TrimSpace(msg.Content)
	if s != "" && a.Summarizer != nil {
		a.Summarizer.SetSummary(chatID, s)
		log.Printf("[compact] summary chat %d diperbarui (%d tok est)", chatID, tokenEstimate(s))
	}
}

// promptTokens — estimasi token system prompt (dihitung sekali, di-cache).
func (a *Agent) promptTokens() int {
	if a.promptTokCache == 0 {
		a.promptTokCache = tokenEstimate(a.Prompt) + 512 // margin tools defs
	}
	return a.promptTokCache
}
