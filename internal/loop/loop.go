// Package loop implements AgentLoop — the top-level orchestrator that bridges
// channels (via Bus) and the AgentRunner. Mirrors Python agent/loop.py.
package loop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/hkuds/nanobot-go/internal/autocompact"
	"github.com/hkuds/nanobot-go/internal/bus"
	"github.com/hkuds/nanobot-go/internal/command"
	"github.com/hkuds/nanobot-go/internal/ctxbuilder"
	"github.com/hkuds/nanobot-go/internal/hook"
	"github.com/hkuds/nanobot-go/internal/provider"
	"github.com/hkuds/nanobot-go/internal/runner"
	"github.com/hkuds/nanobot-go/internal/session"
	"github.com/hkuds/nanobot-go/internal/subagent"
	"github.com/hkuds/nanobot-go/internal/tools"
	"github.com/hkuds/nanobot-go/internal/tools/message"
)

// Options bundles configuration for the loop.
type Options struct {
	Bus            *bus.Bus
	Provider       provider.Provider
	Workspace      string
	Model          string
	MaxIterations  int
	MaxToolResultChars int
	Temperature    float64
	MaxTokens      int
	ReasoningEffort string
	Context        *ctxbuilder.Builder
	Sessions       *session.Manager
	Registry       *tools.Registry
	MessageTool    *message.Tool
	Subagents      *subagent.Manager
	AutoCompact    *autocompact.AutoCompact
	Commands       *command.Router
	UnifiedSession bool
	RetryPolicy    provider.RetryPolicy
}

// Loop is the orchestrator.
type Loop struct {
	opts     Options
	runner   *runner.Runner

	mu           sync.Mutex
	sessionLocks map[string]*sync.Mutex
	pending      map[string]chan provider.Message
	active       map[string]struct{}
	stopping     bool
}

// New builds a Loop.
func New(opts Options) *Loop {
	if opts.MaxIterations <= 0 {
		opts.MaxIterations = 30
	}
	if opts.MaxToolResultChars <= 0 {
		opts.MaxToolResultChars = 12000
	}
	if opts.RetryPolicy.MaxAttempts == 0 && len(opts.RetryPolicy.Backoff) == 0 {
		opts.RetryPolicy = provider.Defaults()
	}
	return &Loop{
		opts:         opts,
		runner:       runner.New(opts.Provider),
		sessionLocks: make(map[string]*sync.Mutex),
		pending:      make(map[string]chan provider.Message),
		active:       make(map[string]struct{}),
	}
}

// Run consumes inbound messages until ctx is cancelled.
func (l *Loop) Run(ctx context.Context) error {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case msg := <-l.opts.Bus.ConsumeInbound():
			go l.dispatch(ctx, msg)
		case <-ticker.C:
			if l.opts.AutoCompact != nil && l.opts.AutoCompact.Enabled() {
				l.opts.AutoCompact.CheckExpired(ctx, l.snapshotActive(), func(f func()) { go f() })
			}
		}
	}
}

// ProcessDirect runs a single turn synchronously. Used by API / SDK callers.
func (l *Loop) ProcessDirect(ctx context.Context, msg bus.InboundMessage) (string, error) {
	key := msg.SessionKey()
	if l.opts.UnifiedSession {
		key = "unified:default"
	}
	return l.process(ctx, key, msg)
}

func (l *Loop) dispatch(ctx context.Context, msg bus.InboundMessage) {
	key := msg.SessionKey()
	if l.opts.UnifiedSession {
		key = "unified:default"
	}
	// Priority commands bypass per-session lock.
	if command.IsPriority(msg.Content) && l.opts.Commands != nil {
		_ = l.handlePriority(ctx, key, msg)
		return
	}
	out, err := l.process(ctx, key, msg)
	if err != nil {
		log.Printf("loop: process error: %v", err)
	}
	if out != "" {
		_ = l.opts.Bus.PublishOutbound(ctx, bus.OutboundMessage{
			Channel:  msg.Channel,
			ChatID:   msg.ChatID,
			Content:  out,
			Metadata: map[string]any{bus.MetaStreamed: false},
		})
	}
}

func (l *Loop) handlePriority(ctx context.Context, key string, msg bus.InboundMessage) error {
	reply, err := l.opts.Commands.Dispatch(ctx, command.DispatchEnv{
		SessionKey: key,
		Message:    msg,
		Loop:       &loopHandle{l: l},
	})
	if reply != "" {
		_ = l.opts.Bus.PublishOutbound(ctx, bus.OutboundMessage{
			Channel: msg.Channel,
			ChatID:  msg.ChatID,
			Content: reply,
		})
	}
	return err
}

