package tools

// delegate_tools.go — G7b: tool delegate_task untuk agent utama.
//
// Agent utama melontarkan sub-tugas ke agent kedua (konteks bersih, tool
// terbatas, budget sendiri). Berguna untuk riset paralel / analisis file
// panjang tanpa mengotori konteks percakapan utama.

import (
	"context"
	"encoding/json"
	"log"
	"sort"
	"strings"
	"time"
)

// Delegator — kemampuan delegasi (dipenuhi *agent.Agent; interface di package
// tools untuk hindari import cycle: agent → tools, jadi tools tak boleh → agent).
type Delegator interface {
	Delegate(ctx context.Context, task, hint string, allowedTools []string, maxRounds, timeoutSec int) (string, error)
}

// MaksDelegateOutput batas hasil sub-agent (harus sinkron dengan agent.MaksOutput).
const MaksDelegateOutput = 8000

// truncateDelegate potong rune-safe.
func truncateDelegate(s string) string {
	r := []rune(s)
	if len(r) <= MaksDelegateOutput {
		return s
	}
	return string(r[:MaksDelegateOutput]) + "\n\n…(hasil dipotong — pecah sub-tugas jadi lebih sempit)"
}

// allowedSubTools — tool yang BOLEH dipakai sub-agent (least privilege).
// TIDAK termasuk: delegate_task (anti-rekursi), exec_command, service_control,
// write_file, scaffold/run/verify_project (least privilege + keamanan).
var allowedSubTools = func() []string {
	all := []string{
		"web_search", "web_fetch",
		"list_uploads", "read_upload", "upload_info",
		"read_file", "list_files",
		"note_get", "note_search", "note_categories",
		"memory_recall",
		"recall_skill",
		"make_document", "make_ppt",
		"server_status",
	}
	sort.Strings(all)
	return all
}()

// RegisterDelegateTool — dipanggil dari main.go setelah agent siap.
func RegisterDelegateTool(reg *Registry, del Delegator) {
	reg.Register(Tool{
		Name: "delegate_task",
		Description: "Delegasikan SATU sub-tugas ke sub-agent (agent kedua dengan konteks bersih). " +
			"Gunakan untuk: riset multi-topik (bisa dipanggil beberapa kali), analisis file panjang, atau pekerjaan mandiri yang hasil ringkasnya cukup. " +
			"Sub-agent TIDAK melihat percakapan kita — cantumkan semua info penting di 'task'/'hint'. " +
			"Hasil akhirnya dikembalikan utuh kepadamu. Tidak untuk tugas 1-langkah sederhana.",
		Params: map[string]interface{}{
			"task":  map[string]interface{}{"type": "string", "description": "deskripsi sub-tugas lengkap & self-contained"},
			"hint":  map[string]interface{}{"type": "string", "description": "opsional: konteks pendukung (path file, nama catatan, fakta relevan)"},
		},
		Required: []string{"task"},
		Fn: func(ctx context.Context, args string) Result {
			var a struct {
				Task string `json:"task"`
				Hint string `json:"hint"`
			}
			if err := json.Unmarshal([]byte(args), &a); err != nil {
				return Fail("args tidak valid")
			}
			if strings.TrimSpace(a.Task) == "" {
				return Fail("task kosong")
			}
			if len(a.Task) > 8000 {
				return Fail("task terlalu panjang (maks 8000 char)")
			}
			if len(a.Hint) > 8000 {
				return Fail("hint terlalu panjang (maks 8000 char)")
			}
			start := time.Now()
			out, err := del.Delegate(ctx, a.Task, a.Hint, allowedSubTools, 15, 300)
			dur := time.Since(start).Round(time.Second)
			if err != nil {
				log.Printf("[delegation] gagal (%v): %v", dur, err)
				return Fail("delegasi gagal: %v", err)
			}
			log.Printf("[delegation] selesai %v — %d char", dur, len(out))
			return Ok(truncateDelegate(out))
		},
	})
}
