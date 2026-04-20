package tools

import (
	"context"
	"testing"
)

type fakeTool struct {
	Base
	got map[string]any
}

func (f *fakeTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	f.got = args
	return "ok", nil
}

func TestRegistryValidation(t *testing.T) {
	r := NewRegistry()
	ft := &fakeTool{Base: Base{
		ToolName: "f",
		Params: &Schema{
			Type: "object",
			Properties: map[string]*Schema{
				"a": {Type: "string"},
				"b": {Type: "integer"},
			},
			Required: []string{"a"},
		},
	}}
	r.Register(ft)
	if _, err := r.Execute(context.Background(), "f", map[string]any{"b": 1}); err == nil {
		t.Fatal("expected missing required a")
	}
	if _, err := r.Execute(context.Background(), "f", map[string]any{"a": 42}); err == nil {
		t.Fatal("expected type error for a")
	}
	if _, err := r.Execute(context.Background(), "f", map[string]any{"a": "x", "b": float64(3)}); err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if _, err := r.Execute(context.Background(), "missing", nil); err == nil {
		t.Fatal("expected unknown tool error")
	}
}

func TestDefinitionsOrder(t *testing.T) {
	r := NewRegistry()
	r.Register(&fakeTool{Base: Base{ToolName: "zebra", Params: &Schema{Type: "object"}}})
	r.Register(&fakeTool{Base: Base{ToolName: "mcp_srv_x", Params: &Schema{Type: "object"}}})
	r.Register(&fakeTool{Base: Base{ToolName: "apple", Params: &Schema{Type: "object"}}})
	defs := r.Definitions()
	names := []string{defs[0].Function.Name, defs[1].Function.Name, defs[2].Function.Name}
	if names[0] != "apple" || names[1] != "zebra" || names[2] != "mcp_srv_x" {
		t.Fatalf("unexpected order: %v", names)
	}
}
