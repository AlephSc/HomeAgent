package telegram

// G5 — Service Manager: daftar service custom milik user + aksi enable/disable
// (auto-startup) + pemantauan berkala dengan alert.
//
// Data: <data_dir>/services.json — daftar unit + flag pantau.
// Aksi tetap melewati mesin permission (ClassifyService) seperti tombol lama.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// CustomService satu entri daftar service user.
type CustomService struct {
	Unit   string `json:"unit"`   // nama unit systemd (mis. adguardhome) atau docker:<nama>
	Watch  bool   `json:"watch"`  // pantau berkala → alert kalau mati
}

// serviceManager state global (satu bot = satu manager).
type serviceManager struct {
	mu       sync.Mutex
	path     string // path services.json
	services []CustomService
	gateway  *Gateway
}

var svcMgr *serviceManager

var unitNameRe = regexp.MustCompile(`^[A-Za-z0-9_.@\\-]{1,64}$`)

// InitServiceManager muat/initialisasi daftar service custom.
func InitServiceManager(dataDir string, g *Gateway) {
	svcMgr = &serviceManager{path: filepath.Join(dataDir, "services.json"), gateway: g}
	data, err := os.ReadFile(svcMgr.path)
	if err == nil {
		_ = json.Unmarshal(data, &svcMgr.services)
	}
	go svcMgr.watchLoop()
}

// validUnit — nama unit systemd valid (blokir injeksi via tombol/teks).
func validUnit(u string) bool {
	u = strings.TrimSpace(u)
	if strings.HasPrefix(u, "docker:") {
		u = strings.TrimPrefix(u, "docker:")
	}
	return unitNameRe.MatchString(u)
}

// List kembalikan salinan daftar.
func (m *serviceManager) List() []CustomService {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]CustomService, len(m.services))
	copy(out, m.services)
	return out
}

// Add tambah service + simpan.
func (m *serviceManager) Add(unit string) error {
	if !validUnit(unit) {
		return fmt.Errorf("nama unit tidak valid (huruf/angka/._@- saja)")
	}
	if strings.HasPrefix(unit, "docker:") {
		unit = "docker:" + strings.TrimPrefix(unit, "docker:")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, s := range m.services {
		if s.Unit == unit {
			return fmt.Errorf("sudah ada di daftar")
		}
	}
	m.services = append(m.services, CustomService{Unit: unit, Watch: true})
	return m.save()
}

// Remove hapus service + simpan.
func (m *serviceManager) Remove(unit string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, s := range m.services {
		if s.Unit == unit {
			m.services = append(m.services[:i], m.services[i+1:]...)
			return m.save()
		}
	}
	return fmt.Errorf("tidak ada di daftar")
}

// SetWatch ubah flag pantau.
func (m *serviceManager) SetWatch(unit string, watch bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, s := range m.services {
		if s.Unit == unit {
			m.services[i].Watch = watch
			return m.save()
		}
	}
	return fmt.Errorf("tidak ada di daftar")
}

// save simpan ke disk (dipanggil dengan mutex terpegang).
func (m *serviceManager) save() error {
	data, err := json.MarshalIndent(m.services, "", "  ")
	if err != nil {
		return err
	}
	tmp := m.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, m.path) // atomik
}

// isActive cek status unit (systemd/docker).
func isActive(unit string) (string, error) {
	if strings.HasPrefix(unit, "docker:") {
		name := strings.TrimPrefix(unit, "docker:")
		out, err := execOutput("docker", "inspect", "-f", "{{.State.Status}}", name)
		return out, err
	}
	out, err := execOutput("systemctl", "is-active", unit)
	return out, err
}

// watchLoop — cek service ber-flag Watch tiap 60 dtk; alert hanya saat transisi aktif→mati.
func (m *serviceManager) watchLoop() {
	wasDown := map[string]bool{}
	lastAlert := map[string]time.Time{} // debounce anti-spam
	t := time.NewTicker(60 * time.Second)
	defer t.Stop()
	for range t.C {
		for _, s := range m.List() {
			if !s.Watch {
				continue
			}
			st, err := isActive(s.Unit)
			down := err != nil || st == "inactive" || st == "failed"
			if down && !wasDown[s.Unit] && time.Since(lastAlert[s.Unit]) > 10*time.Minute {
				lastAlert[s.Unit] = time.Now()
				if g := m.gateway; g != nil {
					admin := g.firstAdminID()
					if admin != 0 {
						g.sendDirectChat(admin, fmt.Sprintf("🚨 Service %s TERDETEKSI MATI (status: %s).", s.Unit, st))
					}
				}
			}
			wasDown[s.Unit] = down
		}
	}
}

// execOutput wrapper kecil (dengan timeout — watcher tidak boleh macet).
func execOutput(name string, args ...string) (string, error) {
	out, err := runOutput(30*time.Second, name, args...)
	return strings.TrimSpace(out), err
}
