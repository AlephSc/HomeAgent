// Package cron — persistence jobs di file JSON (ringan, tanpa table baru).
package cron

import (
	"encoding/json"
	"os"
)

// SaveJobs tulis daftar job ke file JSON.
func (s *Scheduler) SaveJobs(path string) error {
	return os.WriteFile(path, s.MarshalJobs(), 0o644)
}

// LoadJobs baca daftar job dari file JSON.
func (s *Scheduler) LoadJobs(path string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var jobs []*Job
	if err := json.Unmarshal(b, &jobs); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.jobs = append(s.jobs, jobs...)
	return nil
}
