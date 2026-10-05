// Package perm — akses berjenjang (tiered access) untuk aksi bot.
// 🟢 free: langsung | 🟡 gated: eksekusi+backup+audit | 🔴 confirm: tanya user | ⛔ forbidden
package perm

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Tier klasifikasi akses.
type Tier int

const (
	Free Tier = iota
	Gated
	Confirm
	Forbidden
	Denied // tidak masuk daftar path/command sama sekali
)

func (t Tier) String() string {
	switch t {
	case Free:
		return "free"
	case Gated:
		return "gated"
	case Confirm:
		return "confirm"
	case Forbidden:
		return "forbidden"
	}
	return "denied"
}

// Config — sumber aturan (dari config.yaml permissions).
type Config struct {
	Audit           bool     `yaml:"audit"`            // catat aksi gated/confirm
	GatedWriteDirs  []string `yaml:"gated_write_dirs"` // 🟡 boleh tulis (dengan backup)
	ConfirmServices []string `yaml:"confirm_services"` // 🔴 service/container kritis
	ConfirmPaths    []string `yaml:"confirm_paths"`    // 🔴 path sensitif (network, iptables)
	GatedExec       []string `yaml:"gated_exec"`       // 🟡 exec ter-audit (legacy, tak dipakai lagi)
}

// destructiveFragments — potongan command yang SELALU ⛔ (di head ATAU argumen mana pun).
// Dicocokkan pada seluruh string command (lowercase) — murah dan menangkap `rm -rf /`,
// fork bomb (`:(){ :|:& };:`), dd, mkfs, chmod 777 di /, dll.
var destructiveFragments = []string{
	"rm -rf /", "rm -fr /", "mkfs", "of=/dev/", "of=/dev",
	"> /dev/sda", "> /dev/nvme", "> /dev/mmcblk",
	"shutdown", "reboot", "halt", "poweroff", "init 0", "init 6",
	"fork bomb", ":(){", "chmod -r 777 /", "chmod 777 /",
	"mv /* /dev/null", ":(){ :|:& };:",
	"mkfs.ext4", "mkfs.vfat", "wipefs",
	">/dev/sda", "> /dev/nvme", "> /dev/mmcblk",
	"rm -rf /*", ": > /dev/sda", ">> /dev/sda",
	// G6: aleph-guard TIDAK boleh dimatikan/diubah/dihapus oleh bot (aturan admin)
	"aleph-guard", "guard.timer", "guard.service",
}

// destructiveCommand — deteksi command destruktif dari FULL LINE.
func destructiveCommand(cmd string) bool {
	lower := strings.ToLower(cmd)
	for _, frag := range destructiveFragments {
		if strings.Contains(lower, frag) {
			return true
		}
	}
	// pipe ke device block
	if strings.Contains(lower, "of=") && strings.Contains(lower, "/dev/sd") {
		return true
	}
	return false
}

// Heads mengekstrak head (program utama) dari TIAP segmen command, dipisah pipe ; && ||.
// "node server.js | grep x ; tail -f y" → ["node", "grep", "tail"].
// Argumen diabaikan — hanya head yang dinilai.
func Heads(command string) []string {
	var heads []string
	// pecah di | ; && ||
	segs := splitSegments(command)
	for _, seg := range segs {
		fields := strings.Fields(seg)
		if len(fields) == 0 {
			continue
		}
		heads = append(heads, filepath.Base(fields[0]))
	}
	return heads
}