func (l *Loop) process(ctx context.Context, key string, msg bus.InboundMessage) (string, error) {
	lock := l.lockFor(key)
	lock.Lock()
	defer lock.Unlock()

	l.mu.Lock()
	l.active[key] = struct{}{}
	l.mu.Unlock()
	defer func() {
		l.mu.Lock()
		delete(l.active, key)
		l.mu.Unlock()
	}()

	s, err := l.opts.Sessions.GetOrCreate(key)
	if err != nil {
		return "", err
	}

	s, summary := l.optAutoCompactPrepare(s)

	// Command routing (non-priority).
	if l.opts.Commands != nil && command.Looks(msg.Content) {
		out, _ := l.opts.Commands.Dispatch(ctx, command.DispatchEnv{
			SessionKey: key,
			Message:    msg,
			Loop:       &loopHandle{l: l},
		})
		if out != "" {
			return out, nil
		}
	}

	history := s.History(0)
	msgs, err := l.opts.Context.BuildMessages(msg.Channel, msg.ChatID, history, msg.Content, summary)
	if err != nil {
		return "", err
	}

	s.AddMessage(session.Message{Role: "user", Content: jsonString(msg.Content)})
	_ = l.opts.Sessions.Save(s)

	// Bind the message tool to this turn so it emits via the bus.
	if l.opts.MessageTool != nil {
		l.opts.MessageTool.Bind(msg.Channel, msg.ChatID, func(ctx context.Context, om bus.OutboundMessage) error {
			return l.opts.Bus.PublishOutbound(ctx, om)
		})
	}

	var streamHook hook.Hook = hook.Base{}
	if m, ok := msg.Metadata[bus.MetaWantsStream].(bool); ok && m {
		streamHook = l.newStreamingHook(msg)
	}

	result, err := l.runner.Run(ctx, runner.Spec{
		InitialMessages:    msgs,
		Registry:           l.opts.Registry,
		Model:              l.opts.Model,
		MaxIterations:      l.opts.MaxIterations,
		MaxToolResultChars: l.opts.MaxToolResultChars,
		Temperature:        l.opts.Temperature,
		MaxTokens:          l.opts.MaxTokens,
		ReasoningEffort:    l.opts.ReasoningEffort,
		Hook:               streamHook,
		ConcurrentTools:    true,
		SessionKey:         key,
		Workspace:          l.opts.Workspace,
		RetryPolicy:        l.opts.RetryPolicy,
		InjectionCallback:  l.drainPending(key),
		CheckpointCallback: func(ctx context.Context, phase string, m []provider.Message) {
			// persist checkpoint metadata on session — minimal: just save updated_at
			s.UpdatedAt = time.Now().UTC()
			_ = l.opts.Sessions.Save(s)
		},
	})
	if err != nil {
		return "", err
	}

	// Persist new messages produced by the runner (skip the initial user that
	// was already saved).
	if len(result.Messages) > len(msgs) {
		for _, m := range result.Messages[len(msgs):] {
			s.AddMessage(convertProviderMessage(m))
		}
	} else {
		s.AddMessage(session.Message{Role: "assistant", Content: jsonString(result.FinalContent)})
	}
	_ = l.opts.Sessions.Save(s)

	// If the message tool already delivered content, don't duplicate.
	if l.opts.MessageTool != nil && l.opts.MessageTool.SentCount() > 0 && l.opts.MessageTool.LastSent() == result.FinalContent {
		return "", nil
	}
	return result.FinalContent, nil
}

func (l *Loop) optAutoCompactPrepare(s *session.Session) (*session.Session, string) {
	if l.opts.AutoCompact == nil {
		return s, ""
	}
	return l.opts.AutoCompact.PrepareSession(s)
}

func (l *Loop) newStreamingHook(msg bus.InboundMessage) hook.Hook {
	return &streamHook{
		loop: l,
		msg:  msg,
	}
}

func (l *Loop) drainPending(key string) func(ctx context.Context) []provider.Message {
	return func(ctx context.Context) []provider.Message {
		l.mu.Lock()
		ch, ok := l.pending[key]
		l.mu.Unlock()
		if !ok {
			return nil
		}
		var out []provider.Message
		for {
			select {
			case m := <-ch:
				out = append(out, m)
				if len(out) >= runner.MaxInjectionsPerTurn {
					return out
				}
			default:
				return out
			}
		}
	}
}

