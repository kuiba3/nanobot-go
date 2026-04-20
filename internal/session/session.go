package session

import (
	"time"
)

// Session captures the on-disk conversation state for a single session key.
type Session struct {
	Key              string    `json:"key"`
	Messages         []Message `json:"messages"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
	LastConsolidated int       `json:"last_consolidated"`
	Metadata         map[string]any `json:"metadata"`
}

// AddMessage appends a message and bumps UpdatedAt.
func (s *Session) AddMessage(m Message) {
	if m.Timestamp.IsZero() {
		m.Timestamp = time.Now().UTC()
	}
	s.Messages = append(s.Messages, m)
	s.UpdatedAt = time.Now().UTC()
}

// Clear wipes messages but keeps the key and timestamps reset.
func (s *Session) Clear() {
	s.Messages = nil
	s.LastConsolidated = 0
	s.UpdatedAt = time.Now().UTC()
}

// History returns the unconsolidated tail, limited to maxMessages if > 0.
func (s *Session) History(maxMessages int) []Message {
	start := s.LastConsolidated
	if start < 0 || start > len(s.Messages) {
		start = 0
	}
	tail := s.Messages[start:]
	if maxMessages > 0 && len(tail) > maxMessages {
		tail = tail[len(tail)-maxMessages:]
	}
	return tail
}

// RetainRecentLegalSuffix keeps the last n messages that form a legal chain
// (assistant/tool calls matched), dropping earlier ones. Used by AutoCompact.
// For MVP we keep the simple "tail n" heuristic but respect tool_call pairing
// by advancing the start to the next user boundary.
func (s *Session) RetainRecentLegalSuffix(n int) {
	if n <= 0 || len(s.Messages) == 0 {
		return
	}
	start := len(s.Messages) - n
	if start < 0 {
		start = 0
	}
	// walk forward to the nearest user-role boundary to avoid a dangling
	// assistant-with-tool-calls head.
	for start < len(s.Messages) && s.Messages[start].Role != RoleUser && s.Messages[start].Role != RoleSystem {
		start++
	}
	if start >= len(s.Messages) {
		return
	}
	s.Messages = append([]Message{}, s.Messages[start:]...)
	s.LastConsolidated = 0
	s.UpdatedAt = time.Now().UTC()
}