// splitSegments — pecah command di | ; && || (hanya untuk klasifikasi head).
// SADAR-KUTIP: pemisah di dalam "..." / '...' TIDAK dianggap pemisah
// (grep -E "a|b" → satu segmen; jangan lahirkan head palsu "b").
func splitSegments(cmd string) []string {
	var out []string
	var cur strings.Builder
	var quote byte
	peek := func(i int) byte {
		if i+1 < len(cmd) {
			return cmd[i+1]
		}
		return 0
	}
	for i := 0; i < len(cmd); i++ {
		c := cmd[i]
		if quote != 0 {
			// escape backslash di dalam kutip (mis. "a\|b") — lewati char berikut
			if c == '\\' && quote == '"' {
				cur.WriteByte(c)
				if i+1 < len(cmd) {
					cur.WriteByte(cmd[i+1])
					i++
				}
				continue
			}
			if c == quote {
				quote = 0
			}
			cur.WriteByte(c)
			continue
		}
		switch c {
		case '"', '\'':
			quote = c
			cur.WriteByte(c)
		case ';':
			if t := strings.TrimSpace(cur.String()); t != "" {
				out = append(out, t)
			}
			cur.Reset()
		case '|':
			if peek(i) == '|' { // || = pemisah (sama dgn ;)
				if t := strings.TrimSpace(cur.String()); t != "" {
					out = append(out, t)
				}
				cur.Reset()
				i++
				continue
			}
			if t := strings.TrimSpace(cur.String()); t != "" {
				out = append(out, t)
			}
			cur.Reset()
		case '&':
			if peek(i) == '&' { // && = pemisah
				if t := strings.TrimSpace(cur.String()); t != "" {
					out = append(out, t)
				}
				cur.Reset()
				i++
				continue
			}
			cur.WriteByte(c) // '&' tunggal (background) — biarkan
		default:
			cur.WriteByte(c)
		}
	}
	if t := strings.TrimSpace(cur.String()); t != "" {
		out = append(out, t)
	}
	return out
}

// inFlight — singleflight utk command yang sama (dipakai ClassifyExec).
type inFlight struct {
	done   chan struct{}
	choice string
}

// Engine memutuskan tier sebuah aksi.
type Engine struct {
	cfg      Config
	auditLog string // path file audit
	// F-perm: keputusan user per head command ("once"/"always"/"session"/"deny")
	decPath   string // path persist JSON (kosong = tidak persist)
	decMu     sync.Mutex
	decisions map[string]string // head → keputusan
	// singleflight: command identik yang sedang menunggu keputusan → 1 prompt saja
	flightMu sync.Mutex
	flights  map[string]*inFlight
}

// New membuat engine. auditLog boleh "" (audit ke journal saja).
func New(cfg Config, auditLog string) *Engine {
	e := &Engine{cfg: cfg, auditLog: auditLog, decisions: map[string]string{}, flights: map[string]*inFlight{}}
	e.loadDecisions()
	return e
}

// NewPersistent — engine dengan persist keputusan ke path JSON.
func NewPersistent(cfg Config, auditLog, decPath string) *Engine {
	e := &Engine{cfg: cfg, auditLog: auditLog, decisions: map[string]string{}, decPath: decPath, flights: map[string]*inFlight{}}
	e.loadDecisions()
	return e
}

// ---- F-perm: allow once / always / session / deny ----

// AskFunc — prompt user via chat: return "once"|"always"|"session"|"deny".
// Dipenuhi gateway Telegram (inline keyboard 4 tombol).
type AskFunc func(command string) string

// askKey — key context untuk AskFunc.
type askKey struct{}

// WithAsk menyuntikkan prompt func ke context (dipanggil agent loop).
func WithAsk(ctx context.Context, fn AskFunc) context.Context {
	return context.WithValue(ctx, askKey{}, fn)
}

func askFromCtx(ctx context.Context) AskFunc {
	fn, _ := ctx.Value(askKey{}).(AskFunc)
	return fn
}

// decision head → berdasarkan keputusan tersimpan. "" = belum pernah.
func (e *Engine) decision(head string) string {
	e.decMu.Lock()
	defer e.decMu.Unlock()
	return e.decisions[head]
}

