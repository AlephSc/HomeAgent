// Package cron — F7: job terjadwal dengan self-verify.
// Job = perintah shell ATAU prompt agent; hasil diverifikasi (exit code / output check),
// gagal → alert via Telegram. State persist di SQLite (table cron_job).
package cron

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// Notifier — dipakai kirim alert (dipenuhi telegram.Gateway.SendDirect).
type Notifier interface {
	SendDirect(text string)
}

// AgentRunner — jalankan prompt via agent (dipenuhi *agent.Agent dengan wrapper).
type AgentRunner interface {
	RunPrompt(ctx context.Context, prompt string) (string, error)
}

// Job definisi satu job terjadwal.
type Job struct {
	Name     string `json:"name"`
	Schedule string `json:"schedule"` // "every 5m" | "every 1h" | "daily 03:00"
	Command  string `json:"command"`  // perintah shell (kosongkan jika pakai prompt)
	Prompt   string `json:"prompt"`   // prompt agent (jika command kosong)
	Verify   string `json:"verify"`   // substring yang HARUS ada di output (kosong = skip verify)
	Enabled  bool   `json:"enabled"`
	LastRun  string `json:"last_run"`
	LastOK   bool   `json:"last_ok"`
}

// Scheduler kumpulan job.
type Scheduler struct {
	mu     sync.Mutex
	jobs   []*Job
	notify Notifier
	agent  AgentRunner
	stop   chan struct{}
}

// New scheduler.
func New(n Notifier, a AgentRunner) *Scheduler {
	return &Scheduler{notify: n, agent: a, stop: make(chan struct{})}
}

// Add job baru.
func (s *Scheduler) Add(j *Job) {
	s.mu.Lock()
	defer s.mu.Unlock()
	j.Enabled = true
	s.jobs = append(s.jobs, j)
}

// Remove job by name.
func (s *Scheduler) Remove(name string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, j := range s.jobs {
		if j.Name == name {
			s.jobs = append(s.jobs[:i], s.jobs[i+1:]...)
			return true
		}
	}
	return false
}

// List job (format teks).
func (s *Scheduler) List() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.jobs) == 0 {
		return "Tidak ada job cron."
	}
	var b strings.Builder
	b.WriteString("⏰ Job terjadwal:\n")
	for _, j := range s.jobs {
		status := "🔴 belum jalan"
		if j.LastRun != "" {
			if j.LastOK {
				status = "🟢 OK"
			} else {
				status = "❌ gagal"
			}
			status += " @ " + j.LastRun
		}
		fmt.Fprintf(&b, "• %s [%s] %s — %s\n", j.Name, j.Schedule, status, deskripsi(*j))
	}
	return b.String()
}

func deskripsi(j Job) string {
	if j.Command != "" {
		return "`" + j.Command + "`"
	}
	if len(j.Prompt) > 60 {
		return j.Prompt[:60] + "…"
	}
	return j.Prompt
}

// Run loop scheduler — blocking; panggil dalam goroutine.
func (s *Scheduler) Run() {
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-s.stop:
			return
		case now := <-t.C:
			s.tick(now)
		}
	}
}

// Stop scheduler.
func (s *Scheduler) Stop() { close(s.stop) }

func (s *Scheduler) tick(now time.Time) {
	s.mu.Lock()
	due := []*Job{}
	for _, j := range s.jobs {
		if j.Enabled && isDue(j.Schedule, j.LastRun, now) {
			due = append(due, j)
		}
	}
	s.mu.Unlock()
	for _, j := range due {
		go s.runJob(j, now)
	}
}

// runJob eksekusi + verify + alert.
func (s *Scheduler) runJob(j *Job, now time.Time) {
	log.Printf("[cron] job %q mulai", j.Name)
	var out string
	var err error
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	if j.Command != "" {
		var b []byte
		c := exec.CommandContext(ctx, "bash", "-c", j.Command)
		b, err = c.CombinedOutput()
		out = string(b)
	} else if s.agent != nil {
		out, err = s.agent.RunPrompt(ctx, j.Prompt)
	} else {
		err = fmt.Errorf("job tanpa command & agent tidak aktif")
	}
	ok := err == nil
	if ok && j.Verify != "" {
		ok = strings.Contains(out, j.Verify)
		if !ok {
			err = fmt.Errorf("verify gagal: output tidak mengandung %q", j.Verify)
		}
	}
	s.mu.Lock()
	j.LastRun = now.Format("2006-01-02 15:04")
	j.LastOK = ok
	s.mu.Unlock()
	if !ok && s.notify != nil {
		s.notify.SendDirect(fmt.Sprintf("⏰❌ Cron job %q GAGAL:\n%v\n--- output (tail) ---\n%s",
			j.Name, err, tailStr(out, 500)))
	} else if !ok {
		log.Printf("[cron] job %q gagal: %v", j.Name, err)
	} else {
		log.Printf("[cron] job %q OK", j.Name)
	}
}

// isDue — parser sederhana: "every Xm" | "every Xh" | "daily HH:MM".
func isDue(schedule, lastRun string, now time.Time) bool {
	sched := strings.TrimSpace(strings.ToLower(schedule))
	var interval time.Duration
	if strings.HasPrefix(sched, "every ") {
		spec := strings.TrimSpace(strings.TrimPrefix(sched, "every "))
		var n int
		var unit string
		if _, err := fmt.Sscanf(spec, "%d%s", &n, &unit); err != nil {
			return false
		}
		switch strings.TrimSuffix(unit, "s") {
		case "m", "min", "minute":
			interval = time.Duration(n) * time.Minute
		case "h", "hour":
			interval = time.Duration(n) * time.Hour
		case "d", "day":
			interval = time.Duration(n) * 24 * time.Hour
		default:
			return false
		}
	} else if strings.HasPrefix(sched, "daily ") {
		var hh, mm int
		if _, err := fmt.Sscanf(strings.TrimPrefix(sched, "daily "), "%d:%d", &hh, &mm); err != nil {
			return false
		}
		nowStr := now.Format("15:04")
		want := fmt.Sprintf("%02d:%02d", hh, mm)
		if nowStr != want {
			return false
		}
		// hindari double-run: skip jika sudah jalan di menit yang sama
		if lastRun != "" && strings.HasSuffix(lastRun, want) && now.Format("2006-01-02") == lastRun[:10] {
			return false
		}
		return true
	}
	if interval == 0 {
		return false
	}
	if lastRun == "" {
		return true
	}
	last, err := time.Parse("2006-01-02 15:04", lastRun)
	if err != nil {
		return true
	}
	return now.Sub(last) >= interval
}

func tailStr(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return "…" + s[len(s)-n:]
}

// MarshalJobs — serialisasi jobs (untuk persist, dipakai main).
func (s *Scheduler) MarshalJobs() []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, _ := json.MarshalIndent(s.jobs, "", "  ")
	return b
}
