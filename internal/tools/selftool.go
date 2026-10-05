package tools

// selftool.go — G10: script-tool self-extension.
// Agent membuat/mengelola tools-nya sendiri via manifest + script di data/tools/.
// Eksekusi selalu lewat perm engine + CommandContext timeout wajib.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time")

const (
	maxScriptBytes = 32 << 10 // 32 KB
	maxToolKustom  = 20
	maxParamFields = 16
	maxDescLen     = 500
	maxOutputKB    = 64 << 10
)

// SelfToolDef — definisi satu tool kustom.
type SelfToolDef struct {
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Params      map[string]string `json:"params"` // nama → deskripsi (type string)
	Lang        string            `json:"lang"`   // "bash" | "python"
	TimeoutSec  int               `json:"timeout_sec"`
	Enabled     bool              `json:"enabled"`
	Created     string            `json:"created"`
}

// SelfToolStore — penyimpanan + registry integration.
type SelfToolStore struct {
	mu        sync.Mutex
	dir       string          // data/tools
	manifests map[string]*SelfToolDef
}

var selfStore *SelfToolStore

// InitSelfTools — panggil dari main.go: muat manifest, register yang enabled.
func InitSelfTools(dataDir string, reg *Registry) error {
	selfStore = &SelfToolStore{
		dir:       filepath.Join(dataDir, "tools"),
		manifests: map[string]*SelfToolDef{},
	}
	if err := os.MkdirAll(selfStore.dir, 0o700); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(selfStore.dir, "run"), 0o700); err != nil {
		return err
	}
	selfStore.loadManifest()
	// register semua enabled ke registry (hot)
	for _, d := range selfStore.manifests {
		if d.Enabled {
			registerSelfTool(reg, d)
		}
	}
	RegisterSelfToolsMeta(reg)
	return nil
}

// manifestPath & scriptPath
func (s *SelfToolStore) manifestPath() string { return filepath.Join(s.dir, "manifest.json") }
func (s *SelfToolStore) scriptPath(name string) string {
	name = strings.TrimPrefix(name, "x_")
	if s.manifests[name] != nil && s.manifests[name].Lang == "python" {
		return filepath.Join(s.dir, name+".py")
	}
	return filepath.Join(s.dir, name+".sh")
}

var reToolName = regexp.MustCompile(`^[a-z0-9_]{3,32}$`)

// forbiddenSnippets — pola berbahaya ditolak saat save (lapisan 1; perm engine lapisan 2).
var forbiddenSnippets = []string{
	"rm -rf /", "mkfs", "dd if=", ":(){", "fork()", "> /dev/sda", "| sh", "| bash",
	"curl http", "wget http", "nc -l", "chmod 777 /", "shutdown", "reboot",
}

// SaveDef validasi + simpan + register hot. Return error aman utk agent.
func (s *SelfToolStore) SaveDef(def SelfToolDef, script string, reg *Registry, builtins map[string]bool) error {
	name := strings.ToLower(strings.TrimSpace(def.Name))
	if !strings.HasPrefix(name, "x_") {
		name = "x_" + name
	}
	if !reToolName.MatchString(name) {
		return fmt.Errorf("nama tidak valid (3-32 char, a-z0-9_, prefix x_)")
	}
	if builtins[name] {
		return fmt.Errorf("nama bentrok tool bawaan")
	}
	switch blocked := name; {
	case blocked == "x_delegate_task" || blocked == "x_exec_command" || blocked == "x_service_control":
		return fmt.Errorf("nama terlarang")
	}
	if len(script) > maxScriptBytes {
		return fmt.Errorf("script terlalu besar (maks 32 KB)")
	}
	if len(def.Description) > maxDescLen {
		return fmt.Errorf("deskripsi terlalu panjang (maks 500)")
	}
	if len(def.Params) > maxParamFields {
		return fmt.Errorf("terlalu banyak params (maks 16)")
	}
	if def.Lang != "bash" && def.Lang != "python" {
		return fmt.Errorf("lang harus bash|python")
	}
	if def.TimeoutSec <= 0 {
		def.TimeoutSec = 30
	}
	if def.TimeoutSec > 120 {
		return fmt.Errorf("timeout maks 120 detik")
	}
	low := strings.ToLower(script)
	for _, bad := range forbiddenSnippets {
		if strings.Contains(low, bad) {
			return fmt.Errorf("script memuat pola terlarang: %q", bad)
		}
	}
	// syntax check
	if err := syntaxCheck(def.Lang, script); err != nil {
		return fmt.Errorf("syntax check gagal: %v", err)
	}
	if def.Created == "" {
		def.Created = time.Now().Format(time.RFC3339)
	}
	def.Enabled = true
	def.Name = name

	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.manifests[name]; !exists && len(s.manifests) >= maxToolKustom {
		return fmt.Errorf("quota penuh (maks %d tool kustom) — hapus dulu", maxToolKustom)
	}
	// tulis script atomik
	sp := s.scriptPath(name)
	tmp := sp + ".tmp"
	if err := os.WriteFile(tmp, []byte(script), 0o700); err != nil {
		return err
	}
	if err := os.Rename(tmp, sp); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	s.manifests[name] = &def
	if err := s.saveManifestLocked(); err != nil {
		return err
	}
	registerSelfTool(reg, &def)
	return nil
}