// remember simpan keputusan. "session" & "always" disimpan; "once"/"deny" tidak.
// deny disimpan agar tidak ditanya lagi (tapi bisa reset via ResetDecisions).
func (e *Engine) remember(head, choice string) {
	e.decMu.Lock()
	defer e.decMu.Unlock()
	switch choice {
	case "always":
		e.decisions[head] = "always"
	case "session":
		e.decisions[head] = "session"
	case "deny":
		e.decisions[head] = "deny"
		// "once": tak disimpan — nanya lagi di eksekusi berikutnya
	default:
		return
	}
	e.persistLocked()
}

// persistLocked — simpan keputusan "always"/"deny" ke file JSON (best-effort).
// Pemanggil WAJIB memegang decMu.
func (e *Engine) persistLocked() {
	if e.decPath == "" {
		return
	}
	type pf struct {
		Decisions map[string]string `json:"decisions"`
	}
	data, _ := json.MarshalIndent(pf{Decisions: e.decisions}, "", "  ")
	_ = os.WriteFile(e.decPath, data, 0600)
}

// loadDecisions — muat keputusan tersimpan saat engine dibuat (best-effort).
func (e *Engine) loadDecisions() {
	if e.decPath == "" {
		return
	}
	raw, err := os.ReadFile(e.decPath)
	if err != nil {
		return
	}
	var pf struct {
		Decisions map[string]string `json:"decisions"`
	}
	if json.Unmarshal(raw, &pf) == nil && pf.Decisions != nil {
		e.decMu.Lock()
		for h, c := range pf.Decisions {
			if c == "always" || c == "deny" { // session/once tidak di-persist
				e.decisions[h] = c
			}
		}
		e.decMu.Unlock()
	}
}

// ResetDecisions hapus semua keputusan session (utk /perm reset).
func (e *Engine) ResetDecisions() {
	e.decMu.Lock()
	defer e.decMu.Unlock()
	e.decisions = map[string]string{}
	e.persistLocked()
}

// DecisionCount — jumlah head yang sudah punya keputusan "always" (untuk /perm).
func (e *Engine) DecisionCount() int {
	e.decMu.Lock()
	defer e.decMu.Unlock()
	n := 0
	for _, v := range e.decisions {
		if v == "always" {
			n++
		}
	}
	return n
}

// ClassifyExec F-perm: head-based, default-allow dengan prompt 4-pilihan.
// whitelist param diabaikan (kompatibel signature lama).
// Return tier Final: Free (boleh), Confirm (perlu tanya), Forbidden (blocklist), Denied (user deny).
// readOnlyHeads — command baca-saja yang aman dijalankan tanpa prompt izin.
// Jika command hanya terdiri dari head di sini (termasuk via pipe), izinkan langsung.
var readOnlyHeads = map[string]bool{
	"ls": true, "cat": true, "head": true, "tail": true, "grep": true, "find": true,
	"wc": true, "sort": true, "uniq": true, "cut": true, "awk": true, "sed": true,
	"echo": true, "which": true, "whereis": true, "file": true, "stat": true,
	"du": true, "df": true, "free": true, "uptime": true, "date": true, "id": true,
	"whoami": true, "hostname": true, "uname": true, "ps": true, "top": true,
	"ip": true, "ss": true, "netstat": true, "ping": true, "traceroute": true,
	"dig": true, "nslookup": true, "host": true, "arp": true, "route": true,
	"iw": true, "iwconfig": true, "rfkill": true, "hciconfig": true,
	"systemctl": true, "journalctl": true, "dmesg": true, "docker": true,
	"curl": true, "wget": true, "crontab": true, "env": true, "printenv": true,
	"lscpu": true, "lsblk": true, "lspci": true, "lsusb": true, "lsmod": true,
	"vcgencmd": true, "sensors": true, "nproc": true, "getent": true,
	"apt-get": true, "apt": true, "dpkg": true, "snap": true, "pip": true, "pip3": true,
	"mkdir": true, "touch": true, "cp": true, "mv": true, "tee": true, "chmod": true,
	"install": true, "update-rc.d": true, "systemd-tmpfiles": true, "useradd": true,
	"groupadd": true, "sysctl": true, "modprobe": true, "ip6tables": true, "nft": true,
	"sudo": true, "hostapd": true, "wpa_passphrase": true, "wpa_supplicant": true,
	"iptables": true, "iwpriv": true, "udhcpc": true, "wpa_cli": true, "nmcli": true,
	"usermod": true, "passwd": true, "kill": true, "pkill": true, "killall": true,
	"truncate": true, "mount": true, "umount": true, "swapon": true, "swapoff": true,
	"chown": true, "ln": true, "tar": true, "gzip": true, "gunzip": true, "zip": true, "unzip": true,
}

