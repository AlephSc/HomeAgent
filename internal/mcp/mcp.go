// Package mcp — F8: agent sebagai CLIENT ke MCP server eksternal.
// Transport: stdio (spawn proses) — sesuai spec MCP untuk server lokal.
// Tools MCP yang ditemukan diregistrasi ke registry bot sebagai tool biasa.
package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// Request JSON-RPC 2.0.
type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int             `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int             `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// ServerConfig definisi satu MCP server eksternal (dari config.yaml).
type ServerConfig struct {
	Name    string   `yaml:"name"`    // nama unik, prefix tool: mcp_<name>_<tool>
	Command string   `yaml:"command"` // mis. "npx" atau "/usr/bin/python3"
	Args    []string `yaml:"args"`    // mis. ["-y","@modelcontextprotocol/server-filesystem","/srv/data"]
	Enabled bool     `yaml:"enabled"`
}

// Client koneksi ke satu MCP server (stdio).
type Client struct {
	cfg    ServerConfig
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout *bufio.Reader
	mu     sync.Mutex
	nextID int
	// hasil per request ID (server stdio respons berurutan; cukup simpan terakhir)
	lastResp *response
}

// ToolInfo definisi tool dari MCP server.
type ToolInfo struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

// Start spawn proses MCP server + handshake initialize.
func Start(cfg ServerConfig) (*Client, error) {
	cmd := exec.Command(cfg.Command, cfg.Args...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("spawn MCP server %q gagal: %v", cfg.Name, err)
	}
	c := &Client{cfg: cfg, cmd: cmd, stdin: stdin, stdout: bufio.NewReader(stdout), nextID: 1}

	// initialize handshake
	params, _ := json.Marshal(map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "aleph-agent", "version": "0.8.0"},
	})
	if _, err := c.call(context.Background(), "initialize", params); err != nil {
		cmd.Process.Kill()
		return nil, fmt.Errorf("initialize %q gagal: %v", cfg.Name, err)
	}
	// notification initialized
	notif, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "method": "notifications/initialized",
	})
	fmt.Fprintln(stdin, string(notif))
	return c, nil
}

// call satu JSON-RPC request.
func (c *Client) call(ctx context.Context, method string, params json.RawMessage) (json.RawMessage, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.nextID++
	req := request{JSONRPC: "2.0", ID: c.nextID, Method: method, Params: params}
	line, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	// tulis request dengan timeout dari ctx
	done := make(chan error, 1)
	go func() {
		_, werr := fmt.Fprintln(c.stdin, string(line))
		done <- werr
	}()
	select {
	case werr := <-done:
		if werr != nil {
			return nil, werr
		}
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	// baca respons (skip notification/ lainnya)
	deadline := time.Now().Add(30 * time.Second)
	for {
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("timeout menunggu respons %s", method)
		}
		lineBytes, err := c.readLine(ctx)
		if err != nil {
			return nil, err
		}
		var resp response
		if err := json.Unmarshal(lineBytes, &resp); err != nil {
			continue // bukan JSON-RPC, skip
		}
		if resp.ID != req.ID {
			continue // notification / id lain, skip
		}
		if resp.Error != nil {
			return nil, fmt.Errorf("MCP error %d: %s", resp.Error.Code, resp.Error.Message)
		}
		return resp.Result, nil
	}
}

// readLine satu baris stdout dengan ctx.
func (c *Client) readLine(ctx context.Context) ([]byte, error) {
	type result struct {
		b   []byte
		err error
	}
	ch := make(chan result, 1)
	go func() {
		b, err := c.stdout.ReadBytes('\n')
		ch <- result{b, err}
	}()
	select {
	case r := <-ch:
		return r.b, r.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// ListTools — tools/tools dari MCP server.
func (c *Client) ListTools(ctx context.Context) ([]ToolInfo, error) {
	res, err := c.call(ctx, "tools/list", json.RawMessage("{}"))
	if err != nil {
		return nil, err
	}
	var out struct {
		Tools []ToolInfo `json:"tools"`
	}
	if err := json.Unmarshal(res, &out); err != nil {
		return nil, err
	}
	return out.Tools, nil
}

// CallTool — tools/call.
func (c *Client) CallTool(ctx context.Context, name string, argsJSON string) (string, error) {
	params, _ := json.Marshal(map[string]any{
		"name":      name,
		"arguments": json.RawMessage(argsJSON),
	})
	res, err := c.call(ctx, "tools/call", params)
	if err != nil {
		return "", err
	}
	var out struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	if err := json.Unmarshal(res, &out); err != nil {
		return "", err
	}
	var texts []string
	for _, ct := range out.Content {
		if ct.Text != "" {
			texts = append(texts, ct.Text)
		}
	}
	joined := strings.Join(texts, "\n")
	if out.IsError {
		return joined, fmt.Errorf("tool error")
	}
	return joined, nil
}

// Close matikan proses server.
func (c *Client) Close() {
	c.cmd.Process.Kill()
	c.cmd.Wait()
}
