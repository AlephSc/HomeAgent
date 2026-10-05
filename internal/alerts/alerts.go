// Package alerts — alert engine non-AI (0 token): cek kondisi server periodik,
// kirim alert ke Telegram dengan debounce, dan pesan "recovered".
package alerts

import (
	"log"
	"sync"
	"time"

	"aleph-agent/internal/config"
	"aleph-agent/internal/server"
)

// Notifier — dikirim oleh gateway (func yang bisa kirim pesan Telegram).
type Notifier func(text string)

// Engine monitor kondisi server.
type Engine struct {
	cfg  *config.Config
	srv  *server.Server
	send Notifier
	stop chan struct{}
	once sync.Once

	// state alert aktif (untuk debounce & recovered)
	mu       sync.Mutex
	active   map[string]string // key -> pesan alert terakhir yang terkirim
	lastSent map[string]time.Time
}

// New membuat engine.
func New(cfg *config.Config, srv *server.Server, send Notifier) *Engine {
	return &Engine{
		cfg:      cfg,
		srv:      srv,
		send:     send,
		stop:     make(chan struct{}),
		active:   map[string]string{},
		lastSent: map[string]time.Time{},
	}
}

// Run loop monitoring — blocking; panggil dalam goroutine.
func (e *Engine) Run() {
	interval := 60 * time.Second
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-e.stop:
			return
		case <-t.C:
			e.check()
		}
	}
}

// Stop menghentikan engine.
func (e *Engine) Stop() {
	e.once.Do(func() { close(e.stop) })
}

// check menjalankan satu siklus pemeriksaan.
func (e *Engine) check() {
	m, err := e.srv.Collect()
	if err != nil {
		log.Printf("[alert] gagal ambil metrik: %v", err)
		return
	}

	// === RAM ===
	if e.cfg.Alert.RAMMinMB > 0 {
		key := "ram"
		if m.RAMAvailMB < e.cfg.Alert.RAMMinMB {
			e.fire(key, "🔴 RAM menipis di "+e.srv.Name+": available "+itoa(m.RAMAvailMB)+" MB (threshold "+itoa(e.cfg.Alert.RAMMinMB)+" MB)")
		} else {
			e.clear(key, "🟢 RAM pulih: available "+itoa(m.RAMAvailMB)+" MB")
		}
	}

	// === Disk ===
	for _, d := range m.Disks {
		key := "disk" + d.Path
		if d.UsedPct >= e.cfg.Alert.DiskMax {
			e.fire(key, "🔴 Disk "+d.Path+" hampir penuh: "+itoa(d.UsedPct)+"% ("+ftoa(d.UsedGB)+"/"+ftoa(d.TotalGB)+" GB)")
		} else {
			e.clear(key, "")
		}
	}

	// === Services ===
	for _, s := range m.Services {
		key := "svc" + s.Name
		if !s.Active {
			e.fire(key, "🔴 Service "+s.Name+" DOWN (status: "+s.Status+")")
		} else {
			e.clear(key, "🟢 Service "+s.Name+" kembali aktif")
		}
	}
}

// fire mengirim alert (dengan debounce) dan menandai aktif.
func (e *Engine) fire(key, msg string) {
	e.mu.Lock()
	_, wasActive := e.active[key]
	last, hasLast := e.lastSent[key]
	e.mu.Unlock()

	if wasActive {
		return // sudah di-alert, tunggu pulih
	}
	debounce := time.Duration(e.cfg.Alert.DebounceMin) * time.Minute
	if hasLast && time.Since(last) < debounce {
		return // masih dalam periode debounce
	}

	e.mu.Lock()
	e.active[key] = msg
	e.lastSent[key] = time.Now()
	e.mu.Unlock()

	log.Printf("[alert] FIRE %s: %s", key, msg)
	e.send(msg)
}

// clear menandai pulih; bila sebelumnya alert aktif, kirim pesan recovered (jika tidak kosong).
func (e *Engine) clear(key, recoveredMsg string) {
	e.mu.Lock()
	_, wasActive := e.active[key]
	if wasActive {
		delete(e.active, key)
	}
	e.mu.Unlock()

	if wasActive && recoveredMsg != "" {
		log.Printf("[alert] RECOVERED %s", key)
		e.send(recoveredMsg)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

func ftoa(f float64) string {
	return itoa(int(f*10+0.5)/10) + "." + itoa(int(f*10+0.5)%10)
}
