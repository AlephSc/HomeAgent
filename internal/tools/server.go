// Package tools — tool server & file (sandboxed).
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"aleph-agent/internal/perm"
	"aleph-agent/internal/server"
)

// permEng — engine permission tiered (Fase A). Nil via SetPermEngine di main.
var permEng *perm.Engine

// SetPermEngine memasang engine permission (dipanggil main sekali).
func SetPermEngine(e *perm.Engine) { permEng = e }

// PermEngine mengembalikan engine aktif (dipakai fastpath /audit).
func PermEngine() *perm.Engine { return permEng }

// msgDenied — pesan ramah agar model BERHENTI retry (Fase 0).
const msgDenied = "⛔ AKSES DITOLAK oleh kebijakan keamanan. JANGAN coba cara lain — " +
	"tugas ini di luar wewenangmu. Laporkan ke user dalam Bahasa Indonesia: " +
	"apa yang diminta, kenapa tidak bisa, dan alternatif yang disarankan."

// confirmFnKey — key context untuk fungsi konfirmasi Telegram (tier 🔴).
type confirmFnKey struct{}

// ConfirmFunc: tanya user via chat; true = lanjut.
type ConfirmFunc func(action, detail string) bool

// WithConfirm menyuntikkan confirm func ke context (dipanggil agent loop).
func WithConfirm(ctx context.Context, fn ConfirmFunc) context.Context {
	return context.WithValue(ctx, confirmFnKey{}, fn)
}

func confirmFromCtx(ctx context.Context) ConfirmFunc {
	fn, _ := ctx.Value(confirmFnKey{}).(ConfirmFunc)
	return fn
}

