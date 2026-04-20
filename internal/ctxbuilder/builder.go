// Package ctxbuilder implements the ContextBuilder that assembles system
// prompt and user-turn messages for the agent loop. Mirrors Python
// agent/context.py. Named ctxbuilder to avoid colliding with stdlib "context".
package ctxbuilder

import (
	"encoding/json"
	"fmt"
	"runtime"
	"strings"
	"time"

	"github.com/hkuds/nanobot-go/internal/memory"
	"github.com/hkuds/nanobot-go/internal/provider"
	"github.com/hkuds/nanobot-go/internal/session"
	"github.com/hkuds/nanobot-go/internal/skills"
	"github.com/hkuds/nanobot-go/internal/templates"
)

// Builder produces the system prompt + ordered messages for a request.
type Builder struct {
	Workspace string
	Timezone  string
	Memory    *memory.Store
	Skills    *skills.Loader
	// MaxRecentHistory caps the "Recent History" block injected into the
	// system prompt; default 50 to match the Python behavior.
	MaxRecentHistory int
}

// New returns a Builder with sensible defaults.
func New(workspace, tz string, mem *memory.Store, sl *skills.Loader) *Builder {
	return &Builder{
		Workspace:        workspace,
		Timezone:         tz,
		Memory:           mem,
		Skills:           sl,
		MaxRecentHistory: 50,
	}
}

// BuildSystemPrompt assembles the system prompt with identity / bootstrap
// files / memory / active skills / skills index / recent history.
func (b *Builder) BuildSystemPrompt(channel string) (string, error) {
	var parts []string

	identity, err := templates.Render("agent/identity.md", map[string]any{
		"WorkspacePath":  b.Workspace,
		"Runtime":        fmt.Sprintf("time=%s go=%s", now(b.Timezone).Format(time.RFC3339), runtime.Version()),
		"PlatformPolicy": b.renderPlatformPolicy(),
		"Channel":        orDefault(channel, "cli"),
	})
	if err != nil {
		return "", err
	}
	parts = append(parts, identity)

	if mem := b.Memory.MemoryContext(); mem != "" {
		parts = append(parts, "# Memory\n\n"+mem)
	}

	alwaysNames, err := b.Skills.AlwaysSkills()
	if err == nil && len(alwaysNames) > 0 {
		body, err := b.Skills.LoadForContext(alwaysNames)
		if err == nil && body != "" {
			parts = append(parts, "# Active Skills\n\n"+body)
		}
	}
	exclude := make(map[string]struct{}, len(alwaysNames))
	for _, n := range alwaysNames {
		exclude[n] = struct{}{}
	}
	if summary, err := b.Skills.BuildSummary(exclude); err == nil && summary != "" {
		section, err := templates.Render("agent/skills_section.md", map[string]any{"SkillsSummary": summary})
		if err == nil {
			parts = append(parts, section)
		}
	}

	if entries, err := b.Memory.ReadUnprocessedHistory(b.Memory.LastDreamCursor()); err == nil && len(entries) > 0 {
		if n := len(entries); n > b.MaxRecentHistory {
			entries = entries[n-b.MaxRecentHistory:]
		}
		var rh strings.Builder
		rh.WriteString("# Recent History\n\n")
		for _, e := range entries {
			fmt.Fprintf(&rh, "- [%s] %s\n", e.Timestamp, e.Content)
		}
		parts = append(parts, strings.TrimRight(rh.String(), "\n"))
	}

	return strings.Join(parts, "\n\n---\n\n"), nil
}

func (b *Builder) renderPlatformPolicy() string {
	s, err := templates.Render("agent/platform_policy.md", map[string]any{"System": runtime.GOOS})
	if err != nil {
		return "Standard workspace policy applies."
	}
	return s
}

// BuildMessages composes the full messages list: system prompt, session
// history, and the current user turn (merged with runtime context).
func (b *Builder) BuildMessages(
	channel, chatID string,
	history []session.Message,
	currentUser string,
	sessionSummary string,
) ([]provider.Message, error) {
	sysPrompt, err := b.BuildSystemPrompt(channel)
	if err != nil {
		return nil, err
	}

	msgs := make([]provider.Message, 0, len(history)+2)
	msgs = append(msgs, provider.Message{Role: "system", Content: toJSON(sysPrompt)})
	for _, h := range history {
		msgs = append(msgs, convertSessionMessage(h))
	}
	runtimeBlock := b.buildRuntimeBlock(channel, chatID, sessionSummary)
	userContent := runtimeBlock
	if currentUser != "" {
		userContent = runtimeBlock + "\n\n" + currentUser
	}
	msgs = append(msgs, provider.Message{Role: "user", Content: toJSON(userContent)})
	return msgs, nil
}

func (b *Builder) buildRuntimeBlock(channel, chatID, summary string) string {
	var lines []string
	lines = append(lines, "<!-- runtime:start -->")
	lines = append(lines, "Current Time: "+now(b.Timezone).Format(time.RFC3339))
	if channel != "" && chatID != "" {
		lines = append(lines, "Channel: "+channel, "Chat ID: "+chatID)
	}
	if summary != "" {
		lines = append(lines, "", "[Resumed Session]", summary)
	}
	lines = append(lines, "<!-- runtime:end -->")
	return strings.Join(lines, "\n")
}

// AppendToolResult appends a role=tool message to a message list, ready for
// the next provider call.
func AppendToolResult(msgs []provider.Message, callID, name, content string) []provider.Message {
	return append(msgs, provider.Message{
		Role:       "tool",
		Name:       name,
		ToolCallID: callID,
		Content:    toJSON(content),
	})
}

// AppendAssistant appends an assistant message (optionally with tool_calls) to
// the message list.
func AppendAssistant(msgs []provider.Message, content string, tc []provider.ToolCall, thinking []json.RawMessage) []provider.Message {
	return append(msgs, provider.Message{
		Role:           "assistant",
		Content:        toJSON(content),
		ToolCalls:      tc,
		ThinkingBlocks: thinking,
	})
}

func convertSessionMessage(m session.Message) provider.Message {
	tc := make([]provider.ToolCall, 0, len(m.ToolCalls))
	for _, c := range m.ToolCalls {
		tc = append(tc, provider.ToolCall{
			ID:   c.ID,
			Type: firstNonEmpty(c.Type, "function"),
			Function: provider.ToolCallFunction{
				Name:      c.Function.Name,
				Arguments: c.Function.Arguments,
			},
		})
	}
	return provider.Message{
		Role:             m.Role,
		Content:          m.Content,
		Name:             m.Name,
		ToolCallID:       m.ToolCallID,
		ToolCalls:        tc,
		ReasoningContent: m.ReasoningContent,
		ThinkingBlocks:   m.ThinkingBlocks,
		ExtraContent:     m.ExtraContent,
	}
}

func toJSON(s string) json.RawMessage {
	b, _ := json.Marshal(s)
	return b
}

func orDefault(s, d string) string {
	if s != "" {
		return s
	}
	return d
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func now(tz string) time.Time {
	if tz == "" {
		return time.Now()
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return time.Now()
	}
	return time.Now().In(loc)
}
