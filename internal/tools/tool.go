// Package tools defines the Tool interface and a Registry that the agent
// loop consults for tool invocations. Mirrors Python nanobot/agent/tools.
package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/hkuds/nanobot-go/internal/provider"
)

// Schema is a JSON-schema-like parameter declaration (object shape).
type Schema struct {
	Type                 string             `json:"type"`
	Properties           map[string]*Schema `json:"properties,omitempty"`
	Items                *Schema            `json:"items,omitempty"`
	Required             []string           `json:"required,omitempty"`
	Description          string             `json:"description,omitempty"`
	Enum                 []any              `json:"enum,omitempty"`
	AdditionalProperties any                `json:"additionalProperties,omitempty"`
	Default              any                `json:"default,omitempty"`
}

// AsMap converts the schema into a generic map suitable for serialization as
// OpenAI function parameters.
func (s *Schema) AsMap() map[string]any {
	if s == nil {
		return nil
	}
	b, _ := json.Marshal(s)
	m := map[string]any{}
	_ = json.Unmarshal(b, &m)
	return m
}

// Tool is what the agent registry stores. Implementations are responsible for
// argument validation (either manually or via Registry helpers).
type Tool interface {
	Name() string
	Description() string
	Parameters() *Schema
	ReadOnly() bool
	ConcurrencySafe() bool
	Exclusive() bool
	Execute(ctx context.Context, args map[string]any) (string, error)
}

// Base is a convenient embeddable struct for tools with trivial flag values.
type Base struct {
	ToolName        string
	ToolDescription string
	Params          *Schema
	IsReadOnly      bool
	IsConcurrent    bool
	IsExclusive     bool
}

// Name returns the tool's stable identifier.
func (b *Base) Name() string { return b.ToolName }

// Description returns a human-readable summary surfaced to the LLM.
func (b *Base) Description() string { return b.ToolDescription }

// Parameters returns the JSON-schema-like parameter definition.
func (b *Base) Parameters() *Schema { return b.Params }

// ReadOnly reports whether the tool may be batched with other read-only tools.
func (b *Base) ReadOnly() bool { return b.IsReadOnly }

// ConcurrencySafe reports whether multiple invocations can run in parallel.
func (b *Base) ConcurrencySafe() bool { return b.IsConcurrent }

// Exclusive reports whether the tool must run alone in a turn.
func (b *Base) Exclusive() bool { return b.IsExclusive }

// Registry stores Tools keyed by name.
type Registry struct {
	mu    sync.RWMutex
	items map[string]Tool
}

// NewRegistry creates an empty registry.
func NewRegistry() *Registry {
	return &Registry{items: make(map[string]Tool)}
}

// Register adds or replaces a tool.
func (r *Registry) Register(t Tool) {
	r.mu.Lock()
	r.items[t.Name()] = t
	r.mu.Unlock()
}

// Unregister removes a tool by name.
func (r *Registry) Unregister(name string) {
	r.mu.Lock()
	delete(r.items, name)
	r.mu.Unlock()
}

// Get returns the tool or nil.
func (r *Registry) Get(name string) Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.items[name]
}

// Names lists all registered names (sorted with built-ins before mcp_*).
func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	builtins := make([]string, 0)
	mcps := make([]string, 0)
	for name := range r.items {
		if strings.HasPrefix(name, "mcp_") {
			mcps = append(mcps, name)
		} else {
			builtins = append(builtins, name)
		}
	}
	sort.Strings(builtins)
	sort.Strings(mcps)
	return append(builtins, mcps...)
}

// Definitions returns OpenAI-style function definitions in stable order.
func (r *Registry) Definitions() []provider.ToolDefinition {
	names := r.Names()
	defs := make([]provider.ToolDefinition, 0, len(names))
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, n := range names {
		t := r.items[n]
		defs = append(defs, provider.ToolDefinition{
			Type: "function",
			Function: provider.Function{
				Name:        t.Name(),
				Description: t.Description(),
				Parameters:  t.Parameters().AsMap(),
			},
		})
	}
	return defs
}

// Execute validates args against the schema and runs the tool. The returned
// string is what the loop records as the tool message content.
func (r *Registry) Execute(ctx context.Context, name string, args map[string]any) (string, error) {
	t := r.Get(name)
	if t == nil {
		return "", fmt.Errorf("unknown tool %q", name)
	}
	if err := validateAgainst(t.Parameters(), args); err != nil {
		return "", fmt.Errorf("invalid arguments for %s: %w", name, err)
	}
	return t.Execute(ctx, args)
}

// validateAgainst is a permissive presence/type validator. It enforces:
//   - required keys present
//   - scalars parseable to the declared type when obvious
//
// Deep schema validation is delegated to the tool's own logic.
func validateAgainst(s *Schema, args map[string]any) error {
	if s == nil {
		return nil
	}
	for _, req := range s.Required {
		if _, ok := args[req]; !ok {
			return fmt.Errorf("missing required %q", req)
		}
	}
	for key, prop := range s.Properties {
		val, ok := args[key]
		if !ok {
			continue
		}
		if err := checkType(prop, val); err != nil {
			return fmt.Errorf("field %q: %w", key, err)
		}
	}
	return nil
}

func checkType(s *Schema, v any) error {
	if s == nil {
		return nil
	}
	switch s.Type {
	case "string":
		if _, ok := v.(string); !ok {
			return errors.New("expected string")
		}
	case "number":
		switch v.(type) {
		case float64, float32, int, int32, int64:
			return nil
		}
		return errors.New("expected number")
	case "integer":
		switch t := v.(type) {
		case int, int32, int64:
			return nil
		case float64:
			if float64(int(t)) != t {
				return errors.New("expected integer")
			}
		default:
			return errors.New("expected integer")
		}
	case "boolean":
		if _, ok := v.(bool); !ok {
			return errors.New("expected boolean")
		}
	case "array":
		if _, ok := v.([]any); !ok {
			return errors.New("expected array")
		}
	case "object":
		if _, ok := v.(map[string]any); !ok {
			return errors.New("expected object")
		}
	}
	return nil
}

// ArgString returns the string at key or fallback.
func ArgString(args map[string]any, key, fallback string) string {
	if v, ok := args[key].(string); ok {
		return v
	}
	return fallback
}

// ArgInt returns the int at key or fallback.
func ArgInt(args map[string]any, key string, fallback int) int {
	switch v := args[key].(type) {
	case int:
		return v
	case float64:
		return int(v)
	}
	return fallback
}

// ArgBool returns the bool at key or fallback.
func ArgBool(args map[string]any, key string, fallback bool) bool {
	if v, ok := args[key].(bool); ok {
		return v
	}
	return fallback
}

// ArgStringSlice returns a []string (with best-effort coercion).
func ArgStringSlice(args map[string]any, key string) []string {
	v, ok := args[key]
	if !ok {
		return nil
	}
	switch t := v.(type) {
	case []string:
		return t
	case []any:
		out := make([]string, 0, len(t))
		for _, e := range t {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}
