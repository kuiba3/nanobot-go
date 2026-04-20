// Package session implements the JSONL-backed conversation store.
package session

import (
	"encoding/json"
	"time"
)

// Role constants match OpenAI Chat Completions vocabulary.
const (
	RoleSystem    = "system"
	RoleUser      = "user"
	RoleAssistant = "assistant"
	RoleTool      = "tool"
)

// ToolCall mirrors OpenAI's tool_call shape for assistant messages.
type ToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"` // always "function"
	Function ToolCallFunction `json:"function"`
}

// ToolCallFunction is the nested function payload.
type ToolCallFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"` // JSON-encoded map
}

// Message is a single transcript entry. Content may be a string or a list of
// multimodal parts; we keep it as json.RawMessage so upstream code can pass
// through provider-specific structures.
type Message struct {
	Role             string            `json:"role"`
	Content          json.RawMessage   `json:"content,omitempty"`
	Name             string            `json:"name,omitempty"`
	ToolCalls        []ToolCall        `json:"tool_calls,omitempty"`
	ToolCallID       string            `json:"tool_call_id,omitempty"`
	ReasoningContent string            `json:"reasoning_content,omitempty"`
	ThinkingBlocks   []json.RawMessage `json:"thinking_blocks,omitempty"`
	Timestamp        time.Time         `json:"timestamp,omitempty"`
	ExtraContent     map[string]any    `json:"extra_content,omitempty"`
}

// StringContent is a convenience constructor for plain text content.
func StringContent(s string) json.RawMessage {
	b, _ := json.Marshal(s)
	return b
}

// TextOf best-effort decodes the Content field into a Go string if it is a
// plain JSON string. Returns "" for non-string content.
func (m *Message) TextOf() string {
	if len(m.Content) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(m.Content, &s); err == nil {
		return s
	}
	return ""
}

// SetText assigns a plain string to Content.
func (m *Message) SetText(s string) { m.Content = StringContent(s) }
