// Package llm — client 9Router (OpenAI-compatible chat completions)
package llm

import (
	"sync"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Client bicara ke endpoint OpenAI-compatible (9Router).
type Client struct {
	BaseURL string
	APIKey  string

	mu    sync.Mutex // lindungi Model dari akses paralel (SetModel web UI vs Chat loop)
	model string

	HTTP *http.Client
}

// SetModel ganti model (aman antar-goroutine).
func (c *Client) SetModel(m string) {
	c.mu.Lock()
	c.model = m
	c.mu.Unlock()
}

// Model baca model aktif (aman antar-goroutine).
func (c *Client) Model() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.model
}

// Message — pesan chat (role: system/user/assistant/tool).
type Message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	Name       string     `json:"name,omitempty"`
}

// ToolCall — permintaan tool dari model.
type ToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"` // JSON string
	} `json:"function"`
}

// ToolDef — definisi tool untuk model (function calling).
type ToolDef struct {
	Type     string `json:"type"`
	Function struct {
		Name        string                 `json:"name"`
		Description string                 `json:"description"`
		Parameters  map[string]interface{} `json:"parameters"`
	} `json:"function"`
}

// Response — jawaban chat completion.
type Response struct {
	ID      string `json:"id"`
	Model   string `json:"model"`
	Choices []struct {
		Message      Message `json:"message"`
		FinishReason string  `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		TotalTokens int `json:"total_tokens"`
	} `json:"usage"`
}

// Error — format error 9Router/OpenAI.
type apiError struct {
	Error struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error"`
}

// New membuat client.
func New(baseURL, apiKey, model string) *Client {
	return &Client{
		BaseURL: baseURL,
		APIKey:  apiKey,
		model:   model,
		HTTP:    &http.Client{Timeout: 120 * time.Second},
	}
}

// Chat mengirim percakapan + tools, mengembalikan pesan balasan model.
func (c *Client) Chat(ctx context.Context, messages []Message, tools []ToolDef) (*Message, error) {
	return c.chatRaw(ctx, messages, tools, nil)
}

// ChatVision mengirim prompt + 1 gambar (data URL base64) — untuk analisis foto.
// Model di sisi router yang menentukan apakah vision didukung.
func (c *Client) ChatVision(ctx context.Context, prompt string, imageDataURL string) (*Message, error) {
	body := map[string]interface{}{
		"model": c.Model(),
		"messages": []map[string]interface{}{
			{"role": "system", "content": "Kamu asisten server home. Jawab SELALU dalam Bahasa Indonesia, ringkas."},
			{"role": "user", "content": []map[string]interface{}{
				{"type": "text", "text": prompt},
				{"type": "image_url", "image_url": map[string]string{"url": imageDataURL}},
			}},
		},
		"max_tokens":  2048,
		"temperature": 0.4,
	}
	return c.chatRawBody(ctx, body)
}

// chatRawBody mengirim body map mentah (dipakai ChatVision).
func (c *Client) chatRawBody(ctx context.Context, body map[string]interface{}) (*Message, error) {
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, "POST", c.BaseURL+"/chat/completions", bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.APIKey)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}
	if resp.StatusCode != 200 {
		var ae apiError
		_ = json.Unmarshal(data, &ae)
		if ae.Error.Message == "" {
			return nil, fmt.Errorf("HTTP %d: %.200s", resp.StatusCode, string(data))
		}
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, ae.Error.Message)
	}
	var cr Response
	if err := json.Unmarshal(data, &cr); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	if len(cr.Choices) == 0 {
		return nil, fmt.Errorf("respons tanpa choices")
	}
	return &cr.Choices[0].Message, nil
}

// chatRaw — implementasi inti Chat (dipisah agar bisa dipakai ulang).
func (c *Client) chatRaw(ctx context.Context, messages []Message, tools []ToolDef, _ interface{}) (*Message, error) {
	body := map[string]interface{}{
		"model":       c.Model(),
		"messages":    messages,
		"max_tokens":  4096,
		"temperature": 0.4,
	}
	if len(tools) > 0 {
		body["tools"] = tools
	}

	raw, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", c.BaseURL+"/chat/completions", bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.APIKey)

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http: %w", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}

	if resp.StatusCode != 200 {
		var ae apiError
		_ = json.Unmarshal(data, &ae)
		if ae.Error.Message == "" {
			return nil, fmt.Errorf("HTTP %d: %.200s", resp.StatusCode, string(data))
		}
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, ae.Error.Message)
	}

	var cr Response
	if err := json.Unmarshal(data, &cr); err != nil {
		return nil, fmt.Errorf("parse response: %w (%.200s)", err, string(data))
	}
	if len(cr.Choices) == 0 {
		return nil, fmt.Errorf("respons tanpa choices")
	}
	return &cr.Choices[0].Message, nil
}
