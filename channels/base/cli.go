package base

import (
	"context"

	"github.com/kuiba3/nanobot-go/internal/bus"
)

// CLIChannel is a simple in-process channel used by the `agent` interactive
// command. It forwards messages to user-provided callbacks.
type CLIChannel struct {
	OnMessage     func(bus.OutboundMessage)
	OnStreamDelta func(bus.OutboundMessage)
	OnStreamEnd   func(bus.OutboundMessage)
}

// Name returns "cli".
func (c *CLIChannel) Name() string { return "cli" }

// SupportsStreaming returns true.
func (c *CLIChannel) SupportsStreaming() bool { return true }

// Start is a no-op.
func (c *CLIChannel) Start(ctx context.Context) error { return nil }

// Stop is a no-op.
func (c *CLIChannel) Stop(ctx context.Context) error { return nil }

// Send forwards a full-content message.
func (c *CLIChannel) Send(ctx context.Context, m bus.OutboundMessage) error {
	if c.OnMessage != nil {
		c.OnMessage(m)
	}
	return nil
}

// SendDelta forwards a streaming event.
func (c *CLIChannel) SendDelta(ctx context.Context, m bus.OutboundMessage) error {
	if v, _ := m.Metadata[bus.MetaStreamEnd].(bool); v {
		if c.OnStreamEnd != nil {
			c.OnStreamEnd(m)
		}
		return nil
	}
	if c.OnStreamDelta != nil {
		c.OnStreamDelta(m)
	}
	return nil
}