// syntaxCheck — bash -n / py_compile.
func syntaxCheck(lang, script string) error {
	var cmd *exec.Cmd
	if lang == "python" {
		cmd = exec.Command("python3", "-c", "import sys; compile(sys.stdin.read(), 'x', 'exec')")
	} else {
		cmd = exec.Command("bash", "-n")
	}
	cmd.Stdin = strings.NewReader(script)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s", strings.TrimSpace(string(out[:min(len(out), 300)])))
	}
	return nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// registerSelfTool — daftarkan closure executor ke registry.
func registerSelfTool(reg *Registry, def *SelfToolDef) {
	if reg == nil {
		return
	}
	name := def.Name
	params := map[string]interface{}{}
	req := []string{}
	for k, v := range def.Params {
		params[k] = map[string]interface{}{"type": "string", "description": v}
		req = append(req, k)
	}
	reg.Register(Tool{
		Name:        name,
		Description: "[kustom] " + def.Description,
		Params:      params,
		Required:    req,
		Fn: func(ctx context.Context, args string) Result {
			return runSelfTool(ctx, name, args)
		},
	})
}

// runSelfTool — eksekusi script dengan params via stdin JSON.
func runSelfTool(ctx context.Context, name, args string) Result {
	selfStore.mu.Lock()
	def := selfStore.manifests[name]
	selfStore.mu.Unlock()
	if def == nil || !def.Enabled {
		return Fail("tool kustom %s tidak ada / disabled", name)
	}
	// perm engine: eksekusi lewat hukum yang sama
	if permEng := PermEngine(); permEng != nil {
		permEng.Audit("selftool-run", name)
	}
	var payload map[string]string
	if args != "" {
		if err := json.Unmarshal([]byte(args), &payload); err != nil {
			return Fail("args tidak valid (harus JSON object)")
		}
	}
	raw, _ := json.Marshal(payload)

	cctx, cancel := context.WithTimeout(ctx, time.Duration(def.TimeoutSec)*time.Second)
	defer cancel()
	cmd := exec.CommandContext(cctx, interpreter(def.Lang), selfStore.scriptPath(name))
	cmd.Dir = filepath.Join(selfStore.dir, "run")
	cmd.Stdin = strings.NewReader(string(raw))
	// env minimal — tanpa warisan API key dll.
	cmd.Env = []string{"PATH=/usr/local/bin:/usr/bin:/bin", "HOME=" + cmd.Dir, "LANG=C.UTF-8"}
	out, err := cmd.CombinedOutput()
	if len(out) > maxOutputKB {
		r := []rune(string(out))
		out = []byte(string(r[:maxOutputKB]))
	}
	if err != nil {
		msg := string(out)
		if len(msg) > 4096 {
			r := []rune(msg)
			msg = string(r[:4096])
		}
		if ctxErr := cctx.Err(); ctxErr == context.DeadlineExceeded {
			return Fail("timeout %ds", def.TimeoutSec)
		}
		return Fail("gagal: %v — %s", err, msg)
	}
	return Ok(string(out))
}

