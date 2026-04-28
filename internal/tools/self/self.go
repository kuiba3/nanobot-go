// Package self implements the `my` tool — a controlled self-introspection and
// self-config surface for the agent loop.
package self

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/kuiba3/nanobot-go/internal/tools"
)

// Accessor abstracts the agent loop's self-facing state. Concrete loops
// provide a small implementation that mutates their fields.
type Accessor interface {
	Snapshot() map[string]any
	Set(key string, value any) error
	SupportedKeys() []string
}

// New builds the self tool.
func New(acc Accessor) tools.Tool {
	return &myTool{
		Base: tools.Base{
			ToolName:        "my",
			ToolDescription: "Introspect or mutate runtime agent settings (model, temperature, max_iterations...).",
			Params: &tools.Schema{
				Type: "object",
				Properties: map[string]*tools.Schema{
					"action": {Type: "string", Enum: []any{"get", "set", "keys"}, Description: "get | set | keys"},
					"key":    {Type: "string", Description: "field name (required for get/set)"},
					"value":  {Description: "new value for set"},
				},
				Required: []string{"action"},
			},
		},
		acc: acc,
	}
}

type myTool struct {
	tools.Base
	mu  sync.Mutex
	acc Accessor
}

func (t *myTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	action := tools.ArgString(args, "action", "")
	t.mu.Lock()
	defer t.mu.Unlock()
	switch action {
	case "keys":
		return join(t.acc.SupportedKeys()), nil
	case "get":
		snap := t.acc.Snapshot()
		if key := tools.ArgString(args, "key", ""); key != "" {
			v, ok := snap[key]
			if !ok {
				return "", fmt.Errorf("unknown key %q", key)
			}
			return jsonStr(v), nil
		}
		b, _ := json.MarshalIndent(snap, "", "  ")
		return string(b), nil
	case "set":
		key := tools.ArgString(args, "key", "")
		if key == "" {
			return "", fmt.Errorf("set requires key")
		}
		v, ok := args["value"]
		if !ok {
			return "", fmt.Errorf("set requires value")
		}
		if err := t.acc.Set(key, v); err != nil {
			return "", err
		}
		return "ok", nil
	}
	return "", fmt.Errorf("unknown action %q", action)
}

func jsonStr(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func join(xs []string) string {
	out := ""
	for i, x := range xs {
		if i > 0 {
			out += "\n"
		}
		out += "- " + x
	}
	return out
}