func (e *Engine) ClassifyExec(command string, ctx context.Context, whitelist, gatedExec []string) Tier {
	// 1. blocklist destruktif (full-line match) — TIDAK BISA di-allow
	if destructiveCommand(command) {
		return Forbidden
	}
	heads := Heads(command)
	if len(heads) == 0 {
		return Denied
	}
	// 1b. read-only allowlist — aman, tak perlu prompt (mengurangi spam izin)
	allRO := true
	for _, h := range heads {
		if !readOnlyHeads[h] {
			allRO = false
			break
		}
	}
	if allRO {
		return Free
	}
	// 2. kumpulkan head yang belum punya keputusan → SATU prompt untuk semuanya
	var unknown []string
	for _, h := range heads {
		d := e.decision(h)
		switch d {
		case "deny":
			return Denied
		case "once", "always", "session":
			continue
		default:
			unknown = append(unknown, h)
		}
	}
	if len(unknown) == 0 {
		return Free
	}
	// singleflight: command identik yang sedang menunggu keputusan tidak memicu
	// prompt kedua (tool call duplikat dari model / paralel) — bagikan hasilnya.
	key := strings.Join(unknown, ",") + "|" + command
	e.flightMu.Lock()
	if fl, ok := e.flights[key]; ok {
		e.flightMu.Unlock()
		<-fl.done // tunggu keputusan prompt pertama
		return e.classifyKnown(heads)
	}
	fl := &inFlight{done: make(chan struct{})}
	e.flights[key] = fl
	e.flightMu.Unlock()
	defer func() {
		e.flightMu.Lock()
		delete(e.flights, key)
		e.flightMu.Unlock()
		close(fl.done)
	}()

	fn := askFromCtx(ctx)
	if fn == nil {
		// tanpa kanal tanya (mis. cron): default ALLOW + audit (longgar)
		return Free
	}
	// G1c: tampilkan command utuh + daftar head yang ditanya
	choice := strings.ToLower(strings.TrimSpace(fn(command + "\n\nProgram: " + strings.Join(unknown, ", "))))
	switch choice {
	case "always", "session":
		for _, h := range unknown {
			e.remember(h, choice) // sekali jawab → semua head tersimpan
		}
	case "deny":
		for _, h := range unknown {
			e.remember(h, "deny")
		}
		return Denied
	default: // once / jawaban aneh → izinkan sekali ini saja (once diclear oleh caller)
		for _, h := range unknown {
			e.remember(h, "once")
		}
	}
	fl.choice = choice
	return Free
}

// classifyKnown — evaluasi ulang heads SETELAH singleflight selesai (keputusan sudah tersimpan).
func (e *Engine) classifyKnown(heads []string) Tier {
	for _, h := range heads {
		d := e.decision(h)
		if d == "deny" {
			return Denied
		}
	}
	return Free
}

// ConsumeOnce — setelah eksekusi sukses, hapus keputusan "once" per head.
func (e *Engine) ConsumeOnce(command string) {
	for _, h := range Heads(command) {
		e.decMu.Lock()
		if e.decisions[h] == "once" {
			delete(e.decisions, h)
		}
		e.decMu.Unlock()
	}
}

