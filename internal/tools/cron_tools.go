package tools

// cron_tools.go — G11 H-2: agent bisa mengelola job terjadwal sendiri.
// Scheduler runtime API (Add/Remove) sudah ada di internal/cron; tool ini
// mengeksposnya ke agent. Persist via cron.json (LoadJobs saat start).
// Format jadwal scheduler: "every Nm|Nh|Nd" atau "daily HH:MM".

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"aleph-agent/internal/cron"
)

// cronSched — scheduler aktif (diset main.go setelah cron.New).
var (
	cronSched  *cron.Scheduler
	cronFlPath string // path cron.json untuk persist
)

// SetCronScheduler — wiring dari main.go.
func SetCronScheduler(s *cron.Scheduler, jobsFile string) {
	cronSched = s
	cronFlPath = jobsFile
}

// saveCron persist job list.
func saveCron() error {
	if cronSched == nil || cronFlPath == "" {
		return fmt.Errorf("scheduler belum siap")
	}
	return cronSched.SaveJobs(cronFlPath)
}

var reCronName = regexp.MustCompile(`^[a-z0-9_-]{3,32}$`)
var reEvery = regexp.MustCompile(`^every [1-9]\d*[mhd]$`)
var reDaily = regexp.MustCompile(`^daily ([01]\d|2[0-3]):[0-5]\d$`)

// validSchedule — terima "every Nm/Nh/Nd" atau "daily HH:MM".
func validSchedule(s string) bool {
	s = strings.ToLower(strings.TrimSpace(s))
	return reEvery.MatchString(s) || reDaily.MatchString(s)
}

// RegisterCronTools — dipanggil dari main.go.
func RegisterCronTools(reg *Registry) {
	reg.Register(Tool{
		Name: "cron_add",
		Description: "Jadwalkan tugas berulang. Jadwal: 'every 30m' / 'every 2h' / 'every 1d' / 'daily 07:30'. " +
			"Prompt adalah instruksi lengkap yang dijalankan agent saat jadwal tiba (self-contained, tidak melihat percakapan ini). " +
			"Hasil eksekusi dikirim ke admin via Telegram. Persist — tetap jalan setelah restart.",
		Params: map[string]interface{}{
			"name":   map[string]interface{}{"type": "string", "description": "nama job unik, a-z0-9_- (3-32)"},
			"spec":   map[string]interface{}{"type": "string", "description": "'every 30m' | 'every 2h' | 'daily 07:30'"},
			"prompt": map[string]interface{}{"type": "string", "description": "instruksi lengkap utk agent saat eksekusi"},
		},
		Required: []string{"name", "spec", "prompt"},
		Fn: func(ctx context.Context, args string) Result {
			if cronSched == nil {
				return Fail("scheduler belum aktif")
			}
			var a struct {
				Name   string `json:"name"`
				Spec   string `json:"spec"`
				Prompt string `json:"prompt"`
			}
			if err := json.Unmarshal([]byte(args), &a); err != nil {
				return Fail("args tidak valid")
			}
			a.Name = strings.ToLower(strings.TrimSpace(a.Name))
			if !reCronName.MatchString(a.Name) {
				return Fail("nama job tidak valid (3-32, a-z0-9_-)")
			}
			a.Spec = strings.ToLower(strings.TrimSpace(a.Spec))
			if !validSchedule(a.Spec) {
				return Fail("spec tidak dikenal — pakai 'every 30m', 'every 2h', 'every 1d', atau 'daily 07:30'")
			}
			a.Prompt = strings.TrimSpace(a.Prompt)
			if len(a.Prompt) < 8 {
				return Fail("prompt terlalu pendek")
			}
			if len(a.Prompt) > 4096 {
				return Fail("prompt terlalu panjang (maks 4096 char)")
			}
			if n := strings.Count(cronSched.List(), "\n"); n >= 20 {
				return Fail("maks 20 job — hapus dulu yang tidak terpakai (cron_del)")
			}
			cronSched.Add(&cron.Job{
				Name:     a.Name,
				Schedule: a.Spec,
				Prompt:   a.Prompt,
				Enabled:  true,
			})
			if err := saveCron(); err != nil {
				return Fail("tersimpan runtime tapi persist gagal: %v", err)
			}
			if pe := PermEngine(); pe != nil {
				pe.Audit("cron-add", a.Name+" "+a.Spec)
			}
			return Ok(fmt.Sprintf("job '%s' terjadwal (%s). Persist — tetap jalan setelah restart.", a.Name, a.Spec))
		},
	})

	reg.Register(Tool{
		Name:        "cron_list",
		Description: "Daftar job terjadwal yang aktif.",
		Fn: func(ctx context.Context, args string) Result {
			if cronSched == nil {
				return Fail("scheduler belum aktif")
			}
			txt := cronSched.List()
			if strings.TrimSpace(txt) == "" {
				return Ok("belum ada job terjadwal.")
			}
			return Ok(txt)
		},
	})

	reg.Register(Tool{
		Name: "cron_del",
		Description: "Hapus job terjadwal.",
		Params: map[string]interface{}{
			"name": map[string]interface{}{"type": "string", "description": "nama job"},
		},
		Required: []string{"name"},
		Fn: func(ctx context.Context, args string) Result {
			if cronSched == nil {
				return Fail("scheduler belum aktif")
			}
			var a struct {
				Name string `json:"name"`
			}
			if err := json.Unmarshal([]byte(args), &a); err != nil {
				return Fail("args tidak valid")
			}
			a.Name = strings.ToLower(strings.TrimSpace(a.Name))
			if !cronSched.Remove(a.Name) {
				return Fail("job '%s' tidak ditemukan", a.Name)
			}
			if err := saveCron(); err != nil {
				return Fail("terhapus runtime tapi persist gagal: %v", err)
			}
			if pe := PermEngine(); pe != nil {
				pe.Audit("cron-del", a.Name)
			}
			return Ok(fmt.Sprintf("job '%s' dihapus.", a.Name))
		},
	})
}
