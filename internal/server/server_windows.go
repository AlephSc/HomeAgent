//go:build windows

// Stub Windows — agar build di laptop tidak error; metrik nyata hanya di Linux.
// Struct dibuat identik dengan server_linux.go supaya fastpath bisa dikompilasi.
package server

import (
	"fmt"
	"os"
)

type Server struct {
	Name      string
	WatchList []string
}

func New(name string, watchList []string) *Server {
	return &Server{Name: name, WatchList: watchList}
}

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

func (s *Server) Collect() (*Metrics, error) {
	h, err := os.Hostname()
	if err != nil {
		h = "windows-dev"
	}
	return &Metrics{Hostname: h}, nil
}

func (m *Metrics) Summary() string {
	return "(stub windows — deploy ke aleph untuk metrik nyata)\n"
}

var _ = fmt.Sprintf
