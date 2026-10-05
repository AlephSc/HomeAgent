// Package chatlog — log percakapan lengkap ke file harian utk debugging.
// Format: <data_dir>/chat/2006-01-02.log — satu baris per event.
// Event: USER (pesan masuk), AGENT (jawaban akhir), TOOL (panggilan tool + hasil ringkas),
// PERM (keputusan izin), ERR (error agent/LLM).
package chatlog

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Logger chat harian. Aman dipakai antar-goroutine.
type Logger struct {
	mu  sync.Mutex
	dir string // <data>/chat
	day string
	f   *os.File
}

// New buat logger; dir = direktori data (file log di dir/chat/YYYY-MM-DD.log).
func New(dataDir string) *Logger {
	return &Logger{dir: filepath.Join(dataDir, "chat")}
}

// logf tulis satu baris (thread-safe, buka-tutup file per hari otomatis).
func (l *Logger) logf(format string, args ...interface{}) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	day := time.Now().Format("2006-01-02")
	if l.f == nil || l.day != day {
		if l.f != nil {
			l.f.Close()
		}
		if err := os.MkdirAll(l.dir, 0o755); err != nil {
			return
		}
		f, err := os.OpenFile(filepath.Join(l.dir, day+".log"),
			os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			return
		}
		l.f = f
		l.day = day
	}
	ts := time.Now().Format("15:04:05")
	fmt.Fprintf(l.f, "%s %s\n", ts, fmt.Sprintf(format, args...))
}

// User — pesan masuk dari user.
func (l *Logger) User(chatID int64, name, text string) {
	l.logf("USER   [%d/%s] %s", chatID, name, oneLine(text, 300))
}

// Agent — jawaban final agent.
func (l *Logger) Agent(chatID int64, text string, dur time.Duration) {
	l.logf("AGENT  [%d] (%s) %s", chatID, dur.Round(time.Millisecond), oneLine(text, 500))
}

// Tool — pemanggilan tool dengan hasil ringkas.
func (l *Logger) Tool(chatID int64, name, args, result string) {
	l.logf("TOOL   [%d] %s(%s) → %s", chatID, name, oneLine(args, 200), oneLine(result, 300))
}

// Perm — keputusan izin command.
func (l *Logger) Perm(chatID int64, command, decision string) {
	l.logf("PERM   [%d] %s → %s", chatID, command, decision)
}

// Err — error yang terjadi saat memproses.
func (l *Logger) Err(chatID int64, where string, err error) {
	l.logf("ERR    [%d] %s: %v", chatID, where, err)
}

// oneLine — rapikan teks multi-baris jadi satu baris + batasi panjang.
func oneLine(s string, max int) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '\n' || c == '\r' || c == '\t' {
			out = append(out, ' ')
		} else {
			out = append(out, c)
		}
	}
	if len(out) > max {
		out = append(out[:max], []byte("…")...)
	}
	return string(out)
}
