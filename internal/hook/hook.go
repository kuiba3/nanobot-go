// Package hook defines the agent lifecycle hooks.
package hook

import (
	"context"
	"strings"

	"github.com/hkuds/nanobot-go/internal/provider"
)

// Context is the per-iteration context passed to hooks. Mirrors Python
// AgentHookContext but exposes only what hooks actually read/mutate.
type Context struct {
	Iteration    int
	Messages     []provider.Message
	Response     *provider.LLMResponse
	Usage        provider.Usage
	ToolCalls    []provider.ToolCallRequest
	ToolResults  []ToolResult
	FinalContent string
	StopReason   string
	Error        error
	Extra        map[string]any
}

// ToolResult is a structured record of a tool invocation outcome.
type ToolResult struct {
	CallID   string
	Name     string
	Output   string
	Err      error
	Duration int64 // ms
}

// Hook is the interface implemented by listeners of the agent's lifecycle.
// All async methods take a context for cancellation and may be no-ops.
type Hook interface {
	WantsStreaming() bool
	BeforeIteration(ctx context.Context, hctx *Context) error
	OnStream(ctx context.Context, hctx *Context, delta string) error
	OnStreamEnd(ctx context.Context, hctx *Context, resuming bool) error
	BeforeExecuteTools(ctx context.Context, hctx *Context) error
	AfterIteration(ctx context.Context, hctx *Context) error
	FinalizeContent(s string) string
}

// Base is a zero-method hook implementing every callback as a no-op. Embed
// this in custom hooks to avoid stub methods.
type Base struct{}

// WantsStreaming defaults to false.
func (Base) WantsStreaming() bool { return false }

// BeforeIteration is a no-op.
func (Base) BeforeIteration(ctx context.Context, hctx *Context) error { return nil }

// OnStream is a no-op.
func (Base) OnStream(ctx context.Context, hctx *Context, delta string) error { return nil }

// OnStreamEnd is a no-op.
func (Base) OnStreamEnd(ctx context.Context, hctx *Context, resuming bool) error { return nil }

// BeforeExecuteTools is a no-op.
func (Base) BeforeExecuteTools(ctx context.Context, hctx *Context) error { return nil }

// AfterIteration is a no-op.
func (Base) AfterIteration(ctx context.Context, hctx *Context) error { return nil }

// FinalizeContent returns the input unchanged.
func (Base) FinalizeContent(s string) string { return s }

// Composite fans out each call to a list of hooks. WantsStreaming returns
// true if *any* child wants streaming. FinalizeContent chains left-to-right.
type Composite struct {
	Hooks []Hook
}

// NewComposite builds a Composite.
func NewComposite(hs ...Hook) *Composite { return &Composite{Hooks: hs} }

// WantsStreaming returns true if any child wants streaming.
func (c *Composite) WantsStreaming() bool {
	for _, h := range c.Hooks {
		if h.WantsStreaming() {
			return true
		}
	}
	return false
}

// BeforeIteration fans out and returns the first error (continues others).
func (c *Composite) BeforeIteration(ctx context.Context, hctx *Context) error {
	var first error
	for _, h := range c.Hooks {
		if err := h.BeforeIteration(ctx, hctx); err != nil && first == nil {
			first = err
		}
	}
	return first
}

// OnStream fans out.
func (c *Composite) OnStream(ctx context.Context, hctx *Context, delta string) error {
	for _, h := range c.Hooks {
		if err := h.OnStream(ctx, hctx, delta); err != nil {
			return err
		}
	}
	return nil
}

// OnStreamEnd fans out.
func (c *Composite) OnStreamEnd(ctx context.Context, hctx *Context, resuming bool) error {
	for _, h := range c.Hooks {
		if err := h.OnStreamEnd(ctx, hctx, resuming); err != nil {
			return err
		}
	}
	return nil
}

// BeforeExecuteTools fans out.
func (c *Composite) BeforeExecuteTools(ctx context.Context, hctx *Context) error {
	for _, h := range c.Hooks {
		if err := h.BeforeExecuteTools(ctx, hctx); err != nil {
			return err
		}
	}
	return nil
}

// AfterIteration fans out.
func (c *Composite) AfterIteration(ctx context.Context, hctx *Context) error {
	for _, h := range c.Hooks {
		if err := h.AfterIteration(ctx, hctx); err != nil {
			return err
		}
	}
	return nil
}

// FinalizeContent chains through each hook.
func (c *Composite) FinalizeContent(s string) string {
	for _, h := range c.Hooks {
		s = h.FinalizeContent(s)
	}
	return s
}

// StripThink removes <think>...</think> blocks from content. Mirrors Python
// nanobot.utils.helpers.strip_think.
func StripThink(s string) string {
	for {
		start := strings.Index(s, "<think>")
		if start < 0 {
			return s
		}
		end := strings.Index(s[start:], "</think>")
		if end < 0 {
			return s[:start]
		}
		s = s[:start] + s[start+end+len("</think>"):]
	}
}
