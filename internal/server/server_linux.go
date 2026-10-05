//go:build linux

// Package server — metrik & kondisi home server (baca /proc, statfs, systemctl)
package server

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
)

// Server membungkus akses read-only ke kondisi mesin.
type Server struct {
	Name string
	// watchList: unit systemd yang statusnya dilaporkan
	WatchList []string
}

// New membuat instance Server.
func New(name string, watchList []string) *Server {
	return &Server{Name: name, WatchList: watchList}
}

// Metrics — snapshot kondisi server.
type Metrics struct {
	Hostname   string
	UptimeSec  int64
	RAMTotalMB int
	RAMUsedMB  int
	RAMAvailMB int
	LoadAvg1   float64
	Disks      []Disk
	Services   []Service
}

type Disk struct {
	Path    string
	TotalGB float64
	UsedGB  float64
	UsedPct int
}

type Service struct {
	Name   string
	Active bool
	Status string
}

// Collect mengumpulkan semua metrik (Linux).
func (s *Server) Collect() (*Metrics, error) {
	m := &Metrics{}
	var err error

	if m.Hostname, err = os.Hostname(); err != nil {
		m.Hostname = "?"
	}
	m.UptimeSec = readUptime()
	m.LoadAvg1 = readLoadAvg()
	readMeminfo(m)
	m.Disks = readDisks([]string{"/", "/opt"})
	m.Services = s.readServices()

	return m, nil
}

func readUptime() int64 {
	b, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return 0
	}
	var up float64
	fmt.Sscanf(string(b), "%f", &up)
	return int64(up)
}

func readLoadAvg() float64 {
	b, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return 0
	}
	var l float64
	fmt.Sscanf(string(b), "%f", &l)
	return l
}

func readMeminfo(m *Metrics) {
	b, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(b), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		var kb int
		fmt.Sscanf(fields[1], "%d", &kb)
		switch {
		case strings.HasPrefix(line, "MemTotal:"):
			m.RAMTotalMB = kb / 1024
		case strings.HasPrefix(line, "MemAvailable:"):
			m.RAMAvailMB = kb / 1024
		}
	}
	m.RAMUsedMB = m.RAMTotalMB - m.RAMAvailMB
}

func readDisks(paths []string) []Disk {
	var out []Disk
	for _, p := range paths {
		var st syscall.Statfs_t
		if err := syscall.Statfs(p, &st); err != nil {
			continue
		}
		total := float64(st.Blocks) * float64(st.Bsize) / 1e9
		avail := float64(st.Bavail) * float64(st.Bsize) / 1e9
		used := total - avail
		pct := 0
		if total > 0 {
			pct = int(used / total * 100)
		}
		out = append(out, Disk{Path: p, TotalGB: total, UsedGB: used, UsedPct: pct})
	}
	return out
}

func (s *Server) readServices() []Service {
	watch := s.WatchList
	if len(watch) == 0 {
		watch = []string{"dnsmasq", "docker", "tailscaled"}
	}
	var out []Service
	for _, svc := range watch {
		// "docker:<nama>" → cek container Docker (bukan unit systemd)
		if strings.HasPrefix(svc, "docker:") {
			name := strings.TrimPrefix(svc, "docker:")
			st := "unknown"
			out2, err := exec.Command("docker", "inspect", "-f", "{{.State.Status}}", name).Output()
			if err == nil && len(out2) > 0 {
				st = strings.TrimSpace(string(out2))
			}
			out = append(out, Service{Name: svc, Active: st == "running", Status: st})
			continue
		}
		st := "unknown"
		out2, err := exec.Command("systemctl", "is-active", svc).Output()
		if err == nil {
			st = strings.TrimSpace(string(out2))
		}
		out = append(out, Service{Name: svc, Active: st == "active", Status: st})
	}
	return out
}

// Summary — ringkasan kondisi (dipakai /status & system prompt agent).
func (m *Metrics) Summary() string {
	var b strings.Builder
	fmt.Fprintf(&b, "Host: %s | uptime: %s\n", m.Hostname, fmtUptime(m.UptimeSec))
	fmt.Fprintf(&b, "RAM: %d/%d MB (available %d MB)\n", m.RAMUsedMB, m.RAMTotalMB, m.RAMAvailMB)
	fmt.Fprintf(&b, "Load: %.2f\n", m.LoadAvg1)
	for _, d := range m.Disks {
		fmt.Fprintf(&b, "Disk %s: %.0f/%.0f GB (%d%%)\n", d.Path, d.UsedGB, d.TotalGB, d.UsedPct)
	}
	for _, s := range m.Services {
		mark := "OK"
		if !s.Active {
			mark = "DOWN"
		}
		fmt.Fprintf(&b, "Service %s: %s [%s]\n", s.Name, s.Status, mark)
	}
	return b.String()
}

func fmtUptime(sec int64) string {
	d := sec / 86400
	h := (sec % 86400) / 3600
	m := (sec % 3600) / 60
	if d > 0 {
		return fmt.Sprintf("%dd %dh %dm", d, h, m)
	}
	return fmt.Sprintf("%dh %dm", h, m)
}
