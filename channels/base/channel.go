// Package base defines the Channel interface and a Manager that pumps
// outbound messages from the bus into registered channels.
package base

import (
	"context"
	"log"
	"sync"

	"github.com/kuiba3/nanobot-go/internal/bus"
)

// Channel is the contract every ingress/egress integration implements.
type Channel interface {
	Name() string
	Start(ctx context.Context) error
	Stop(ctx context.Context) error
	Send(ctx context.Context, m bus.OutboundMessage) error
	// SendDelta is optional; channels that do not stream should return nil.
	SendDelta(ctx context.Context, m bus.OutboundMessage) error
	SupportsStreaming() bool
}

// Manager owns the set of channels and forwards bus outbound messages to them.
type Manager struct {
	bus      *bus.Bus
	channels map[string]Channel

	mu sync.RWMutex
}

// NewManager constructs a Manager.
func NewManager(b *bus.Bus) *Manager {
	return &Manager{bus: b, channels: make(map[string]Channel)}
}

// Register adds a channel.
func (m *Manager) Register(c Channel) {
	m.mu.Lock()
	m.channels[c.Name()] = c
	m.mu.Unlock()
}

// Channels returns the list of registered names.
func (m *Manager) Channels() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]string, 0, len(m.channels))
	for k := range m.channels {
		out = append(out, k)
	}
	return out
}

// StartAll starts every registered channel and begins forwarding outbound msgs.
func (m *Manager) StartAll(ctx context.Context) error {
	m.mu.RLock()
	chans := make([]Channel, 0, len(m.channels))
	for _, c := range m.channels {
		chans = append(chans, c)
	}
	m.mu.RUnlock()
	for _, c := range chans {
		if err := c.Start(ctx); err != nil {
			return err
		}
	}
	go m.pump(ctx)
	return nil
}

// StopAll stops every channel.
func (m *Manager) StopAll(ctx context.Context) {
	m.mu.RLock()
	chans := make([]Channel, 0, len(m.channels))
	for _, c := range m.channels {
		chans = append(chans, c)
	}
	m.mu.RUnlock()
	for _, c := range chans {
		_ = c.Stop(ctx)
	}
}

func (m *Manager) pump(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case msg := <-m.bus.ConsumeOutbound():
			m.dispatch(ctx, msg)
		}
	}
}

func (m *Manager) dispatch(ctx context.Context, msg bus.OutboundMessage) {
	m.mu.RLock()
	c, ok := m.channels[msg.Channel]
	m.mu.RUnlock()
	if !ok {
		return
	}
	if isStreamPart(msg) && c.SupportsStreaming() {
		if err := c.SendDelta(ctx, msg); err != nil {
			log.Printf("channel %s delta: %v", c.Name(), err)
		}
		return
	}
	if err := c.Send(ctx, msg); err != nil {
		log.Printf("channel %s send: %v", c.Name(), err)
	}
}

func isStreamPart(msg bus.OutboundMessage) bool {
	if msg.Metadata == nil {
		return false
	}
	if v, ok := msg.Metadata[bus.MetaStreamDelta].(bool); ok && v {
		return true
	}
	if v, ok := msg.Metadata[bus.MetaStreamEnd].(bool); ok && v {
		return true
	}
	return false
}
