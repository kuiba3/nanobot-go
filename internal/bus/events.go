// Package bus defines the in-process message bus events and queue used to
// decouple channels from the agent loop. Mirrors Python nanobot/bus.
package bus

import "time"

// Metadata keys used on messages to drive streaming / progress behavior.
// Must remain stable because channels rely on them.
const (
	MetaWantsStream = "_wants_stream"
	MetaStreamDelta = "_stream_delta"
	MetaStreamID    = "_stream_id"
	MetaStreamEnd   = "_stream_end"
	MetaResuming    = "_resuming"
	MetaStreamed    = "_streamed"
	MetaProgress    = "_progress"
	MetaToolHint    = "_tool_hint"
)

// InboundMessage is an incoming user message from any channel.
type InboundMessage struct {
	Channel             string
	SenderID            string
	ChatID              string
	Content             string
	Timestamp           time.Time
	Media               []MediaItem
	Metadata            map[string]any
	SessionKeyOverride  string
}

// MediaItem is a lightweight descriptor for attached media (images, audio, docs).
type MediaItem struct {
	Kind string // e.g. "image", "audio", "file"
	Path string // local filesystem path or URL
	Name string
}

// SessionKey derives the persistent session key for this message.
// Channels that unify sessions across chats may override via SessionKeyOverride.
func (m *InboundMessage) SessionKey() string {
	if m.SessionKeyOverride != "" {
		return m.SessionKeyOverride
	}
	return m.Channel + ":" + m.ChatID
}

// OutboundMessage is a reply produced by the agent loop for a channel to deliver.
type OutboundMessage struct {
	Channel   string
	ChatID    string
	Content   string
	ReplyTo   string
	Media     []MediaItem
	Metadata  map[string]any
	Timestamp time.Time
}
