package anthropic

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kuiba3/nanobot-go/internal/provider"
)

func TestChatNonStreaming(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), `"model":"claude-opus"`) {
			t.Fatalf("body missing model: %s", string(body))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"content":[{"type":"text","text":"hello"}],"usage":{"input_tokens":5,"output_tokens":2},"stop_reason":"end_turn"}`)
	}))
	defer srv.Close()
	p := New(Options{APIBase: srv.URL, APIKey: "k", DefaultModel: "claude-opus"})
	b, _ := json.Marshal("hi")
	resp, err := p.Chat(context.Background(), provider.ChatRequest{
		Messages: []provider.Message{{Role: "user", Content: b}},
	})
	if err != nil {
		t.Fatalf("chat: %v", err)
	}
	if resp.Content != "hello" {
		t.Fatalf("content=%q", resp.Content)
	}
	if resp.FinishReason != "stop" {
		t.Fatalf("finish=%q", resp.FinishReason)
	}
}

func TestChatToolUse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"content":[{"type":"tool_use","id":"t1","name":"read_file","input":{"path":"/tmp/x"}}],"usage":{"input_tokens":5,"output_tokens":2},"stop_reason":"tool_use"}`)
	}))
	defer srv.Close()
	p := New(Options{APIBase: srv.URL, APIKey: "k", DefaultModel: "claude-opus"})
	b, _ := json.Marshal("read please")
	resp, err := p.Chat(context.Background(), provider.ChatRequest{
		Messages: []provider.Message{{Role: "user", Content: b}},
	})
	if err != nil {
		t.Fatalf("chat: %v", err)
	}
	if !resp.ShouldExecuteTools() || resp.ToolCalls[0].Name != "read_file" {
		t.Fatalf("tool calls: %+v finish=%s", resp.ToolCalls, resp.FinishReason)
	}
	m, _ := resp.ToolCalls[0].ArgumentsMap()
	if m["path"] != "/tmp/x" {
		t.Fatalf("args: %+v", m)
	}
}

func TestSplitSystemAndMessages(t *testing.T) {
	msgs := []provider.Message{
		{Role: "system", Content: jsonStr("you are helpful")},
		{Role: "user", Content: jsonStr("hi")},
		{Role: "assistant", Content: jsonStr(""), ToolCalls: []provider.ToolCall{{ID: "c1", Type: "function", Function: provider.ToolCallFunction{Name: "read_file", Arguments: `{"path":"/x"}`}}}},
		{Role: "tool", ToolCallID: "c1", Content: jsonStr("contents")},
	}
	sys, out := splitSystemAndMessages(msgs)
	if sys != "you are helpful" {
		t.Fatalf("sys=%q", sys)
	}
	if len(out) != 3 {
		t.Fatalf("expected 3 blocks, got %d: %+v", len(out), out)
	}
	// third block should be a user with tool_result
	last := out[len(out)-1]
	if last["role"] != "user" {
		t.Fatalf("expected tool_result wrapped in user: %+v", last)
	}
}

func jsonStr(s string) json.RawMessage { b, _ := json.Marshal(s); return b }
