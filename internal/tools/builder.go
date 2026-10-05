// Package tools — F10: builder capability.
// Bot bisa scaffold, jalankan, verifikasi, dan stop proyek di workspace terisolasi.
// Tulis file proyek tetap lewat write_file (resolveWrite: dalam workspace = tier 🟢).
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// WorkspaceRoot — root direktori proyek (di-set main.go).
var WorkspaceRoot string

// SetWorkspace — inisialisasi root workspace proyek.
func SetWorkspace(root string) {
	WorkspaceRoot = root
	os.MkdirAll(root, 0o755)
}

// resolveProject — pastikan nama proyek aman (tanpa path traversal).
func resolveProject(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || strings.Contains(name, "..") || strings.ContainsAny(name, "/\\ ") {
		return "", fmt.Errorf("nama proyek tidak valid: %q (huruf/angka/dash saja)", name)
	}
	return filepath.Join(WorkspaceRoot, name), nil
}

// RegisterBuilderTools — scaffold_project, run_project, verify_project, stop_project, list_projects.
func RegisterBuilderTools(reg *Registry) {
	reg.Register(Tool{
		Name:        "scaffold_project",
		Description: "Buat proyek baru di workspace. Template: static (HTML/CSS/JS), flask (Python), go, node. Setelah scaffold, isi file-nya dengan write_file lalu jalankan dengan run_project.",
		Params: map[string]interface{}{
			"name":        map[string]interface{}{"type": "string", "description": "nama proyek (huruf/angka/dash)"},
			"template":    map[string]interface{}{"type": "string", "description": "static|flask|go|node", "enum": []string{"static", "flask", "go", "node"}},
			"description": map[string]interface{}{"type": "string", "description": "deskripsi singkat proyek"},
		},
		Required: []string{"name", "template"},
		Fn: func(ctx context.Context, args string) Result {
			var a struct {
				Name        string `json:"name"`
				Template    string `json:"template"`
				Description string `json:"description"`
			}
			if err := json.Unmarshal([]byte(args), &a); err != nil {
				return Fail("args tidak valid")
			}
			dir, err := resolveProject(a.Name)
			if err != nil {
				return Fail("%v", err)
			}
			if _, err := os.Stat(dir); err == nil {
				return Fail("proyek %q sudah ada — pakai nama lain atau hapus dulu", a.Name)
			}
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return Fail("%v", err)
			}
			write := func(rel, content string) error {
				return os.WriteFile(filepath.Join(dir, rel), []byte(content), 0o644)
			}
			switch a.Template {
			case "static":
				write("index.html", staticHTML(a.Name, a.Description))
				write("style.css", "body{font-family:sans-serif;max-width:720px;margin:40px auto;padding:0 16px}\n")
				write("app.js", "console.log('"+a.Name+" siap');\n")
			case "flask":
				write("app.py", flaskApp(a.Name, a.Description))
				write("requirements.txt", "flask\n")
			case "go":
				write("main.go", goApp(a.Name, a.Description))
				write("go.mod", "module "+a.Name+"\n\ngo 1.21\n")
			case "node":
				write("server.js", nodeApp(a.Name, a.Description))
				write("package.json", fmt.Sprintf(`{"name":"%s","version":"1.0.0","scripts":{"start":"node server.js"},"dependencies":{}}`, a.Name))
			default:
				os.RemoveAll(dir)
				return Fail("template tidak dikenal: %q (pilihan: static, flask, go, node)", a.Template)
			}
			readme := "# " + a.Name + "\n\n" + a.Description + "\n\nDibuat oleh aleph-agent (F10 builder).\nTemplate: " + a.Template + "\n"
			write("README.md", readme)
			return Okf("✅ Proyek %q dibuat (%s) di %s. Isi/ubah file dengan write_file (path di dalam folder proyek), lalu jalankan dengan run_project.", a.Name, a.Template, dir)
		},
	})

	reg.Register(Tool{
		Name:        "run_project",
		Description: "Jalankan proyek di workspace (background). Return port yang dipakai. Proyek go/node/python di-deteksi otomatis dari file utamanya.",
		Params: map[string]interface{}{
			"name": map[string]interface{}{"type": "string"},
			"port": map[string]interface{}{"type": "integer", "description": "port (opsional, default auto 8100+)"},
		},
		Required: []string{"name"},
		Fn: func(ctx context.Context, args string) Result {
			var a struct {
				Name string `json:"name"`
				Port int    `json:"port"`
			}
			if err := json.Unmarshal([]byte(args), &a); err != nil {
				return Fail("args tidak valid")
			}
			dir, err := resolveProject(a.Name)
			if err != nil {
				return Fail("%v", err)
			}
			if _, err := os.Stat(dir); err != nil {
				return Fail("proyek %q tidak ada — scaffold_project dulu", a.Name)
			}
			port := a.Port
			if port == 0 {
				port = freePort()
			}
			if port == 0 {
				return Fail("tidak ada port kosong di 8100-8199")
			}
			var cmd *exec.Cmd
			switch {
			case exists(filepath.Join(dir, "main.go")):
				bin := filepath.Join(dir, a.Name)
				c := exec.CommandContext(ctx, "go", "build", "-o", bin, ".")
				c.Dir = dir
				if out, err := c.CombinedOutput(); err != nil {
					return Fail("go build gagal: %s", tail(string(out), 400))
				}
				cmd = exec.Command(bin)
			case exists(filepath.Join(dir, "server.js")):
				cmd = exec.Command("node", "server.js")
			case exists(filepath.Join(dir, "app.py")):
				cmd = exec.Command("python3", "app.py")
			default:
				return Okf("ℹ️ Proyek static (tanpa server) — layani via Caddy/nginx atau buka index.html langsung.")
			}
			cmd.Dir = dir
			cmd.Env = append(os.Environ(), fmt.Sprintf("PORT=%d", port))
			logFile, err := os.OpenFile(filepath.Join(dir, "run.log"), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
			if err != nil {
				return Fail("%v", err)
			}
			cmd.Stdout = logFile
			cmd.Stderr = logFile
			if err := cmd.Start(); err != nil {
				return Fail("gagal start: %v", err)
			}
			pid := cmd.Process.Pid
			os.WriteFile(filepath.Join(dir, ".aleph-pid"), []byte(fmt.Sprintf("%d %d", pid, port)), 0o644)
			go cmd.Wait()
			time.Sleep(700 * time.Millisecond)
			if !processAlive(pid) {
				log := tail(readSmall(filepath.Join(dir, "run.log")), 400)
				os.Remove(filepath.Join(dir, ".aleph-pid"))
				return Fail("proses langsung mati. Log:\n%s", log)
			}
			return Okf("🚀 Proyek %q jalan — http://127.0.0.1:%d (PID %d, log: %s/run.log). Verifikasi dengan verify_project.", a.Name, port, pid, dir)
		},
	})

	reg.Register(Tool{
		Name:        "verify_project",
		Description: "Cek proyek yang jalan: GET endpoint, return status HTTP + potongan body + tail log. WAJIB dipakai setelah run_project untuk self-verify.",
		Params: map[string]interface{}{
			"name": map[string]interface{}{"type": "string"},
			"path": map[string]interface{}{"type": "string", "description": "path endpoint (default /)"},
		},
		Required: []string{"name"},
		Fn: func(ctx context.Context, args string) Result {
			var a struct {
				Name string `json:"name"`
				Path string `json:"path"`
			}
			if err := json.Unmarshal([]byte(args), &a); err != nil {
				return Fail("args tidak valid")
			}
			dir, err := resolveProject(a.Name)
			if err != nil {
				return Fail("%v", err)
			}
			_, port := readPidPort(dir)
			if port == 0 {
				return Fail("proyek %q belum jalan (tidak ada record port) — run_project dulu", a.Name)
			}
			path := a.Path
			if path == "" {
				path = "/"
			}
			url := fmt.Sprintf("http://127.0.0.1:%d%s", port, path)
			client := &http.Client{Timeout: 10 * time.Second}
			resp, err := client.Get(url)
			if err != nil {
				return Okf("❌ %s tidak merespons (%v). Tail log:\n%s", url, err, tail(readSmall(filepath.Join(dir, "run.log")), 400))
			}
			defer resp.Body.Close()
			buf := make([]byte, 600)
			n, _ := resp.Body.Read(buf)
			return Okf("✅ %s → HTTP %d\n--- body ---\n%s\n--- tail run.log ---\n%s", url, resp.StatusCode, string(buf[:n]), tail(readSmall(filepath.Join(dir, "run.log")), 300))
		},
	})

	reg.Register(Tool{
		Name:        "stop_project",
		Description: "Hentikan proyek yang jalan (kill PID yang tercatat).",
		Params:      map[string]interface{}{"name": map[string]interface{}{"type": "string"}},
		Required:    []string{"name"},
		Fn: func(ctx context.Context, args string) Result {
			var a struct {
				Name string `json:"name"`
			}
			if err := json.Unmarshal([]byte(args), &a); err != nil {
				return Fail("args tidak valid")
			}
			dir, err := resolveProject(a.Name)
			if err != nil {
				return Fail("%v", err)
			}
			pid, port := readPidPort(dir)
			if pid == 0 {
				return Okf("ℹ️ Proyek %q tidak sedang jalan.", a.Name)
			}
			if p, err := os.FindProcess(pid); err == nil {
				p.Kill()
			}
			os.Remove(filepath.Join(dir, ".aleph-pid"))
			return Okf("🛑 Proyek %q (PID %d, port %d) dihentikan.", a.Name, pid, port)
		},
	})

	reg.Register(Tool{
		Name:        "list_projects",
		Description: "Daftar semua proyek di workspace + status jalan/mati + port.",
		Params:      map[string]interface{}{},
		Fn: func(ctx context.Context, args string) Result {
			entries, err := os.ReadDir(WorkspaceRoot)
			if err != nil || len(entries) == 0 {
				return Okf("Workspace kosong — belum ada proyek.")
			}
			var b strings.Builder
			b.WriteString("📁 Workspace proyek:\n")
			for _, e := range entries {
				if !e.IsDir() {
					continue
				}
				dir := filepath.Join(WorkspaceRoot, e.Name())
				pid, port := readPidPort(dir)
				status := "⏹ mati"
				if pid != 0 && processAlive(pid) {
					status = fmt.Sprintf("🟢 jalan :%d", port)
				}
				readme := readSmall(filepath.Join(dir, "README.md"))
				tmpl := ""
				if i := strings.Index(readme, "Template: "); i >= 0 {
					tmpl = " (" + strings.TrimSpace(readme[i+10:]) + ")"
				}
				fmt.Fprintf(&b, "• %s%s — %s\n", e.Name(), tmpl, status)
			}
			return Okf("%s", b.String())
		},
	})
}

// ---- helpers ----

// Okf - hasil sukses dengan formatting.
func Okf(format string, args ...interface{}) Result {
	return Result{Output: fmt.Sprintf(format, args...)}
}

func exists(p string) bool { _, err := os.Stat(p); return err == nil }

func tail(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return "…" + s[len(s)-n:]
}

func readSmall(p string) string {
	b, err := os.ReadFile(p)
	if err != nil {
		return ""
	}
	if len(b) > 4096 {
		return string(b[:4096])
	}
	return string(b)
}

func readPidPort(dir string) (int, int) {
	var pid, port int
	fmt.Sscanf(readSmall(filepath.Join(dir, ".aleph-pid")), "%d %d", &pid, &port)
	return pid, port
}

var sysSignalZero = syscall.Signal(0) // cek proses hidup tanpa efek

func processAlive(pid int) bool {
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return p.Signal(sysSignalZero) == nil
}

func freePort() int {
	for p := 8100; p < 8200; p++ {
		if !portInUse(p) {
			return p
		}
	}
	return 0
}

func portInUse(port int) bool {
	c, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 300*time.Millisecond)
	if err != nil {
		return false
	}
	c.Close()
	return true
}
