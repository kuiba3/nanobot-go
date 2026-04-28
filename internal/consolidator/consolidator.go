// Package consolidator implements token-budget archival of session history.
// For MVP we offer a simple count-based policy and delegate the actual LLM
// summarization to the caller. This keeps the P4 scope manageable; the
// richer token-aware version can be added in P7.
package consolidator

import (
	"context"
	"strings"

	"github.com/kuiba3/nanobot-go/internal/memory"
	"github.com/kuiba3/nanobot-go/internal/session"
)

// Consolidator archives slices of a session into memory.history.jsonl.
type Consolidator struct {
	Store *memory.Store
	// MaxBatchSize is how many messages to pull into a single archive call.
	MaxBatchSize int
}

// New creates a Consolidator.
func New(store *memory.Store) *Consolidator {
	return &Consolidator{Store: store, MaxBatchSize: 60}
}

// MaybeArchive runs when the unconsolidated tail exceeds `threshold` messages.
// It appends a textual summary of the archived slice to history.jsonl and
// advances session.LastConsolidated.
//
// The `summarize` callback is provided by the caller (typically a function
// that calls the LLM). If nil, we fall back to a cheap textual summary.
func (c *Consolidator) MaybeArchive(
	ctx context.Context,
	s *session.Session,
	threshold int,
	summarize func(ctx context.Context, msgs []session.Message) (string, error),
) error {
	if threshold <= 0 {
		threshold = 120
	}
	tail := s.History(0)
	if len(tail) <= threshold {
		return nil
	}
	batch := tail
	if len(batch) > c.MaxBatchSize {
		batch = batch[:c.MaxBatchSize]
	}
	var summary string
	if summarize != nil {
		sum, err := summarize(ctx, batch)
		if err != nil || sum == "" {
			summary = fallbackSummary(batch)
		} else {
			summary = sum
		}
	} else {
		summary = fallbackSummary(batch)
	}
	if _, err := c.Store.AppendHistory(summary); err != nil {
		return err
	}
	s.LastConsolidated += len(batch)
	return nil
}

func fallbackSummary(msgs []session.Message) string {
	var b strings.Builder
	b.WriteString("Archived ")
	b.WriteString(intToStr(len(msgs)))
	b.WriteString(" messages. Highlights:\n")
	for _, m := range msgs {
		line := strings.ReplaceAll(m.TextOf(), "\n", " ")
		if len(line) > 160 {
			line = line[:160] + "..."
		}
		b.WriteString("- ")
		b.WriteString(m.Role)
		b.WriteString(": ")
		b.WriteString(line)
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

func intToStr(n int) string {
	if n == 0 {
		return "0"
	}
	digits := "0123456789"
	sign := ""
	if n < 0 {
		sign = "-"
		n = -n
	}
	buf := make([]byte, 0, 8)
	for n > 0 {
		buf = append([]byte{digits[n%10]}, buf...)
		n /= 10
	}
	return sign + string(buf)
}
