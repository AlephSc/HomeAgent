package agent

// subagent.go — G7b: delegasi sub-tugas ke "agent kedua".
//
// Agent utama (via tool delegate_task) melontarkan sub-tugas yang dikerjakan
// loop ReAct TERPISAH dengan konteks bersih (tanpa riwayat percakapan user).
// Hasil akhir dikembalikan sebagai output tool.
//
// Batas keras (hardening):
//   - Maks 2 sub-agent paralel (semaphore) — ramah N2600.
//   - Deadline wajib (default 300 dtk) via context.
//   - Output dipotong rune-safe (MaksOutput).
//   - Tool subset disaring: sub-agent TIDAK mendapat delegate_task / exec_command /
//     service_control / write_file (anti-rekursi + least privilege).

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
)

// MaksOutput batas panjang hasil sub-agent (rune-safe dipotong pemanggil).
const MaksOutput = 8000

// MaksParalel batas sub-agent bersamaan.
const MaksParalel = 2

// sem — semaphore sub-agent paralel (kapasitas bisa diubah runtime via
// ResizeSubAgentSem; semMu melindungi penggantian channel).
var (
	sem    = make(chan struct{}, MaksParalel)
	semMu  sync.RWMutex
)

// ResizeSubAgentSem ganti kapasitas semaphore (B2). Goroutine yang sedang
// memegang slot semaphore lama tetap release ke channel lama (tidak hilang).
func ResizeSubAgentSem(n int) {
	if n < 1 || n > 16 {
		return
	}
	semMu.Lock()
	sem = make(chan struct{}, n)
	semMu.Unlock()
}

// acquireSem ambil slot (hormati ctx & timeout antre); releaseSlot untuk lepas.
func acquireSem(ctx context.Context, wait time.Duration) (release func(), err error) {
	semMu.RLock()
	ch := sem
	semMu.RUnlock()
	select {
	case ch <- struct{}{}:
		return func() { <-ch }, nil
	case <-ctx.Done():
		return nil, fmt.Errorf("context selesai saat antre delegasi")
	case <-time.After(wait):
		return nil, fmt.Errorf("antrean delegasi penuh (maks %d paralel) — coba lagi nanti", cap(ch))
	}
}

// Delegate implementasi untuk *Agent.
// Sub-agent berbagi LLM client & prompt dasar, tapi Session nil (konteks bersih).
func (a *Agent) Delegate(ctx context.Context, task, hint string, allowedTools []string, maxRounds, timeoutSec int) (string, error) {
	if strings.TrimSpace(task) == "" {
		return "", fmt.Errorf("tugas kosong")
	}
	if maxRounds <= 0 || maxRounds > 30 {
		maxRounds = 15
	}
	if timeoutSec <= 0 || timeoutSec > 600 {
		timeoutSec = 300
	}

	// B1: proteksi memori — tolak delegasi saat RAM >= 85% limit
	if p := a.MemoryPressure(); p >= 85 {
		return "", fmt.Errorf("memori %.0f%% dari limit — delegasi ditunda; kurangi Max Memory atau beban dulu", p)
	}
	// semaphore anti overload (dengan hormati deadline context)
	release, serr := acquireSem(ctx, 30*time.Second)
	if serr != nil {
		return "", serr
	}
	defer release()

	// clone registry dengan tool subset (anti-rekursi: delegate_task tidak termasuk)
	sub := &Agent{
		LLM:        a.LLM, // berbagi client (mutex-protected)
		Tools:      a.Tools.CloneAllowed(allowedTools),
		Prompt:     subPrompt(a.Prompt, task, hint),
		MaxRounds:  maxRounds,
		ContextBudget: 0, // tanpa compacting — sub-tugas pendek
		summarizedCount: map[int64]int{},
	}

	cctx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSec)*time.Second)
	defer cancel()

	msg, err := sub.Handle(cctx, task)
	if err != nil {
		return "", fmt.Errorf("sub-agent gagal: %w", err)
	}
	return msg, nil
}

// subPrompt susun system prompt sub-agent: prompt dasar + framing tugas.
func subPrompt(base, task, hint string) string {
	var b strings.Builder
	b.WriteString(strings.TrimSpace(base))
	b.WriteString("\n\nMODE SUB-AGENT: Kamu mengerjakan SATU sub-tugas yang didelegasikan oleh agent utama. ")
	b.WriteString("Fokus selesaikan tugas, jawab ringkas dan padat (maks ~8000 karakter) — jawabanmu DITERUSKAN utuh ke agent utama. ")
	b.WriteString("Jangan menyapa atau bertanya balik; asumsi terbaik jika ada yang kurang jelas.")
	if hint != "" {
		b.WriteString("\n\nKONTEKS PENDUKUNG:\n" + hint)
	}
	b.WriteString("\n\nSUB-TUGAS: " + task)
	return b.String()
}

// TruncateOutput potong hasil rune-safe + catatan.
func TruncateOutput(s string) string {
	r := []rune(s)
	if len(r) <= MaksOutput {
		return s
	}
	return string(r[:MaksOutput]) + "\n\n…(hasil dipotong — minta agent utama melakukan delegasi lanjutan dengan sub-tugas lebih sempit)"
}
