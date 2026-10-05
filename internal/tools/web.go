// Package tools — web search & fetch.
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// RegisterWebTools menambahkan web_search & web_fetch.
// braveKey: API key Brave Search (kosong = tool menolak dengan pesan jelas).
func RegisterWebTools(r *Registry, braveKey string) {
	client := &http.Client{Timeout: 30 * time.Second}

	// === web_search ===
	r.Register(Tool{
		Name:        "web_search",
		Description: "Cari di web. Kembalikan daftar hasil: judul, URL, deskripsi singkat.",
		Params: map[string]interface{}{
			"query": map[string]interface{}{"type": "string"},
		},
		Required: []string{"query"},
		Fn: func(ctx context.Context, args string) Result {
			if braveKey == "" {
				return Fail("Brave Search belum dikonfigurasi (llm.brave_api_key kosong)")
			}
			var a struct {
				Query string `json:"query"`
			}
			if err := json.Unmarshal([]byte(args), &a); err != nil || strings.TrimSpace(a.Query) == "" {
				return Fail("query kosong")
			}
			req, _ := http.NewRequestWithContext(ctx, "GET",
				"https://api.search.brave.com/res/v1/web/search?q="+url.QueryEscape(a.Query)+"&count=5", nil)
			req.Header.Set("X-Subscription-Token", braveKey)
			req.Header.Set("Accept", "application/json")
			resp, err := client.Do(req)
			if err != nil {
				return Fail("search http: %v", err)
			}
			defer resp.Body.Close()
			data, _ := io.ReadAll(io.LimitReader(resp.Body, 512<<10))
			if resp.StatusCode != 200 {
				return Fail("search HTTP %d: %.200s", resp.StatusCode, string(data))
			}
			var br struct {
				Web struct {
					Results []struct {
						Title       string `json:"title"`
						URL         string `json:"url"`
						Description string `json:"description"`
					} `json:"results"`
				} `json:"web"`
			}
			if err := json.Unmarshal(data, &br); err != nil {
				return Fail("parse search: %v", err)
			}
			if len(br.Web.Results) == 0 {
				return Ok("tidak ada hasil")
			}
			var b strings.Builder
			for i, res := range br.Web.Results {
				fmt.Fprintf(&b, "%d. %s\n   %s\n   %s\n", i+1, res.Title, res.URL, res.Description)
			}
			return Ok(b.String())
		},
	})

	// === web_fetch ===
	r.Register(Tool{
		Name:        "web_fetch",
		Description: "Ambil isi halaman web (HTML→teks bersih, maks 12 KB).",
		Params: map[string]interface{}{
			"url": map[string]interface{}{"type": "string"},
		},
		Required: []string{"url"},
		Fn: func(ctx context.Context, args string) Result {
			var a struct {
				URL string `json:"url"`
			}
			if err := json.Unmarshal([]byte(args), &a); err != nil {
				return Fail("args tidak valid")
			}
			u, err := url.Parse(a.URL)
			if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
				return Fail("URL tidak valid: %s", a.URL)
			}
			// blokir akses ke localhost/internal (SSRF guard)
			host := u.Hostname()
			if host == "localhost" || strings.HasPrefix(host, "127.") || strings.HasPrefix(host, "192.168.") ||
				strings.HasPrefix(host, "10.") || strings.HasPrefix(host, "100.") || strings.HasPrefix(host, "172.") {
				return Fail("URL internal diblokir")
			}
			req, _ := http.NewRequestWithContext(ctx, "GET", a.URL, nil)
			req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; aleph-agent/0.2)")
			resp, err := client.Do(req)
			if err != nil {
				return Fail("fetch: %v", err)
			}
			defer resp.Body.Close()
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
			if resp.StatusCode != 200 {
				return Fail("HTTP %d: %.200s", resp.StatusCode, string(body))
			}
			return Ok(htmlToText(string(body)))
		},
	})
}

var (
	tagScript = regexp.MustCompile(`(?is)<(script|style|noscript)[^>]*>.*?</(script|style|noscript)>`)
	tagAny    = regexp.MustCompile(`(?s)<[^>]+>`)
	spaces    = regexp.MustCompile(`[ 	]+`)
	lines     = regexp.MustCompile(`\n{3,}`)
)

// htmlToText — strip HTML kasar jadi teks.
func htmlToText(s string) string {
	s = tagScript.ReplaceAllString(s, " ")
	s = tagAny.ReplaceAllString(s, "\n")
	// decode entity umum
	repl := strings.NewReplacer("&amp;", "&", "&lt;", "<", "&gt;", ">", "&quot;", `"`, "&#39;", "'", "&nbsp;", " ")
	s = repl.Replace(s)
	out := lines.ReplaceAllString(spaces.ReplaceAllString(s, " "), "\n\n")
	out = strings.TrimSpace(out)
	if len(out) > 12<<10 {
		out = out[:12<<10] + "\n…[dipotong]"
	}
	return out
}
