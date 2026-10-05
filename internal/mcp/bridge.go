// Package mcp — bridge: daftarkan tools MCP server eksternal ke registry bot.
package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"aleph-agent/internal/tools"
)

// RegisterServer — start MCP server + daftarkan semua tools-nya ke registry.
// Tool muncul sebagai mcp_<server>_<tool>. Return jumlah tool terdaftar.
func RegisterServer(reg *tools.Registry, cfg ServerConfig) (int, error) {
	client, err := Start(cfg)
	if err != nil {
		return 0, err
	}
	toolList, err := client.ListTools(context.Background())
	if err != nil {
		client.Close()
		return 0, err
	}
	n := 0
	for _, t := range toolList {
		tool := t // capture
		name := fmt.Sprintf("mcp_%s_%s", cfg.Name, tool.Name)
		// konversi inputSchema MCP → params registry (sudah JSON schema, cocok)
		params := map[string]interface{}{}
		if len(tool.InputSchema) > 0 {
			var schema struct {
				Properties map[string]interface{} `json:"properties"`
				Required   []string               `json:"required"`
			}
			if json.Unmarshal(tool.InputSchema, &schema) == nil {
				params = schema.Properties
				reg.Register(Tool_with_required(name, tool.Description, params, schema.Required, client, tool.Name))
				n++
				continue
			}
		}
		reg.Register(Tool_with_required(name, tool.Description, params, nil, client, tool.Name))
		n++
	}
	log.Printf("[mcp] server %q: %d tools terdaftar", cfg.Name, n)
	return n, nil
}

// Tool_with_required buat Tool dengan required list + closure call ke MCP client.
func Tool_with_required(name, desc string, params map[string]interface{}, required []string, c *Client, mcpName string) tools.Tool {
	return tools.Tool{
		Name:        name,
		Description: desc + " (via MCP server " + c.cfg.Name + ")",
		Params:      params,
		Required:    required,
		Fn: func(ctx context.Context, args string) tools.Result {
			out, err := c.CallTool(ctx, mcpName, args)
			if err != nil && out == "" {
				return tools.Fail("MCP call gagal: %v", err)
			}
			if err != nil {
				return tools.Fail("%s", out)
			}
			return tools.Result{Output: out}
		},
	}
}