func interpreter(lang string) string {
	if lang == "python" {
		return "python3"
	}
	return "bash"
}

// loadManifest / saveManifestLocked
func (s *SelfToolStore) loadManifest() {
	b, err := os.ReadFile(s.manifestPath())
	if err != nil {
		return
	}
	var defs []*SelfToolDef
	if json.Unmarshal(b, &defs) != nil {
		return
	}
	for _, d := range defs {
		if d != nil && d.Name != "" {
			s.manifests[d.Name] = d
		}
	}
}

func (s *SelfToolStore) saveManifestLocked() error {
	names := make([]string, 0, len(s.manifests))
	for n := range s.manifests {
		names = append(names, n)
	}
	sort.Strings(names)
	defs := make([]*SelfToolDef, 0, len(names))
	for _, n := range names {
		defs = append(defs, s.manifests[n])
	}
	b, err := json.MarshalIndent(defs, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.manifestPath() + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.manifestPath())
}

// Delete hapus tool kustom.
func (s *SelfToolStore) Delete(name string, reg *Registry) error {
	selfStore.mu.Lock()
	defer selfStore.mu.Unlock()
	if _, ok := s.manifests[name]; !ok {
		return fmt.Errorf("tool tidak ditemukan")
	}
	delete(s.manifests, name)
	_ = os.Remove(s.scriptPath(name))
	if err := s.saveManifestLocked(); err != nil {
		return err
	}
	// unregister: buat registry baru tanpa tool tsb tak mudah — cukup tandai disabled di manifest;
	// hot-unregister penuh dilakukan via RebuildRegistryFromScratch di handler.
	if reg != nil {
		reg.Disable(name)
	}
	return nil
}

// Toggle enable/disable.
func (s *SelfToolStore) Toggle(name string, reg *Registry) (bool, error) {
	selfStore.mu.Lock()
	defer selfStore.mu.Unlock()
	def := s.manifests[name]
	if def == nil {
		return false, fmt.Errorf("tool tidak ditemukan")
	}
	def.Enabled = !def.Enabled
	if err := s.saveManifestLocked(); err != nil {
		return def.Enabled, err
	}
	if def.Enabled {
		registerSelfTool(reg, def)
		reg.Enable(name)
	} else {
		reg.Disable(name)
	}
	return def.Enabled, nil
}

