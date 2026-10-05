package main

// aleph-agent — Hybrid Bot + AI Agent untuk home server aleph
// F2: agent path (LLM + tools) + model changer

import (
	"flag"
	"fmt"
	"net/http"
	_ "net/http/pprof" // F9: pprof di 127.0.0.1:6060 (hanya bila -pprof)
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"aleph-agent/internal/agent"
	"aleph-agent/internal/alerts"
	"aleph-agent/internal/chatlog"
	"aleph-agent/internal/config"
	"aleph-agent/internal/cron"
	"aleph-agent/internal/fastpath"
	"aleph-agent/internal/gateways/discord"
	"aleph-agent/internal/gateways/telegram"
	"aleph-agent/internal/llm"
	"aleph-agent/internal/mcp"
	"aleph-agent/internal/memory"
	"aleph-agent/internal/perm"
	"aleph-agent/internal/server"
	"aleph-agent/internal/tools"
	"aleph-agent/internal/webui"
)

var version = "0.9.1-P"

func main() {
	cfgPath := flag.String("config", "config.yaml", "path ke file config")
	pprof := flag.Bool("pprof", false, "aktifkan pprof debug server di 127.0.0.1:6060 (F9)")
	flag.Parse()

	// F9: pprof opsional — bind localhost saja, tidak ke jaringan
	if *pprof {
		go func() {
			fmt.Println("[pprof] debug server di http://127.0.0.1:6060/debug/pprof/")
			if err := http.ListenAndServe("127.0.0.1:6060", nil); err != nil {
				fmt.Println("[pprof] mati:", err)
			}
		}()
	}

	fmt.Printf("aleph-agent %s\n", version)

	// ---- Config ----
	cfg, err := config.Load(*cfgPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "FATAL: config: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("config loaded: %s (admin: %d user)\n", *cfgPath, len(cfg.Admins.TelegramIDs))

	// ---- Server metrics (untuk /status) ----
	srv := server.New(cfg.Server.Name, cfg.Alert.WatchList)

	// maxRounds: dari config, default 10
	maxRounds := cfg.LLM.MaxToolCalls

	// ---- Fast path (slash commands) ----
	fast := fastpath.New(cfg, srv)
	// (SetMemory dipanggil setelah memory store dibuka, di bawah)

	// ---- Tools registry ----
	reg := tools.NewRegistry()
	tools.RegisterServerTools(reg, srv, cfg.Server.SandboxDir, []string{
		"df", "uptime", "free", "docker", "ip", "ss", "journalctl", "dmesg", "date", "whoami", "uname", "vcgencmd",
	})
	tools.RegisterWebTools(reg, cfg.Web.BraveAPIKey)

	// ---- Permission engine (Fase A: tiered access) ----
	auditLog := "/opt/aleph-agent/audit.log"
	if runtime.GOOS == "windows" {
		auditLog = "audit.log"
	}
	permDecPath := "/opt/aleph-agent/data/perm.json"
	if runtime.GOOS == "windows" {
		permDecPath = "perm.json"
	}
	permEng := perm.NewPersistent(perm.Config{
		Audit:           true,
		GatedWriteDirs:  cfg.Permissions.GatedWriteDirs,
		ConfirmServices: cfg.Permissions.ConfirmServices,
		ConfirmPaths:    cfg.Permissions.ConfirmPaths,
		GatedExec:       cfg.Permissions.GatedExec,
	}, auditLog, permDecPath)
	tools.SetPermEngine(permEng)
	fmt.Printf("[perm] aktif — gated %d dir, confirm %d service, %d path\n",
		len(cfg.Permissions.GatedWriteDirs), len(cfg.Permissions.ConfirmServices), len(cfg.Permissions.ConfirmPaths))

	// ---- Memory + Skill + Lesson (F5/F5b/F5c) ----
	var memStore *memory.Store
	dbPath := filepath.Join(cfg.Server.DataDir, "aleph.db")
	if runtime.GOOS == "windows" {
		dbPath = "aleph.db"
	}
	memStore, err = memory.Open(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "WARN: memory DB gagal: %v (fitur memory nonaktif)\n", err)
		memStore = nil
	} else {
		defer memStore.Close()
		fmt.Printf("[memory] aktif — %s (%s)\n", dbPath, memStore.Stats())
	}
	skillsDir := filepath.Join(cfg.Server.DataDir, "skills")
	if err := tools.SetSkillsDir(skillsDir); err != nil {
		fmt.Fprintf(os.Stderr, "WARN: skills dir gagal: %v\n", err)
	}
	if memStore != nil {
		tools.RegisterMemoryTools(reg, memStore)
		tools.RegisterSkillTools(reg, memStore)
		tools.RegisterNoteTools(reg, memStore) // G2 notes obsidian
		fast.SetMemory(memStore)
	}

	// ---- Builder workspace (F10) ----
	projectsDir := "/opt/aleph-agent/projects"
	if runtime.GOOS == "windows" {
		projectsDir = "projects"
	}
	tools.SetWorkspace(projectsDir)
	tools.RegisterBuilderTools(reg)
	if permEng != nil {
		// tulis di dalam workspace = bebas (sudah tercakup sandbox default), catat saja
		permEng.Audit("workspace", projectsDir+" (builder aktif)")
	}
	fmt.Printf("[builder] aktif — workspace %s (%d tools)\n", projectsDir, len(reg.Names()))

	// ---- Agent path (F2) ----
	var ag *agent.Agent
	if cfg.LLM.BaseURL != "" && cfg.LLM.APIKey != "" {
		client := llm.New(cfg.LLM.BaseURL, cfg.LLM.APIKey, cfg.LLM.Model)
		// extra prompt: skill index + lesson terkonfirmasi (F5b/F5c)
		extra := tools.SkillIndex()
		if memStore != nil {
			if ls := memStore.Lessons(); len(ls) > 0 {
				extra += "\nPELAJARAN TERKONFIRMASI (ikuti jika relevan):\n" + strings.Join(ls, "\n") + "\n"
				memStore.TouchLessons()
			}
			if m := memStore.Search("preferensi", 5); len(m) > 0 {
				extra += "\nPREFERENSI TERSIMPAN:\n" + strings.Join(m, "\n") + "\n"
			}
		}
		prompt := agent.BuildPromptExtra(cfg.Server.Name, reg.Summary(), time.Now().Format("2006-01-02 15:04 MST"), maxRounds, extra)
		ag = agent.New(client, reg, prompt, maxRounds)
		ag.DataDir = cfg.Server.DataDir  // B4: persist overlay
		ag.ServerName = cfg.Server.Name  // G11 H-1: bahan rebuild prompt
		ag.PromptExtra = extra           // G11 H-1
		ag.SetToolsRef(reg)              // G11 H-1
		ag.ApplyPersist()                // B4: pulihkan config runtime tersimpan
		if memStore != nil {
			ag.Session = memStore    // F5: session context per chat
			ag.Summarizer = memStore // F6a: running summary per chat
			ag.Memory = memStore     // F6b: auto-recall
			ag.ContextBudget = cfg.LLM.ContextMaxTokens
			if ag.ContextBudget <= 0 {
				// 8k verbatim cukup (≈30 pesan terakhir); sisanya ditutup running summary.
				// 64k verbatim di N2600 membuat inference per-round menit → deadline (bug 2026-10-03).
				ag.ContextBudget = 8192
			}
			// Chat log harian utk debugging: <data_dir>/chat/YYYY-MM-DD.log
			ag.Log = chatlog.New(cfg.Server.DataDir)
		}
		fmt.Printf("[agent] aktif — model %s @ %s (%d tools)\n", cfg.LLM.Model, cfg.LLM.BaseURL, len(reg.Names()))
	} else {
		fmt.Println("[info] llm belum dikonfigurasi — agent path nonaktif")
	}

	// ---- Kurator memory (F6b): tiap 6 jam bersihkan memory usang ----
	if memStore != nil {
		go func() {
			for {
				time.Sleep(6 * time.Hour)
				if n := memStore.Curate(); n > 0 {
					fmt.Printf("[memory] kurator: %d memory usang dihapus\n", n)
				}
			}
		}()
	}

	// ---- MCP client (F8): spawn MCP server eksternal, daftarkan tools-nya ----
	mcpCount := 0
	for _, s := range cfg.MCP.Servers {
		if !s.Enabled || s.Command == "" {
			continue
		}
		if n, err := mcp.RegisterServer(reg, mcp.ServerConfig{
			Name: s.Name, Command: s.Command, Args: s.Args, Enabled: s.Enabled,
		}); err != nil {
			fmt.Fprintf(os.Stderr, "WARN: MCP %q: %v\n", s.Name, err)
		} else {
			mcpCount += n
		}
	}
	if mcpCount > 0 {
		fmt.Printf("[mcp] aktif — %d tools dari MCP server eksternal\n", mcpCount)
	}

	// ---- Cron scheduler (F7): job terjadwal + self-verify + alert ----
	// (dibuat setelah telegram gateway siap; di-init di bawah)

	// ---- Telegram gateway ----
	tg, err := telegram.New(cfg, fast, ag, reg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "FATAL: telegram: %v\n", err)
		os.Exit(1)
	}
	// G1: wiring store + folder file/inbox untuk fitur upload
	tg.SetStore(memStore)
	filesDir := filepath.Join(cfg.Server.DataDir, "files")
	if runtime.GOOS == "windows" {
		filesDir = "files"
	}
	telegram.SetFilesRoot(filesDir)
	tools.RegisterUploadTools(reg, memStore, filesDir)
	tools.RegisterLearnTools(reg, filesDir) // G4: make_document + make_ppt
	if ag != nil {
		tools.RegisterDelegateTool(reg, ag) // G7b: delegate_task (sub-agent)
		tools.SetCurrentAgent(ag)           // G10: self_tool_save → RebuildPrompt
	}
	tools.SetCurrentRegistry(reg)         // G10
	if err := tools.InitSelfTools(cfg.Server.DataDir, reg); err != nil { // G10: tool kustom
		fmt.Printf("[selftool] init: %v\n", err)
	}
	fmt.Printf("[agent] total tools terdaftar: %d\n", len(reg.Names()))
	tg.StartUploadServices()

	// G3: web UI via Tailscale — bind hanya ke antarmuka tailscale/localhost
	wui := webui.New(memStore, ag, filesDir, cfg.Admins.TelegramIDs[0])
	go func() {
		addr := webui.PickAddr()
		fmt.Printf("[webui] aktif — http://%s (token via /cfgweb di Telegram)\n", addr)
		if err := wui.ListenAndServe(addr); err != nil {
			fmt.Println("[webui] mati:", err)
		}
	}()
	tg.SetWebUI(wui.Token()) // /cfgweb kirim URL+token ke admin
	go tg.Run()

	// ---- Cron scheduler init (butuh tg sebagai Notifier) ----
	sched := cron.New(tg, ag)
	cronFile := filepath.Join(cfg.Server.DataDir, "cron.json")
	if err := sched.LoadJobs(cronFile); err == nil {
		fmt.Printf("[cron] aktif — job dimuat dari %s\n", cronFile)
	}
	tools.SetCronScheduler(sched, cronFile) // G11 H-2
	tools.RegisterCronTools(reg)            // G11 H-2: cron_add/list/del
	if ag != nil {
		ag.RebuildPrompt() // G11: sertakan tools baru di prompt
	}
	go sched.Run()
	defer sched.Stop()

	// ---- Alert engine (F3, non-AI 0 token) ----
	if cfg.Alert.Enabled {
		eng := alerts.New(cfg, srv, tg.SendDirect)
		go eng.Run()
		fmt.Printf("[alert] aktif — disk>%d%%, ram<%dMB, watch %d service\n",
			cfg.Alert.DiskMax, cfg.Alert.RAMMinMB, len(cfg.Alert.WatchList))
	}

	// ---- Discord gateway (F4) ----
	if cfg.Token.Discord != "" {
		if dg, err := discord.New(cfg, fast, ag); err != nil {
			fmt.Fprintf(os.Stderr, "WARN: discord: %v\n", err)
		} else {
			go func() {
				if err := dg.Run(); err != nil {
					fmt.Fprintf(os.Stderr, "WARN: discord run: %v\n", err)
				}
			}()
			fmt.Println("[discord] gateway aktif")
		}
	}

	fmt.Println("aleph-agent siap. Ctrl+C untuk keluar.")
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	fmt.Println("shutdown.")
}