// RegisterServerTools menambahkan tools akses server.
// srv: server metrics collector. sandboxDirs: direktori yang boleh diakses file tools.
// execWhitelist: command yang boleh dieksekusi.
func RegisterServerTools(r *Registry, srv *server.Server, sandboxDirs []string, execWhitelist []string) {
	// === server_status ===
	r.Register(Tool{
		Name:        "server_status",
		Description: "Kondisi home server: uptime, CPU load, RAM, disk, daftar service systemd yang dimonitor.",
		Params:      map[string]interface{}{},
		Fn: func(ctx context.Context, args string) Result {
			m, err := srv.Collect()
			if err != nil {
				return Fail("gagal ambil metrik: %v", err)
			}
			return Ok(m.Summary())
		},
	})

	// === service_control ===
	r.Register(Tool{
		Name:        "service_control",
		Description: "Kontrol service systemd: status/start/stop/restart. Untuk container Docker gunakan unit 'docker:<nama>' (mis. docker:adguardhome). Hati-hati untuk service kritis.",
		Params: map[string]interface{}{
			"action": map[string]interface{}{"type": "string", "enum": []string{"status", "start", "stop", "restart"}},
			"unit":   map[string]interface{}{"type": "string", "description": "nama unit systemd, mis. dnsmasq"},
		},
		Required: []string{"action", "unit"},
		Fn: func(ctx context.Context, args string) Result {
			var a struct {
				Action string `json:"action"`
				Unit   string `json:"unit"`
			}
			if err := json.Unmarshal([]byte(args), &a); err != nil {
				return Fail("args tidak valid: %v", err)
			}
			if strings.ContainsAny(a.Unit, ";|&$`") || len(a.Unit) > 64 {
				return Fail("nama unit mencurigakan")
			}
			// Fase A: klasifikasi tier sebelum aksi
			if permEng != nil {
				tier := permEng.ClassifyService(a.Action, a.Unit)
				permEng.Audit("service", a.Action+" "+a.Unit+" ["+tier.String()+"]")
				switch tier {
				case perm.Confirm:
					fn := confirmFromCtx(ctx)
					if fn == nil {
						return Ok("⛔ Aksi '" + a.Action + " " + a.Unit + "' adalah service KRITIS dan butuh konfirmasi user, tetapi kanal konfirmasi tidak tersedia. Laporkan ke user, jangan coba lagi.")
					}
					if !fn(a.Action, a.Unit) {
						return Ok("⏸ User menolak/membatalkan " + a.Action + " " + a.Unit + ". Hentikan, jangan ulangi.")
					}
				case perm.Denied:
					return Ok(msgDenied)
				}
			}
			// unit "docker:<nama>" → operasikan container Docker
			if strings.HasPrefix(a.Unit, "docker:") {
				name := strings.TrimPrefix(a.Unit, "docker:")
				if strings.ContainsAny(name, ";|&$`") {
					return Fail("nama container mencurigakan")
				}
				return dockerControl(ctx, a.Action, name)
			}
			switch a.Action {
			case "status":
				out, err := exec.CommandContext(ctx, "systemctl", "is-active", a.Unit).Output()
				st := strings.TrimSpace(string(out))
				if err != nil && st == "" {
					return Fail("systemctl gagal: %v", err)
				}
				return Ok(fmt.Sprintf("%s: %s", a.Unit, st))
			case "start", "stop", "restart":
				out, err := exec.CommandContext(ctx, "systemctl", a.Action, a.Unit).CombinedOutput()
				if err != nil {
					return Fail("%s %s gagal: %v: %.300s", a.Action, a.Unit, err, string(out))
				}
				// verifikasi setelah aksi (verify-after-action)
				time.Sleep(1500 * time.Millisecond)
				st, _ := exec.CommandContext(ctx, "systemctl", "is-active", a.Unit).Output()
				return Ok(fmt.Sprintf("%s %s OK — status sekarang: %s", a.Action, a.Unit, strings.TrimSpace(string(st))))
			default:
				return Fail("action tidak dikenal: %s", a.Action)
			}
		},
	})

	// === list_files ===
	r.Register(Tool{
		Name:        "list_files",
		Description: "Daftar isi direktori dalam sandbox home server.",
		Params: map[string]interface{}{
			"path": map[string]interface{}{"type": "string", "description": "path direktori relatif/absolut dalam sandbox"},
		},
		Required: []string{"path"},
		Fn: func(ctx context.Context, args string) Result {
			var a struct {
				Path string `json:"path"`
			}
			if err := json.Unmarshal([]byte(args), &a); err != nil {
				return Fail("args tidak valid")
			}
			dir, ok := sandboxPath(sandboxDirs, a.Path)
			if !ok {
				return Fail("path di luar sandbox: %s", a.Path)
			}
			entries, err := os.ReadDir(dir)
			if err != nil {
				return Fail("read dir: %v", err)
			}
			var b strings.Builder
			for _, e := range entries {
				info, err := e.Info()
				if err != nil {
					continue
				}
				kind := "file"
				if e.IsDir() {
					kind = "dir "
				}
				fmt.Fprintf(&b, "%s %8d %s\n", kind, info.Size(), e.Name())
			}
			return Ok(b.String())
		},
	})

	// === read_file ===
	r.Register(Tool{
		Name:        "read_file",
		Description: "Baca isi file teks (maks 16 KB) dari sandbox.",
		Params: map[string]interface{}{
			"path": map[string]interface{}{"type": "string"},
		},
		Required: []string{"path"},
		Fn: func(ctx context.Context, args string) Result {
			var a struct {
				Path string `json:"path"`
			}
			if err := json.Unmarshal([]byte(args), &a); err != nil {
				return Fail("args tidak valid")
			}
			p, ok := sandboxPath(sandboxDirs, a.Path)
			if !ok {
				return Fail("path di luar sandbox: %s", a.Path)
			}
			data, err := os.ReadFile(p)
			if err != nil {
				return Fail("read: %v", err)
			}
			if len(data) > 16<<10 {
				data = data[:16<<10]
			}
			return Ok(string(data))
		},
	})

	// === write_file ===
	r.Register(Tool{
		Name:        "write_file",
		Description: "Tulis/buat file teks di sandbox (overwrite penuh).",
		Params: map[string]interface{}{
			"path":    map[string]interface{}{"type": "string"},
			"content": map[string]interface{}{"type": "string"},
		},
		Required: []string{"path", "content"},
		Fn: func(ctx context.Context, args string) Result {
			var a struct {
				Path    string `json:"path"`
				Content string `json:"content"`
			}
			if err := json.Unmarshal([]byte(args), &a); err != nil {
				return Fail("args tidak valid")
			}
			// Fase A: klasifikasi tier tulis (sandbox=free, gated=backup, confirm=tanya, lainnya=tolak)
			if permEng != nil {
				tier := permEng.ClassifyWrite(a.Path, sandboxDirs)
				permEng.Audit("write", a.Path+" ("+fmt.Sprint(len(a.Content))+"B) ["+tier.String()+"]")
				switch tier {
				case perm.Forbidden, perm.Denied:
					return Ok(msgDenied + " Path: " + a.Path)
				case perm.Confirm:
					fn := confirmFromCtx(ctx)
					if fn == nil || !fn("tulis file", a.Path) {
						return Ok("⏸ User menolak/membatalkan penulisan " + a.Path + ". Hentikan, jangan ulangi.")
					}
				}
			}
			p, ok := sandboxPath(sandboxDirs, a.Path)
			if !ok {
				// Gated/confirm path di luar sandbox — sudah lolos klasifikasi: resolve langsung
				if permEng == nil {
					return Fail("path di luar sandbox: %s", a.Path)
				}
				p = resolveWrite(sandboxDirs, a.Path)
			}
			if permEng != nil && permEng.ClassifyWrite(a.Path, sandboxDirs) == perm.Gated {
				if bak, err := permEng.Backup(p); err != nil {
					return Fail("backup gagal: %v", err)
				} else if bak != "" {
					permEng.Audit("backup", bak)
				}
			}
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				return Fail("mkdir: %v", err)
			}
			if err := os.WriteFile(p, []byte(a.Content), 0o644); err != nil {
				return Fail("write: %v", err)
			}
			res := fmt.Sprintf("tersimpan: %s (%d bytes)", p, len(a.Content))
			if permEng != nil && permEng.ClassifyWrite(a.Path, sandboxDirs) == perm.Gated {
				res += " [backup otomatis dibuat]"
			}
			return Ok(res)
		},
	})

	// === exec_command ===
	r.Register(Tool{
		Name:        "exec_command",
		Description: "Jalankan command whitelist di server (mis. 'df -h', 'uptime', 'docker ps'). Command di luar whitelist ditolak.",
		Params: map[string]interface{}{
			"command": map[string]interface{}{"type": "string", "description": "command tunggal, mis. 'df -h'"},
		},
		Required: []string{"command"},
		Fn: func(ctx context.Context, args string) Result {
			var a struct {
				Command string `json:"command"`
			}
			if err := json.Unmarshal([]byte(args), &a); err != nil {
				return Fail("args tidak valid")
			}
			fields := strings.Fields(a.Command)
			if len(fields) == 0 {
				return Fail("command kosong")
			}
			base := filepath.Base(fields[0])
			// F-perm: head-based classification + prompt 4-pilihan (once/always/session/deny)
			if permEng != nil {
				tier := permEng.ClassifyExec(a.Command, ctx, execWhitelist, permEng.GatedExec())
				permEng.Audit("exec", a.Command+" ["+tier.String()+"]")
				switch tier {
				case perm.Forbidden:
					return Ok("⛔ Command DESTRUKTIF — dilarang keras (blocklist). Jangan ulangi, tawarkan alternatif aman.")
				case perm.Denied:
					return Ok("⛔ User menolak command `" + base + "`. Hentikan, jangan ulangi, tawarkan alternatif.")
				case perm.Confirm:
					// tidak dipakai di model baru (prompt via AskFunc)
				}
				defer permEng.ConsumeOnce(a.Command)
			}
			cmdCtx, cancel := context.WithTimeout(ctx, 300*time.Second)
			defer cancel()
			out, err := exec.CommandContext(cmdCtx, "sh", "-c", a.Command).CombinedOutput()
			if len(out) > 8<<10 {
				out = out[:8<<10]
			}
			if err != nil {
				return Ok(fmt.Sprintf("exit error: %v\n%.4000s", err, string(out)))
			}
			return Ok(string(out))
		},
	})
}

