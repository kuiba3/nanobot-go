package bus

import "context"

// Bus is the in-process two-lane message queue between channels and the agent
// loop. Inbound flows channel -> loop, outbound flows loop -> channels.
type Bus struct {
	in  chan InboundMessage
	out chan OutboundMessage
}

// New creates a bus. bufferSize is the channel capacity for both lanes.
// Passing 0 creates unbuffered channels (for tests); production callers
// typically use 256.
func New(bufferSize int) *Bus {
	if bufferSize < 0 {
		bufferSize = 0
	}
	return &Bus{
		in:  make(chan InboundMessage, bufferSize),
		out: make(chan OutboundMessage, bufferSize),
	}
}

// PublishInbound pushes an inbound message. Blocks if the channel is full,
// respects ctx cancellation.
func (b *Bus) PublishInbound(ctx context.Context, m InboundMessage) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case b.in <- m:
		return nil
	}
}

// PublishOutbound pushes an outbound message.
func (b *Bus) PublishOutbound(ctx context.Context, m OutboundMessage) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case b.out <- m:
		return nil
	}
}

// ConsumeInbound returns the raw receive channel; prefer Next* helpers below.
func (b *Bus) ConsumeInbound() <-chan InboundMessage { return b.in }

// ConsumeOutbound returns the raw outbound receive channel.
func (b *Bus) ConsumeOutbound() <-chan OutboundMessage { return b.out }

// InboundQueueDepth reports the current inbound backlog (best-effort).
func (b *Bus) InboundQueueDepth() int { return len(b.in) }

// OutboundQueueDepth reports the current outbound backlog (best-effort).
func (b *Bus) OutboundQueueDepth() int { return len(b.out) }

// Close releases the underlying channels. Callers must not publish afterwards.
func (b *Bus) Close() {
	close(b.in)
	close(b.out)
}
