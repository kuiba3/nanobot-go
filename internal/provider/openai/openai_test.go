package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kuiba3/nanobot-go/internal/provider"
)

func TestChatNonStreaming(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), `"model":"gpt-4o-mini"`) {
			t.Fatalf("missing model in body: %s", string(body))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{
			"choices": [
				{"finish_reason":"stop","message":{"role":"assistant","content":"hello world"}}
			],
			"usage":{"prompt_tokens":10,"completion_tokens":3,"total_tokens":13}
		}`)
	}))
	defer srv.Close()
	p := New(Options{APIBase: srv.URL, APIKey: "k", DefaultModel: "gpt-4o-mini"})
	resp, err := p.Chat(context.Background(), provider.ChatRequest{
		Messages: []provider.Message{mustUserMessage("hi")},
	})
	if err != nil {
		t.Fatalf("chat: %v", err)
	}
	if resp.Content != "hello world" {
		t.Fatalf("content: %q", resp.Content)
	}
	if resp.Usage.TotalTokens != 13 {
		t.Fatalf("usage: %+v", resp.Usage)
	}
}

func TestChatStream(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		chunks := []string{
			`{"choices":[{"index":0,"delta":{"role":"assistant","content":"Hel"}}]}`,
			`{"choices":[{"index":0,"delta":{"content":"lo"}}]}`,
			`{"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
		}
		for _, c := range chunks {
			fmt.Fprintf(w, "data: %s\n\n", c)
			if flusher != nil {
				flusher.Flush()
			}
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()
	p := New(Options{APIBase: srv.URL, APIKey: "k", DefaultModel: "m"})
	var got string
	resp, err := p.ChatStream(context.Background(), provider.ChatRequest{
		Messages: []provider.Message{mustUserMessage("hi")},
	}, func(s string) { got += s })
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	if got != "Hello" || resp.Content != "Hello" {
		t.Fatalf("expected Hello; delta=%q resp=%q", got, resp.Content)
	}
	if resp.FinishReason != "stop" {
		t.Fatalf("finish: %s", resp.FinishReason)
	}
}

func TestChatToolCall(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"choices":[{"finish_reason":"tool_calls","message":{"role":"assistant","content":null,"tool_calls":[{"id":"call_1","type":"function","function":{"name":"read_file","arguments":"{\"path\":\"/tmp/x\"}"}}]}}]}`)
	}))
	defer srv.Close()
	p := New(Options{APIBase: srv.URL, APIKey: "k", DefaultModel: "m"})
	resp, err := p.Chat(context.Background(), provider.ChatRequest{
		Messages: []provider.Message{mustUserMessage("call a tool")},
	})
	if err != nil {
		t.Fatalf("chat: %v", err)
	}
	if len(resp.ToolCalls) != 1 || resp.ToolCalls[0].Name != "read_file" {
		t.Fatalf("tool_calls: %+v", resp.ToolCalls)
	}
	args, _ := resp.ToolCalls[0].ArgumentsMap()
	if args["path"] != "/tmp/x" {
		t.Fatalf("args: %+v", args)
	}
	if !resp.ShouldExecuteTools() {
		t.Fatalf("expected should execute")
	}
}

func mustUserMessage(s string) provider.Message {
	b, _ := json.Marshal(s)
	return provider.Message{Role: "user", Content: json.RawMessage(b)}
}