// ClassifyWrite menentukan tier penulisan file ke path.
// freeDirs = sandbox baca+bebas tulis; hasil: Free/Gated/Forbidden/Denied.
func (e *Engine) ClassifyWrite(path string, freeDirs []string) Tier {
	clean := filepath.Clean(path)
	lower := strings.ToLower(clean)

	// ⛔ forbidden: file kredensial & config bot sendiri
	for _, f := range []string{"config.yaml", ".env", "id_rsa", "authorized_keys", "shadow", "hermes_tmp_key"} {
		if strings.HasSuffix(lower, "/"+f) || lower == f {
			return Forbidden
		}
	}
	if strings.Contains(lower, "/.ssh/") {
		return Forbidden
	}

	// 🔴 confirm: path sensitif (network, firewall)
	for _, p := range e.cfg.ConfirmPaths {
		if matchDir(p, clean) {
			return Confirm
		}
	}

	// 🟢 free: dalam sandbox dirs
	for _, d := range freeDirs {
		if matchDir(d, clean) {
			return Free
		}
	}

	// 🟡 gated: dalam gated_write_dirs
	for _, d := range e.cfg.GatedWriteDirs {
		if matchDir(d, clean) {
			return Gated
		}
	}
	// 🟡 SEMUA path lain di luar sandbox → GATED (backup otomatis + audit), BUKAN tolak.
	// Kebijakan user: akses root penuh, pagar hanya kredensial (forbidden di atas)
	// dan blocklist perintah destruktif di ClassifyExec.
	return Gated
}

// ClassifyService menentukan tier aksi service/container.
// action: status|start|stop|restart; unit: "dnsmasq" atau "docker:adguardhome".
func (e *Engine) ClassifyService(action, unit string) Tier {
	if action == "status" {
		return Free
	}
	name := strings.TrimPrefix(unit, "docker:")
	for _, s := range e.cfg.ConfirmServices {
		if s == name {
			// stop service kritis selalu confirm; start/restart kritis juga confirm
			return Confirm
		}
	}
	return Gated
}

// GatedExec mengembalikan daftar command tier 🟡 (legacy, kompatibilitas).
func (e *Engine) GatedExec() []string { return e.cfg.GatedExec }

// Backup menyalin file ke <path>.bak-<timestamp> sebelum ditimpa (tier Gated).
func (e *Engine) Backup(path string) (string, error) {
	if _, err := os.Stat(path); err != nil {
		return "", nil // file baru, tak perlu backup
	}
	dst := fmt.Sprintf("%s.bak-%s", path, time.Now().Format("20060102-150405"))
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(dst, data, 0o644); err != nil {
		return "", err
	}
	return dst, nil
}

// Audit mencatat aksi gated/confirm (file + journal via log).
func (e *Engine) Audit(action, detail string) {
	line := fmt.Sprintf("%s | %s | %s\n", time.Now().Format("2006-01-02 15:04:05"), action, detail)
	if e.cfg.Audit {
		if e.auditLog != "" {
			os.MkdirAll(filepath.Dir(e.auditLog), 0o755)
			f, err := os.OpenFile(e.auditLog, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
			if err == nil {
				f.WriteString(line)
				f.Close()
			}
		}
		fmt.Printf("[audit] %s | %s", action, detail)
	}
}

// TailAudit membaca N baris terakhir audit log (untuk /audit).
func (e *Engine) TailAudit(n int) []string {
	if e.auditLog == "" {
		return nil
	}
	data, err := os.ReadFile(e.auditLog)
	if err != nil {
		return nil
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines
}

// matchDir: clean == dir atau clean di dalam dir.
func matchDir(dir, clean string) bool {
	dAbs, err := filepath.Abs(dir)
	if err != nil {
		return false
	}
	return clean == dAbs || strings.HasPrefix(clean, dAbs+string(filepath.Separator)) ||
		clean == dir || strings.HasPrefix(clean, dir+"/")
}
