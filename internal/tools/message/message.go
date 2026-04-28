// Package message implements the `message` tool which lets the agent send an
// outbound chat message mid-turn (e.g. streaming progress updates or replying
// before the final answer).
package message

import (
	"context"
	"fmt"
	"sync"

	"github.com/kuiba3/nanobot-go/internal/bus"
	"github.com/kuiba3/nanobot-go/internal/tools"
)

// SendFunc is injected by the loop so the tool can emit outbound messages.
type SendFunc func(ctx context.Context, m bus.OutboundMessage) error

// New builds the message tool bound to a SendFunc, a channel, and a chat id.
// The loop rewires these via NewWithAccessor per-turn.
type Tool struct {
	tools.Base
	mu        sync.Mutex
	channel   string
	chatID    string
	send      SendFunc
	lastSent  string
	sentCount int
}

// New builds the tool shell.
func New() *Tool {
	return &Tool{Base: tools.Base{
		ToolName:        "message",
		ToolDescription: "Send an outbound chat message to the current channel before the final reply. Use sparingly to stream progress.",
		Params: &tools.Schema{
			Type: "object",
			Properties: map[string]*tools.Schema{
				"text": {Type: "string", Description: "The message body."},
			},
			Required: []string{"text"},
		},
	}}
}

// Bind rewires the per-turn state.
func (t *Tool) Bind(channel, chatID string, send SendFunc) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.channel = channel
	t.chatID = chatID
	t.send = send
	t.lastSent = ""
	t.sentCount = 0
}

// LastSent returns the most recent text delivered via this tool in the current
// turn. Used by the loop to suppress a duplicate final message.
func (t *Tool) LastSent() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.lastSent
}

// SentCount returns how many messages were delivered in this turn.
func (t *Tool) SentCount() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.sentCount
}

// Execute sends the outbound message.
func (t *Tool) Execute(ctx context.Context, args map[string]any) (string, error) {
	text := tools.ArgString(args, "text", "")
	if text == "" {
		return "", fmt.Errorf("text must be non-empty")
	}
	t.mu.Lock()
	ch := t.channel
	chat := t.chatID
	send := t.send
	t.mu.Unlock()
	if send == nil {
		return "", fmt.Errorf("message tool not wired to bus")
	}
	if err := send(ctx, bus.OutboundMessage{
		Channel:  ch,
		ChatID:   chat,
		Content:  text,
		Metadata: map[string]any{bus.MetaStreamed: false},
	}); err != nil {
		return "", err
	}
	t.mu.Lock()
	t.lastSent = text
	t.sentCount++
	t.mu.Unlock()
	return "sent", nil
}
