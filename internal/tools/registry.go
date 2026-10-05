// Package tools — registry tool native in-process untuk agent.
package tools

import (
	"sync"
	"context"
	"fmt"
	"sort"
	"strings"

	"aleph-agent/internal/llm"
)

// Result hasil eksekusi tool.
type Result struct {
	Output string // teks untuk model
	Err    string // error (kosong = sukses)
}

// Ok membuat hasil sukses.
func Ok(s string) Result { return Result{Output: s} }

// Fail membuat hasil error.
func Fail(format string, args ...interface{}) Result {
	return Result{Err: fmt.Sprintf(format, args...)}
}

// Func — tanda tangan fungsi tool. Args = JSON object string dari model.
type Func func(ctx context.Context, args string) Result

// Tool — definisi satu tool.
type Tool struct {
	Name        string
	Description string
	Params      map[string]interface{} // JSON schema properties
	Required    []string
	Fn          Func
}

// Registry kumpulan tool.
type Registry struct {
	muTool   sync.Mutex
	tools    map[string]Tool
	disabled map[string]ToolFunc
}

// NewRegistry membuat registry kosong.
func NewRegistry() *Registry {
	return &Registry{tools: map[string]Tool{}, disabled: map[string]ToolFunc{}}
}

// Register menambah tool.
func (r *Registry) Register(t Tool) {
	r.tools[t.Name] = t
}

// Get mengambil tool by name.
func (r *Registry) Get(name string) (Tool, bool) {
	t, ok := r.tools[name]
	return t, ok
}

// CloneAllowed — registry baru berisi HANYA tool dengan nama di allowlist.
// Dipakai G7b: sub-agent tidak boleh mendelegasikan lagi / akses tool berbahaya.
func (r *Registry) CloneAllowed(allowed []string) *Registry {
	n := NewRegistry()
	for _, name := range allowed {
		if t, ok := r.tools[name]; ok {
			n.tools[name] = t
		}
	}
	return n
}

// Names daftar nama tool terurut.
func (r *Registry) Names() []string {
	names := make([]string, 0, len(r.tools))
	for n := range r.tools {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// LLMDefs mengubah registry jadi definisi tools untuk LLM.
func (r *Registry) LLMDefs() []llm.ToolDef {
	defs := make([]llm.ToolDef, 0, len(r.tools))
	for _, name := range r.Names() {
		t := r.tools[name]
		var d llm.ToolDef
		d.Type = "function"
		d.Function.Name = t.Name
		d.Function.Description = t.Description
		d.Function.Parameters = map[string]interface{}{
			"type":       "object",
			"properties": t.Params,
			"required":   t.Required,
		}
		defs = append(defs, d)
	}
	return defs
}

// Summary ringkasan tool untuk /help.
func (r *Registry) Summary() string {
	var b strings.Builder
	for _, name := range r.Names() {
		fmt.Fprintf(&b, "- %s: %s\n", name, r.tools[name].Description)
	}
	return b.String()
}


// ===== global registry & agent pointers (untuk self-tool hot ops) =====
var (
	currentRegistry *Registry
	currentAgent    AgentRef
)

// AgentRef — minimal interface yang dibutuhkan selftool (hindari import cycle).
type AgentRef interface {
	RebuildPrompt()
}

// SetCurrentRegistry — dipanggil main.go setelah registry utama dibuat.
func SetCurrentRegistry(r *Registry) { currentRegistry = r }

// CurrentRegistry — registry aktif (nil jika belum diset).
func CurrentRegistry() *Registry { return currentRegistry }

// SetCurrentAgent — dipanggil main.go setelah agent dibuat.
func SetCurrentAgent(a AgentRef) { currentAgent = a }

// CurrentAgent — agent aktif (nil jika belum diset).
func CurrentAgent() AgentRef { return currentAgent }

// Disable — tandai tool nonaktif (tetap di registry tapi Fn gagal).
func (r *Registry) Disable(name string) {
	r.muTool.Lock()
	defer r.muTool.Unlock()
	if t, ok := r.tools[name]; ok {
		old := t.Fn
		t.Fn = func(ctx context.Context, args string) Result {
			return Fail("tool %s sedang disabled", name)
		}
		r.tools[name] = t
		r.disabled[name] = old
	}
}

// Enable — aktifkan kembali dari disabled.
func (r *Registry) Enable(name string) {
	r.muTool.Lock()
	defer r.muTool.Unlock()
	if old, ok := r.disabled[name]; ok {
		if t, ok2 := r.tools[name]; ok2 {
			t.Fn = old
			r.tools[name] = t
		}
		delete(r.disabled, name)
	}
}

// ToolFunc — tipe fungsi tool.
type ToolFunc = func(ctx context.Context, args string) Result
