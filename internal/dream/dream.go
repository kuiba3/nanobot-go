// Package dream implements a minimal two-phase Dream process. Phase 1 asks the
// LLM to summarize unprocessed history; Phase 2 is a stubbed tool-loop slot
// that callers can fill in by supplying a runner.Spec via OnTools. This is the
// MVP version; the richer Python behavior (targeted MEMORY/SOUL/USER edits via
// tool use) will be added in a follow-up phase.
package dream

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/kuiba3/nanobot-go/internal/memory"
	"github.com/kuiba3/nanobot-go/internal/provider"
	"github.com/kuiba3/nanobot-go/internal/runner"
)

// Options configures Dream.
type Options struct {
	Memory   *memory.Store
	Runner   *runner.Runner
	Model    string
	MaxBatch int
}

// Dream is the runner.
type Dream struct {
	opts Options
}

// New builds a Dream.
func New(opts Options) *Dream {
	if opts.MaxBatch <= 0 {
		opts.MaxBatch = 60
	}
	return &Dream{opts: opts}
}

// Run executes a single Dream cycle.
func (d *Dream) Run(ctx context.Context) error {
	if d.opts.Memory == nil || d.opts.Runner == nil {
		return errors.New("dream: memory/runner required")
	}
	cursor := d.opts.Memory.LastDreamCursor()
	entries, err := d.opts.Memory.ReadUnprocessedHistory(cursor)
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		return nil
	}
	batch := entries
	if len(batch) > d.opts.MaxBatch {
		batch = batch[:d.opts.MaxBatch]
	}

	var b strings.Builder
	for _, e := range batch {
		fmt.Fprintf(&b, "- [%s] %s\n", e.Timestamp, e.Content)
	}
	prompt := "Summarize the following new entries into 3-5 bullet points, " +
		"capturing lasting facts, open loops, and user preferences:\n\n" + b.String()

	body, _ := json.Marshal(prompt)
	cctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	resp, err := d.opts.Runner.Provider.Chat(cctx, provider.ChatRequest{
		Model: d.opts.Model,
		Messages: []provider.Message{
			{Role: "system", Content: json.RawMessage(`"You are a memory consolidator. Be concise."`)},
			{Role: "user", Content: body},
		},
	})
	if err != nil || resp == nil || strings.TrimSpace(resp.Content) == "" {
		// still advance cursor to avoid a stuck state
		return d.opts.Memory.SetDreamCursor(batch[len(batch)-1].Cursor)
	}
	if _, err := d.opts.Memory.AppendHistory("Dream summary: " + resp.Content); err != nil {
		return err
	}
	return d.opts.Memory.SetDreamCursor(batch[len(batch)-1].Cursor)
}
