package tools

import (
	"context"
	"strings"
	"testing"
	"time"
)

type fakeDelegator struct {
	gotTools []string
	delay    time.Duration
}

func (f *fakeDelegator) Delegate(ctx context.Context, task, hint string, allowed []string, maxRounds, timeoutSec int) (string, error) {
	if f.delay > 0 {
		time.Sleep(f.delay)
	}
	f.gotTools = allowed
	return "hasil: " + task, nil
}

func TestDelegateToolBasics(t *testing.T) {
	reg := NewRegistry()
	fd := &fakeDelegator{}
	RegisterDelegateTool(reg, fd)
	tool, ok := reg.Get("delegate_task")
	if !ok {
		t.Fatal("delegate_task tidak terdaftar")
	}
	// args valid
	res := tool.Fn(context.Background(), `{"task":"riset A"}`)
	if res.Err != "" || !strings.Contains(res.Output, "hasil: riset A") {
		t.Fatalf("hasil salah: %q / err=%q", res.Output, res.Err)
	}
	// anti-rekursi: delegate_task TIDAK boleh di allowlist
	for _, name := range fd.gotTools {
		if name == "delegate_task" || name == "exec_command" || name == "service_control" || name == "write_file" {
			t.Fatalf("tool terlarang di allowlist sub-agent: %s", name)
		}
	}
	// task kosong ditolak
	res = tool.Fn(context.Background(), `{"task":"  "}`)
	if res.Err == "" {
		t.Fatal("task kosong harus ditolak")
	}
	// task raksasa ditolak
	big := `{"task":"` + strings.Repeat("x", 9000) + `"}`
	res = tool.Fn(context.Background(), big)
	if res.Err == "" {
		t.Fatal("task oversize harus ditolak")
	}
	// args rusak ditolak
	res = tool.Fn(context.Background(), `bukan-json`)
	if res.Err == "" {
		t.Fatal("args rusak harus ditolak")
	}
}

func TestTruncateDelegateRuneSafe(t *testing.T) {
	s := strings.Repeat("é", MaksDelegateOutput+50) // rune 2-byte
	out := truncateDelegate(s)
	if len(out) >= len(s) {
		t.Fatal("harus dipotong")
	}
	// tidak membelah rune: harus tetap valid UTF-8 (no replacement char di tengah)
	if strings.ContainsRune(out[:MaksDelegateOutput+10], 0xFFFD) {
		t.Fatal("rune terbelah")
	}
}

func TestCloneAllowed(t *testing.T) {
	reg := NewRegistry()
	reg.Register(Tool{Name: "a", Fn: func(ctx context.Context, args string) Result { return Ok("") }})
	reg.Register(Tool{Name: "b", Fn: func(ctx context.Context, args string) Result { return Ok("") }})
	reg.Register(Tool{Name: "delegate_task", Fn: func(ctx context.Context, args string) Result { return Ok("") }})
	cl := reg.CloneAllowed([]string{"a", "delegate_task"})
	if _, ok := cl.Get("b"); ok {
		t.Fatal("tool di luar allowlist bocor")
	}
	if _, ok := cl.Get("a"); !ok {
		t.Fatal("tool allowlist hilang")
	}
}
