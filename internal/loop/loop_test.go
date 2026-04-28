package loop

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/kuiba3/nanobot-go/internal/bus"
	"github.com/kuiba3/nanobot-go/internal/command"
	"github.com/kuiba3/nanobot-go/internal/ctxbuilder"
	"github.com/kuiba3/nanobot-go/internal/memory"
	"github.com/kuiba3/nanobot-go/internal/provider"
	"github.com/kuiba3/nanobot-go/internal/session"
	"github.com/kuiba3/nanobot-go/internal/skills"
	"github.com/kuiba3/nanobot-go/internal/tools"
)

// fakeProvider returns a scripted sequence of responses.
type fakeProvider struct {
	responses []provider.LLMResponse
	idx       int
	calls     int
}

func (f *fakeProvider) Chat(ctx context.Context, req provider.ChatRequest) (*provider.LLMResponse, error) {
	r := f.next()
	return &r, nil
}

func (f *fakeProvider) ChatStream(ctx context.Context, req provider.ChatRequest, onDelta func(string)) (*provider.LLMResponse, error) {
	r := f.next()
	if onDelta != nil && r.Content != "" {
		onDelta(r.Content)
	}
	return &r, nil
}

func (f *fakeProvider) DefaultModel() string                    { return "fake" }
func (f *fakeProvider) Name() string                            { return "fake" }
func (f *fakeProvider) Settings() provider.GenerationSettings   { return provider.GenerationSettings{} }

func (f *fakeProvider) next() provider.LLMResponse {
	f.calls++
	if f.idx >= len(f.responses) {
		return provider.LLMResponse{FinishReason: "stop", Content: "no more scripted responses"}
	}
	r := f.responses[f.idx]
	f.idx++
	return r
}

func TestLoopSingleTurn(t *testing.T) {
	ws := t.TempDir()
	fp := &fakeProvider{responses: []provider.LLMResponse{
		{Content: "hi there!", FinishReason: "stop"},
	}}
	b := bus.New(4)
	defer b.Close()

	sessions := session.NewManager(ws)
	store := memory.NewStore(ws)
	sl := skills.NewLoader(ws, "", nil)
	cb := ctxbuilder.New(ws, "", store, sl)
	reg := tools.NewRegistry()

	router := command.NewRouter()
	command.RegisterBuiltins(router)

	l := New(Options{
		Bus:       b,
		Provider:  fp,
		Workspace: ws,
		Model:     "fake",
		Context:   cb,
		Sessions:  sessions,
		Registry:  reg,
		Commands:  router,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	out, err := l.ProcessDirect(ctx, bus.InboundMessage{
		Channel: "cli",
		ChatID:  "default",
		Content: "hello",
	})
	if err != nil {
		t.Fatalf("process: %v", err)
	}
	if out != "hi there!" {
		t.Fatalf("unexpected output: %q", out)
	}
}

func TestLoopToolCallTurn(t *testing.T) {
	ws := t.TempDir()
	// First response: ask to call echo tool. Second: final content.
	fp := &fakeProvider{responses: []provider.LLMResponse{
		{
			ToolCalls: []provider.ToolCallRequest{
				{ID: "c1", Name: "echo", Arguments: json.RawMessage(`{"text":"hi"}`)},
			},
			FinishReason: "tool_calls",
		},
		{Content: "done!", FinishReason: "stop"},
	}}
	b := bus.New(4)
	defer b.Close()

	sessions := session.NewManager(ws)
	store := memory.NewStore(ws)
	sl := skills.NewLoader(ws, "", nil)
	cb := ctxbuilder.New(ws, "", store, sl)

	reg := tools.NewRegistry()
	reg.Register(&echoTool{tools.Base{
		ToolName: "echo",
		Params: &tools.Schema{
			Type:       "object",
			Properties: map[string]*tools.Schema{"text": {Type: "string"}},
			Required:   []string{"text"},
		},
	}})

	router := command.NewRouter()
	command.RegisterBuiltins(router)

	l := New(Options{
		Bus:       b,
		Provider:  fp,
		Workspace: ws,
		Model:     "fake",
		Context:   cb,
		Sessions:  sessions,
		Registry:  reg,
		Commands:  router,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	out, err := l.ProcessDirect(ctx, bus.InboundMessage{
		Channel: "cli",
		ChatID:  "default",
		Content: "please echo hi",
	})
	if err != nil {
		t.Fatalf("process: %v", err)
	}
	if out != "done!" {
		t.Fatalf("unexpected: %q", out)
	}
	if fp.calls != 2 {
		t.Fatalf("expected 2 LLM calls, got %d", fp.calls)
	}

	// Session should contain: user, assistant(tool_calls), tool, assistant(final)
	s, _ := sessions.GetOrCreate("cli:default")
	if len(s.Messages) < 4 {
		t.Fatalf("expected >=4 messages in session, got %d: %+v", len(s.Messages), s.Messages)
	}
}

type echoTool struct{ tools.Base }

func (e *echoTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	if v, ok := args["text"].(string); ok {
		return v, nil
	}
	return "", nil
}