// resolveWrite menyelesaikan path tulis di luar sandbox (gated/confirm, sudah lolos klasifikasi):
// relatif → gabung sandbox pertama; absolut → clean.
func resolveWrite(sandboxDirs []string, p string) string {
	if filepath.IsAbs(p) {
		return filepath.Clean(p)
	}
	if len(sandboxDirs) > 0 {
		return filepath.Join(sandboxDirs[0], p)
	}
	return filepath.Clean(p)
}

// dockerControl menangani action status/start/stop/restart untuk container Docker.
func dockerControl(ctx context.Context, action, name string) Result {
	switch action {
	case "status":
		out, err := exec.CommandContext(ctx, "docker", "inspect", "-f", "{{.State.Status}}", name).Output()
		st := strings.TrimSpace(string(out))
		if err != nil {
			return Fail("container %s tidak ditemukan / docker error", name)
		}
		return Ok(fmt.Sprintf("docker:%s: %s", name, st))
	case "start", "stop", "restart":
		out, err := exec.CommandContext(ctx, "docker", action, name).CombinedOutput()
		if err != nil {
			return Fail("docker %s %s gagal: %v: %.300s", action, name, err, string(out))
		}
		time.Sleep(1500 * time.Millisecond)
		st, _ := exec.CommandContext(ctx, "docker", "inspect", "-f", "{{.State.Status}}", name).Output()
		return Ok(fmt.Sprintf("docker %s %s OK — status sekarang: %s", action, name, strings.TrimSpace(string(st))))
	default:
		return Fail("action tidak dikenal: %s", action)
	}
}

// sandboxPath memvalidasi path terhadap sandbox dirs (mencegah path traversal).
func sandboxPath(sandboxDirs []string, p string) (string, bool) {
	if p == "" {
		return "", false
	}
	// expand ~ ke sandbox pertama
	if strings.HasPrefix(p, "~") {
		if len(sandboxDirs) == 0 {
			return "", false
		}
		p = filepath.Join(sandboxDirs[0], strings.TrimPrefix(p, "~"))
	}
	if !filepath.IsAbs(p) && len(sandboxDirs) > 0 {
		p = filepath.Join(sandboxDirs[0], p)
	}
	clean := filepath.Clean(p)
	for _, s := range sandboxDirs {
		sAbs, err := filepath.Abs(s)
		if err != nil {
			continue
		}
		if runtime.GOOS == "windows" {
			sAbs = strings.ToLower(sAbs)
			cleanCmp := strings.ToLower(clean)
			if cleanCmp == sAbs || strings.HasPrefix(cleanCmp, sAbs+string(filepath.Separator)) {
				return clean, true
			}
		} else if clean == sAbs || strings.HasPrefix(clean, sAbs+"/") {
			return clean, true
		}
	}
	return "", false
}
