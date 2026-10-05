package telegram

// exec_ctx.go — helper exec dengan context/timeout (hardening H-C/H-D).
// SEMUA exec dari tombol menu & watcher WAJIB lewat sini agar goroutine tidak
// pernah menggantung selamanya (speedtest-cli & systemctl terkadang hang).

import (
	"context"
	"os/exec"
	"time"
)

// execTimeout default untuk command tombol/watcher.
const execTimeout = 90 * time.Second

// runOutput — exec.CommandContext + CombinedOutput dengan timeout.
func runOutput(timeout time.Duration, name string, args ...string) (string, error) {
	if timeout <= 0 {
		timeout = execTimeout
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	return string(out), err
}
