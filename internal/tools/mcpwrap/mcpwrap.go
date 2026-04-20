// Package mcpwrap turns MCP server tools into nanobot Tool instances.
package mcpwrap

import (
	"context"
	"encoding/json"

	"github.com/hkuds/nanobot-go/internal/mcp"
	"github.com/hkuds/nanobot-go/internal/tools"
)

// Wrap produces Tool instances for every tool advertised by the server, prefixed
// with "mcp_<serverName>_".
func Wrap(serverName string, c *mcp.Client) []tools.Tool {
	defs := c.Tools()
	out := make([]tools.Tool, 0, len(defs))
	for _, d := range defs {
		out = append(out, newWrapper(serverName, d, c))
	}
	return out
}

func newWrapper(server string, d mcp.ToolDef, c *mcp.Client) tools.Tool {
	schema := schemaFromRaw(d.InputSchema)
	return &wrapper{
		Base: tools.Base{
			ToolName:        "mcp_" + server + "_" + d.Name,
			ToolDescription: d.Description,
			Params:          schema,
		},
		server: server,
		tool:   d.Name,
		client: c,
	}
}

func schemaFromRaw(raw map[string]any) *tools.Schema {
	if raw == nil {
		return &tools.Schema{Type: "object"}
	}
	b, _ := json.Marshal(raw)
	s := &tools.Schema{}
	_ = json.Unmarshal(b, s)
	if s.Type == "" {
		s.Type = "object"
	}
	return s
}

type wrapper struct {
	tools.Base
	server string
	tool   string
	client *mcp.Client
}

func (w *wrapper) Execute(ctx context.Context, args map[string]any) (string, error) {
	return w.client.Call(ctx, w.tool, args)
}
