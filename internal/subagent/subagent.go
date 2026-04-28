// Package subagent implements SubagentManager: fire-and-forget agent runs
// used by the `spawn` tool. Each subagent runs in its own goroutine and
// publishes its final reply back to the bus as an inbound "system" message
// so the main agent can observe it. Mirrors Python agent/subagent.py.
package subagent

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kuiba3/nanobot-go/internal/bus"
	"github.com/kuiba3/nanobot-go/internal/provider"
	"github.com/kuiba3/nanobot-go/internal/runner"
	"github.com/kuiba3/nanobot-go/internal/tools"
)

// Manager spawns background agent runs.
type Manager struct {
	Provider  provider.Provider
	Bus       *bus.Bus
	Workspace string
	Model     string

	runner *runner.Runner
	nextID int64

	mu      sync.Mutex
	running map[string]*task
}

// New creates a Manager.
func New(p provider.Provider, b *bus.Bus, workspace, model string) *Manager {
	return &Manager{
		Provider:  p,
		Bus:       b,
		Workspace: workspace,
		Model:     model,
		runner:    runner.New(p),
		running:   make(map[string]*task),
	}
}

type task struct {
	id         string
	label      string
	startedAt  time.Time
	cancel     context.CancelFunc
	sessionKey string
}

// Spawn starts a subagent task with the given prompt.
func (m *Manager) Spawn(ctx context.Context, parentSessionKey, label, prompt string) (string, error) {
	if prompt == "" {
		return "", fmt.Errorf("prompt required")
	}
	id := fmt.Sprintf("sub_%d", atomic.AddInt64(&m.nextID, 1))
	tctx, cancel := context.WithCancel(ctx)
	t := &task{id: id, label: label, startedAt: time.Now(), cancel: cancel, sessionKey: parentSessionKey}
	m.mu.Lock()
	m.running[id] = t
	m.mu.Unlock()
	go m.run(tctx, t, prompt)
	return id, nil
}

// Cancel terminates a running subagent.
func (m *Manager) Cancel(id string) {
	m.mu.Lock()
	t := m.running[id]
	m.mu.Unlock()
	if t != nil {
		t.cancel()
	}
}

// Running reports how many subagents are active.
func (m *Manager) Running() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.running)
}

func (m *Manager) run(ctx context.Context, t *task, prompt string) {
	defer func() {
		m.mu.Lock()
		delete(m.running, t.id)
		m.mu.Unlock()
	}()
	reg := tools.NewRegistry()
	result, err := m.runner.Run(ctx, runner.Spec{
		InitialMessages: []provider.Message{
			{Role: "system", Content: jsonString("You are a subagent. Be focused and concise.")},
			{Role: "user", Content: jsonString(prompt)},
		},
		Registry:       reg,
		Model:          m.Model,
		MaxIterations:  15,
		RetryPolicy:    provider.Defaults(),
		ConcurrentTools: false,
	})
	summary := ""
	if err != nil {
		summary = fmt.Sprintf("[subagent %s error] %s", t.label, err)
	} else if result != nil {
		summary = fmt.Sprintf("[subagent %s finished] %s", t.label, result.FinalContent)
	}
	if m.Bus != nil && summary != "" {
		_ = m.Bus.PublishInbound(context.Background(), bus.InboundMessage{
			Channel:            "system",
			SenderID:           "subagent",
			ChatID:             t.sessionKey,
			Content:            summary,
			Timestamp:          time.Now().UTC(),
			SessionKeyOverride: t.sessionKey,
		})
	}
}

func jsonString(s string) json.RawMessage {
	b, _ := json.Marshal(s)
	return b
}
