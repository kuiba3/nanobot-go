package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/hkuds/nanobot-go/channels/base"
	"github.com/hkuds/nanobot-go/internal/bus"
	"github.com/hkuds/nanobot-go/internal/command"
	"github.com/hkuds/nanobot-go/internal/ctxbuilder"
	"github.com/hkuds/nanobot-go/internal/loop"
	"github.com/hkuds/nanobot-go/internal/memory"
	openaiprov "github.com/hkuds/nanobot-go/internal/provider/openai"
	"github.com/hkuds/nanobot-go/internal/session"
	"github.com/hkuds/nanobot-go/internal/skills"
	"github.com/hkuds/nanobot-go/internal/tools"
)

// scriptedLLM stands in for a real OpenAI endpoint.
func scriptedLLM(t *testing.T, content string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, fmt.Sprintf(`{"choices":[{"finish_reason":"stop","message":{"role":"assistant","content":%q}}]}`, content))
	}))
}

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func TestAPIChatJSON(t *testing.T) {
	ws := t.TempDir()
	llm := scriptedLLM(t, "hello from fake!")
	defer llm.Close()

	b := bus.New(8)
	defer b.Close()
	sessions := session.NewManager(ws)
	store := memory.NewStore(ws)
	sl := skills.NewLoader(ws, "", nil)
	cb := ctxbuilder.New(ws, "", store, sl)
	reg := tools.NewRegistry()
	router := command.NewRouter()
	command.RegisterBuiltins(router)

	p := openaiprov.New(openaiprov.Options{APIBase: llm.URL, APIKey: "k", DefaultModel: "gpt-test"})
	l := loop.New(loop.Options{
		Bus: b, Provider: p, Workspace: ws, Model: "gpt-test",
		Context: cb, Sessions: sessions, Registry: reg, Commands: router,
	})

	port := freePort(t)
	mgr := base.NewManager(b)
	ch := New(Options{Host: "127.0.0.1", Port: port, Timeout: 5 * time.Second, Loop: l, Model: "gpt-test"})
	mgr.Register(ch)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := mgr.StartAll(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}
	go l.Run(ctx)
	defer mgr.StopAll(ctx)

	// Wait for server
	time.Sleep(200 * time.Millisecond)

	body := map[string]any{
		"model":    "gpt-test",
		"messages": []map[string]any{{"role": "user", "content": "hi"}},
	}
	payload, _ := json.Marshal(body)
	resp, err := http.Post(fmt.Sprintf("http://127.0.0.1:%d/v1/chat/completions", port), "application/json", bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	var parsed map[string]any
	_ = json.Unmarshal(data, &parsed)
	choices, _ := parsed["choices"].([]any)
	if len(choices) == 0 {
		t.Fatalf("no choices: %s", string(data))
	}
	first := choices[0].(map[string]any)
	msg := first["message"].(map[string]any)
	if !strings.Contains(msg["content"].(string), "hello from fake") {
		t.Fatalf("content: %q", msg["content"])
	}
}
