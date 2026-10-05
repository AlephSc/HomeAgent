// Package config — loader config.yaml aleph-agent
package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Config adalah root konfigurasi aleph-agent.
type Config struct {
	Server struct {
		Name       string   `yaml:"name"`         // nama tampilan server
		LogTag     string   `yaml:"log_tag"`      // journalctl -t <tag>
		DataDir    string   `yaml:"data_dir"`     // /opt/aleph-agent
		SandboxDir []string `yaml:"sandbox_dirs"` // path yang boleh diakses tools file
	} `yaml:"server"`

	Token struct {
		Telegram string `yaml:"telegram"` // bot token dari @BotFather
		Discord  string `yaml:"discord"`  // bot token dari developer portal (F4)
	} `yaml:"token"`

	Admins struct {
		TelegramIDs []int64  `yaml:"telegram_ids"` // user ID yang boleh memerintah
		DiscordIDs  []string `yaml:"discord_ids"`
	} `yaml:"admins"`

	LLM struct {
		BaseURL      string   `yaml:"base_url"`       // 9Router endpoint (https://.../v1)
		APIKey       string   `yaml:"api_key"`        // key (9r-... atau lainnya)
		Model        string   `yaml:"model"`          // model default
		Models       []string `yaml:"models"`         // daftar model yg boleh dipakai
		MaxToolCalls int      `yaml:"max_tool_calls"` // budget guard (default 10)
		TimeoutSec   int      `yaml:"timeout_sec"`    // timeout per request (default 90)
		// F6a — auto context compacting
		ContextMaxTokens int `yaml:"context_max_tokens"` // verbatim budget (default 65536)
	} `yaml:"llm"`

	Web struct {
		BraveAPIKey string `yaml:"brave_api_key"` // Brave Search (opsional, F6)
	} `yaml:"web"`

	Alert struct {
		Enabled     bool     `yaml:"enabled"`
		DiskMax     int      `yaml:"disk_max_percent"` // default 90
		RAMMinMB    int      `yaml:"ram_min_mb"`       // default 250
		DebounceMin int      `yaml:"debounce_minutes"` // default 30
		WatchList   []string `yaml:"watch_services"`   // unit systemd yang dipantau
	} `yaml:"alert"`

	MCP struct {
		Servers []struct {
			Name    string   `yaml:"name"`    // prefix tool: mcp_<name>_<tool>
			Command string   `yaml:"command"` // proses MCP server (stdio)
			Args    []string `yaml:"args"`
			Enabled bool     `yaml:"enabled"`
		} `yaml:"servers"` // F8
	} `yaml:"mcp"`

	Permissions struct {
		Audit           bool     `yaml:"audit"`
		GatedWriteDirs  []string `yaml:"gated_write_dirs"` // 🟡 tulis dengan backup
		ConfirmServices []string `yaml:"confirm_services"` // 🔴 service kritis
		ConfirmPaths    []string `yaml:"confirm_paths"`    // 🔴 path sensitif
		GatedExec       []string `yaml:"gated_exec"`       // 🟡 exec butuh audit
	} `yaml:"permissions"`
}

// Load membaca config dari path.
func Load(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("baca %s: %w", path, err)
	}
	cfg := &Config{}
	if err := yaml.Unmarshal(raw, cfg); err != nil {
		return nil, fmt.Errorf("parse yaml: %w", err)
	}
	applyDefaults(cfg)
	if err := validate(cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

func applyDefaults(c *Config) {
	if c.Server.Name == "" {
		c.Server.Name = "aleph"
	}
	if c.Server.LogTag == "" {
		c.Server.LogTag = "aleph-agent"
	}
	if c.Server.DataDir == "" {
		c.Server.DataDir = "./data"
	}
	if c.LLM.MaxToolCalls == 0 {
		c.LLM.MaxToolCalls = 10
	}
	if c.LLM.TimeoutSec == 0 {
		c.LLM.TimeoutSec = 90
	}
	if c.Alert.DiskMax == 0 {
		c.Alert.DiskMax = 90
	}
	if c.Alert.RAMMinMB == 0 {
		c.Alert.RAMMinMB = 250
	}
	if c.Alert.DebounceMin == 0 {
		c.Alert.DebounceMin = 30
	}
	// Default permissions (Fase A)
	if len(c.Permissions.GatedWriteDirs) == 0 {
		c.Permissions.GatedWriteDirs = []string{"/etc/systemd/system"}
	}
	if len(c.Permissions.ConfirmServices) == 0 {
		c.Permissions.ConfirmServices = []string{"dnsmasq", "docker", "tailscaled"}
	}
	if len(c.Permissions.ConfirmPaths) == 0 {
		c.Permissions.ConfirmPaths = []string{"/etc/network/interfaces", "/etc/dnsmasq.d", "/etc/nftables.conf", "/etc/iptables"}
	}
	if len(c.Permissions.GatedExec) == 0 {
		c.Permissions.GatedExec = []string{"systemctl", "ping", "curl", "host"}
	}
	// F6a — default compacting
	if c.LLM.ContextMaxTokens == 0 {
		c.LLM.ContextMaxTokens = 65536
	}
}

func validate(c *Config) error {
	if c.Token.Telegram == "" {
		return fmt.Errorf("token.telegram wajib diisi (dari @BotFather)")
	}
	if len(c.Admins.TelegramIDs) == 0 {
		return fmt.Errorf("admins.telegram_ids wajib diisi (user ID anda)")
	}
	return nil
}

// IsTelegramAdmin memeriksa apakah user ID boleh memerintah bot.
func (c *Config) IsTelegramAdmin(id int64) bool {
	for _, a := range c.Admins.TelegramIDs {
		if a == id {
			return true
		}
	}
	return false
}
