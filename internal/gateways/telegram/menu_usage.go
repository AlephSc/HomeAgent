package telegram

// menu_usage.go — Tombol "📊 Usage" di /menu (G12): pemakaian resource
// host + container Docker + top proses, murni baca (/proc, docker stats
// --no-stream, ps). Tanpa aksi, tanpa LLM — instan.
//
// Hardening:
//   - exec lewat runOutputCtx (CommandContext timeout wajib, pola H-C)
//   - docker stats --no-stream (bukan streaming)
//   - output dipotong rune-safe (pesan Telegram ~4096 char)

import (
	"fmt"
	"os"
	"strings"
	"time"
)

const usageMaxMsg = 3800 // batas aman pesan Telegram

// usageText — susun laporan resource.
func (g *Gateway) usageText() string {
	var b strings.Builder
	b.WriteString("📊 *Usage Resource — Semua App*\n\n")

	// --- Host: RAM + load + uptime (baca /proc, tanpa exec) ---
	b.WriteString(hostUsage())
	b.WriteString("\n")

	// --- Disk root (df, 1 baris) ---
	if out, err := runOutput(15*time.Second, "sh", "-c", "df -h / | tail -1 | awk '{print \"💾 Disk /: \" $3 \" / \" $2 \" (\" $5 \" terpakai)\"}'"); err == nil {
		b.WriteString(strings.TrimSpace(out) + "\n")
	}

	// --- Container Docker ---
	if out, err := runOutput(20*time.Second, "docker", "stats", "--no-stream",
		"--format", "{{.Name}}\t{{.CPUPerc}}\t{{.MemUsage}}\t{{.MemPerc}}"); err == nil {
		lines := strings.Split(strings.TrimSpace(out), "\n")
		if len(lines) > 0 && lines[0] != "" {
			b.WriteString("\n🐳 *Container Docker:*\n```\n")
			b.WriteString(fmt.Sprintf("%-16s %8s %16s %7s\n", "NAMA", "CPU", "MEM", "MEM%"))
			for _, ln := range lines {
				p := strings.Split(ln, "\t")
				if len(p) == 4 {
					b.WriteString(fmt.Sprintf("%-16s %8s %16s %7s\n", trunc(p[0], 16), p[1], p[2], p[3]))
				}
			}
			b.WriteString("```")
		}
	} else {
		b.WriteString("\n🐳 Docker: tidak dapat membaca stats (docker tidak jalan?)\n")
	}

	// --- Top proses host (CPU) ---
	if out, err := runOutput(15*time.Second, "sh", "-c",
		"ps -eo comm,pcpu,pmem --sort=-pcpu | head -8"); err == nil {
		b.WriteString("\n⚙️ *Top Proses (CPU):*\n```\n" + strings.TrimRight(out, "\n") + "\n```")
	}

	s := b.String()
	r := []rune(s)
	if len(r) > usageMaxMsg {
		s = string(r[:usageMaxMsg]) + "\n…(dipotong)"
	}
	return s
}

// hostUsage — RAM/load/uptime dari /proc (murah, tanpa exec).
func hostUsage() string {
	var b strings.Builder
	// meminfo
	if data, err := os.ReadFile("/proc/meminfo"); err == nil {
		var totalKB, availKB uint64
		for _, ln := range strings.Split(string(data), "\n") {
			f := strings.Fields(ln)
			if len(f) < 2 {
				continue
			}
			var v uint64
			fmt.Sscanf(f[1], "%d", &v)
			switch f[0] {
			case "MemTotal:":
				totalKB = v
			case "MemAvailable:":
				availKB = v
			}
		}
		if totalKB > 0 {
			used := totalKB - availKB
			pct := used * 100 / totalKB
			fmt.Fprintf(&b, "🧠 RAM host: %dM / %dM (%d%%)\n", used/1024, totalKB/1024, pct)
		}
	}
	// loadavg
	if data, err := os.ReadFile("/proc/loadavg"); err == nil {
		f := strings.Fields(string(data))
		if len(f) >= 3 {
			b.WriteString("⚙️ Load: " + f[0] + " " + f[1] + " " + f[2] + "\n")
		}
	}
	// uptime
	if data, err := os.ReadFile("/proc/uptime"); err == nil {
		var up float64
		if _, err2 := fmt.Sscanf(strings.TrimSpace(string(data)), "%f", &up); err2 == nil {
			d := int(up) / 86400
			h := (int(up) % 86400) / 3600
			fmt.Fprintf(&b, "⏱ Uptime: %dj %dh", d, h)
		}
	}
	return b.String()
}

// trunc potong string byte-safe sederhana utk kolom nama.
func trunc(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}
