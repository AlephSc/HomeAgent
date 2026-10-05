// Package discord — F4: gateway Discord (parity Telegram inti).
// Fitur: pesan natural → agent path, /status /help /menu, admin-only,typing indicator.
// Parity penuh (menu tombol, foto vision) menyusul — fondasi dulu.
package discord

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"

	"aleph-agent/internal/agent"
	"aleph-agent/internal/config"
	"aleph-agent/internal/fastpath"
)

// Gateway Discord.
type Gateway struct {
	cfg   *config.Config
	fast  *fastpath.Handler
	agent *agent.Agent
	sess  *discordgo.Session
}

// New buat session Discord (tanpa connect — Run yang connect).
func New(cfg *config.Config, fast *fastpath.Handler, ag *agent.Agent) (*Gateway, error) {
	sess, err := discordgo.New("Bot " + cfg.Token.Discord)
	if err != nil {
		return nil, err
	}
	return &Gateway{cfg: cfg, fast: fast, agent: ag, sess: sess}, nil
}

// Run connect + loop event (blocking; panggil dalam goroutine).
func (g *Gateway) Run() error {
	g.sess.AddHandler(g.onMessage)
	g.sess.Identify.Intents = discordgo.IntentsGuildMessages | discordgo.IntentsDirectMessages
	if err := g.sess.Open(); err != nil {
		return err
	}
	log.Printf("[discord] authorized sebagai %s", g.sess.State.User.Username)
	select {} // loop event via callback
}

// SendDirect kirim DM ke admin Discord pertama (dipakai alert/cron).
func (g *Gateway) SendDirect(text string) {
	for _, id := range g.cfg.Admins.DiscordIDs {
		ch, err := g.sess.UserChannelCreate(id)
		if err != nil {
			continue
		}
		g.sess.ChannelMessageSend(ch.ID, truncate(text, 1900))
	}
}

// onMessage handler semua pesan.
func (g *Gateway) onMessage(s *discordgo.Session, m *discordgo.MessageCreate) {
	// abaikan bot lain & diri sendiri
	if m.Author == nil || m.Author.Bot {
		return
	}
	// admin-only
	if !g.isDiscordAdmin(m.Author.ID) {
		return
	}
	content := strings.TrimSpace(m.Content)
	if content == "" {
		return
	}
	// strip mention bot di depan
	content = strings.TrimPrefix(content, "<@"+s.State.User.ID+">")
	content = strings.TrimSpace(content)

	start := time.Now()
	_ = s.ChannelTyping(m.ChannelID)

	var reply string
	if strings.HasPrefix(content, "/") {
		reply = g.handleCommand(content)
	} else if g.agent != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 240*time.Second)
		defer cancel()
		chatKey := int64(len(m.Author.ID))<<48 | int64(fnv(m.Author.ID))
		ans, err := g.agent.HandleChat(ctx, chatKey, content)
		if err != nil {
			reply = "⚠️ Agent error: " + err.Error()
		} else {
			reply = ans
		}
	} else {
		reply = "🤖 Agent path belum aktif."
	}
	if reply == "" {
		reply = "(tanpa jawaban)"
	}
	_ = s.ChannelTyping(m.ChannelID)
	_, _ = s.ChannelMessageSendComplex(m.ChannelID, &discordgo.MessageSend{
		Content: truncate(reply, 1900),
		Reference: &discordgo.MessageReference{
			MessageID: m.ID, ChannelID: m.ChannelID,
		},
	})
	log.Printf("[discord] balas %s (%d ms)", m.Author.Username, time.Since(start).Milliseconds())
}

// handleCommand — slash command via prefix / (parity fastpath; Handle menangani semua).
func (g *Gateway) handleCommand(text string) string {
	return g.fast.Handle(text)
}

func (g *Gateway) isDiscordAdmin(id string) bool {
	for _, a := range g.cfg.Admins.DiscordIDs {
		if a == id {
			return true
		}
	}
	return false
}

// fnv — hash kecil utk ID string → int64 (session key Discord).
func fnv(s string) uint64 {
	var h uint64 = 14695981039346656037
	for i := 0; i < len(s); i++ {
		h ^= uint64(s[i])
		h *= 1099511628211
	}
	return h
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}

var _ = fmt.Sprintf