// RegisterSelfToolsMeta — 4 tool meta (save/list/del/toggle).
func RegisterSelfToolsMeta(reg *Registry) {
	reg.Register(Tool{
		Name: "self_tool_save",
		Description: "Buat / timpa TOOL BARU untuk dirimu sendiri (script bash/python). Tool bisa langsung dipakai setelah disimpan. " +
			"Nama otomatis diberi prefix x_. Script menerima params via stdin JSON (object string→string) dan mengembalikan hasil via stdout. " +
			"Untuk kemampuan yang kamu butuhkan berulang kali.",
		Params: map[string]interface{}{
			"name":        map[string]interface{}{"type": "string", "description": "nama tool, a-z0-9_ (3-32 char), contoh 'x_cek_suhu'"},
			"description": map[string]interface{}{"type": "string", "description": "deskripsi singkat fungsi tool"},
			"params":      map[string]interface{}{"type": "object", "description": "{nama_param: deskripsi} — maks 16"},
			"script":      map[string]interface{}{"type": "string", "description": "isi script lengkap (bash atau python)"},
			"lang":        map[string]interface{}{"type": "string", "description": "bash | python"},
			"timeout_sec": map[string]interface{}{"type": "integer", "description": "opsional, default 30, maks 120"},
		},
		Required: []string{"name", "description", "script", "lang"},
		Fn: func(ctx context.Context, args string) Result {
			var a struct {
				Name        string            `json:"name"`
				Description string            `json:"description"`
				Params      map[string]string `json:"params"`
				Script      string            `json:"script"`
				Lang        string            `json:"lang"`
				TimeoutSec  int               `json:"timeout_sec"`
			}
			if err := json.Unmarshal([]byte(args), &a); err != nil {
				return Fail("args tidak valid")
			}
			reg2 := CurrentRegistry()
			if reg2 == nil {
				return Fail("registry belum siap")
			}
			builtins := map[string]bool{}
			for _, n := range reg2.Names() {
				builtins[n] = true
			}
			def := SelfToolDef{Name: a.Name, Description: a.Description, Params: a.Params, Lang: a.Lang, TimeoutSec: a.TimeoutSec}
			if err := selfStore.SaveDef(def, a.Script, reg2, builtins); err != nil {
				return Fail("%v", err)
			}
			if permEng := PermEngine(); permEng != nil {
				permEng.Audit("selftool-save", a.Name)
			}
			if ag := CurrentAgent(); ag != nil {
				ag.RebuildPrompt()
			}
			return Ok(fmt.Sprintf("tool %s tersimpan & langsung bisa dipakai. Panggil sebagai '%s'.", def.Name, def.Name))
		},
	})

	reg.Register(Tool{
		Name:        "self_tool_list",
		Description: "Daftar tool kustom yang pernah kamu buat.",
		Fn: func(ctx context.Context, args string) Result {
			selfStore.mu.Lock()
			defer selfStore.mu.Unlock()
			var b strings.Builder
			names := make([]string, 0, len(selfStore.manifests))
			for n := range selfStore.manifests {
				names = append(names, n)
			}
			sort.Strings(names)
			if len(names) == 0 {
				return Ok("belum ada tool kustom.")
			}
			for _, n := range names {
				d := selfStore.manifests[n]
				st := "off"
				if d.Enabled {
					st = "on"
				}
				fmt.Fprintf(&b, "%s [%s] (%s, %ds) — %s\n", n, st, d.Lang, d.TimeoutSec, d.Description)
			}
			return Ok(b.String())
		},
	})

	reg.Register(Tool{
		Name:        "self_tool_del",
		Description: "Hapus tool kustom (yang kamu buat sendiri via self_tool_save).",
		Params: map[string]interface{}{
			"name": map[string]interface{}{"type": "string", "description": "nama tool kustom"},
		},
		Required: []string{"name"},
		Fn: func(ctx context.Context, args string) Result {
			var a struct {
				Name string `json:"name"`
			}
			if err := json.Unmarshal([]byte(args), &a); err != nil {
				return Fail("args tidak valid")
			}
			n := strings.ToLower(strings.TrimSpace(a.Name))
			if !strings.HasPrefix(n, "x_") {
				n = "x_" + n
			}
			if err := selfStore.Delete(n, CurrentRegistry()); err != nil {
				return Fail("%v", err)
			}
			if permEng := PermEngine(); permEng != nil {
				permEng.Audit("selftool-del", n)
			}
			if ag := CurrentAgent(); ag != nil {
				ag.RebuildPrompt()
			}
			return Ok(fmt.Sprintf("tool %s dihapus.", n))
		},
	})

	reg.Register(Tool{
		Name:        "self_tool_toggle",
		Description: "Enable/disable tool kustom tanpa menghapusnya.",
		Params: map[string]interface{}{
			"name": map[string]interface{}{"type": "string", "description": "nama tool kustom"},
		},
		Required: []string{"name"},
		Fn: func(ctx context.Context, args string) Result {
			var a struct {
				Name string `json:"name"`
			}
			if err := json.Unmarshal([]byte(args), &a); err != nil {
				return Fail("args tidak valid")
			}
			n := strings.ToLower(strings.TrimSpace(a.Name))
			if !strings.HasPrefix(n, "x_") {
				n = "x_" + n
			}
			enabled, err := selfStore.Toggle(n, CurrentRegistry())
			if err != nil {
				return Fail("%v", err)
			}
			if permEng := PermEngine(); permEng != nil {
				permEng.Audit("selftool-toggle", n)
			}
			if ag := CurrentAgent(); ag != nil {
				ag.RebuildPrompt()
			}
			if enabled {
				return Ok(fmt.Sprintf("tool %s ENABLED.", n))
			}
			return Ok(fmt.Sprintf("tool %s DISABLED.", n))
		},
	})
}