func (l *Loop) lockFor(key string) *sync.Mutex {
	l.mu.Lock()
	defer l.mu.Unlock()
	m, ok := l.sessionLocks[key]
	if !ok {
		m = &sync.Mutex{}
		l.sessionLocks[key] = m
	}
	return m
}

func (l *Loop) snapshotActive() map[string]struct{} {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make(map[string]struct{}, len(l.active))
	for k := range l.active {
		out[k] = struct{}{}
	}
	return out
}

// jsonString serializes a Go string to JSON.
func jsonString(s string) json.RawMessage {
	b, _ := json.Marshal(s)
	return b
}

func convertProviderMessage(m provider.Message) session.Message {
	tc := make([]session.ToolCall, 0, len(m.ToolCalls))
	for _, c := range m.ToolCalls {
		tc = append(tc, session.ToolCall{
			ID:       c.ID,
			Type:     firstNonEmpty(c.Type, "function"),
			Function: session.ToolCallFunction{Name: c.Function.Name, Arguments: c.Function.Arguments},
		})
	}
	return session.Message{
		Role:             m.Role,
		Content:          m.Content,
		Name:             m.Name,
		ToolCalls:        tc,
		ToolCallID:       m.ToolCallID,
		ReasoningContent: m.ReasoningContent,
		ThinkingBlocks:   m.ThinkingBlocks,
		ExtraContent:     m.ExtraContent,
		Timestamp:        time.Now().UTC(),
	}
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// --- streaming hook --------------------------------------------------------

type streamHook struct {
	hook.Base
	loop        *Loop
	msg         bus.InboundMessage
	seg         int
	buf         strings.Builder
	streamedAny bool
}

func (s *streamHook) WantsStreaming() bool { return true }

func (s *streamHook) OnStream(ctx context.Context, hctx *hook.Context, delta string) error {
	prevClean := hook.StripThink(s.buf.String())
	s.buf.WriteString(delta)
	newClean := hook.StripThink(s.buf.String())
	if len(newClean) <= len(prevClean) {
		return nil
	}
	inc := newClean[len(prevClean):]
	s.streamedAny = true
	meta := cloneMeta(s.msg.Metadata)
	meta[bus.MetaStreamDelta] = true
	meta[bus.MetaStreamID] = fmt.Sprintf("%s:%d", s.msg.SessionKey(), s.seg)
	return s.loop.opts.Bus.PublishOutbound(ctx, bus.OutboundMessage{
		Channel:  s.msg.Channel,
		ChatID:   s.msg.ChatID,
		Content:  inc,
		Metadata: meta,
	})
}

func (s *streamHook) OnStreamEnd(ctx context.Context, hctx *hook.Context, resuming bool) error {
	if !s.streamedAny {
		return nil
	}
	meta := cloneMeta(s.msg.Metadata)
	meta[bus.MetaStreamEnd] = true
	meta[bus.MetaResuming] = resuming
	meta[bus.MetaStreamID] = fmt.Sprintf("%s:%d", s.msg.SessionKey(), s.seg)
	s.seg++
	if !resuming {
		s.buf.Reset()
	}
	return s.loop.opts.Bus.PublishOutbound(ctx, bus.OutboundMessage{
		Channel:  s.msg.Channel,
		ChatID:   s.msg.ChatID,
		Metadata: meta,
	})
}

func (s *streamHook) FinalizeContent(in string) string { return hook.StripThink(in) }

func cloneMeta(m map[string]any) map[string]any {
	out := make(map[string]any, len(m)+4)
	for k, v := range m {
		out[k] = v
	}
	return out
}

// --- loopHandle implements command.LoopHandle ------------------------------

type loopHandle struct{ l *Loop }

func (h *loopHandle) ClearSession(key string) error {
	h.l.mu.Lock()
	delete(h.l.active, key)
	h.l.mu.Unlock()
	s, err := h.l.opts.Sessions.GetOrCreate(key)
	if err != nil {
		return err
	}
	s.Clear()
	return h.l.opts.Sessions.Save(s)
}

func (h *loopHandle) Status() command.StatusInfo {
	return command.StatusInfo{
		Workspace: h.l.opts.Workspace,
		Model:     h.l.opts.Model,
		Active:    len(h.l.snapshotActive()),
	}
}

func (h *loopHandle) StopSession(key string) error {
	h.l.mu.Lock()
	defer h.l.mu.Unlock()
	if h.l.opts.Subagents != nil {
		// best-effort: cancel all subagents for this session key
	}
	delete(h.l.active, key)
	return nil
}

func (h *loopHandle) Restart() error { return errors.New("restart not supported yet") }
