// Package api implements the OpenAI-compatible HTTP API channel. It exposes
// /v1/chat/completions (JSON + multipart, streaming via SSE), /v1/models, and
// /health. Mirrors Python nanobot/api/server.py.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/hkuds/nanobot-go/channels/base"
	"github.com/hkuds/nanobot-go/internal/bus"
	"github.com/hkuds/nanobot-go/internal/loop"
)

// Options configures the API channel.
type Options struct {
	Host    string
	Port    int
	Timeout time.Duration
	Loop    *loop.Loop
	Model   string
}

// Channel is the API channel implementation.
type Channel struct {
	opts   Options
	server *http.Server

	// awaiting maps a session_key to a waiter so ProcessDirect's response
	// can be returned to the caller. For streaming, we instead hook into the
	// loop's on-stream callback via ProcessDirect's response streamer below.
	mu       sync.Mutex
	awaiting map[string]chan streamEvent
}

type streamEvent struct {
	Delta string
	End   bool
	Final string
}

// New constructs a Channel.
func New(opts Options) *Channel {
	if opts.Host == "" {
		opts.Host = "127.0.0.1"
	}
	if opts.Port == 0 {
		opts.Port = 8900
	}
	if opts.Timeout == 0 {
		opts.Timeout = 120 * time.Second
	}
	return &Channel{opts: opts, awaiting: make(map[string]chan streamEvent)}
}

// Name returns "api".
func (c *Channel) Name() string { return "api" }

// SupportsStreaming reports true; actual SSE happens in HandleChat.
func (c *Channel) SupportsStreaming() bool { return true }

// Start launches the HTTP server.
func (c *Channel) Start(ctx context.Context) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", c.handleHealth)
	mux.HandleFunc("/v1/models", c.handleModels)
	mux.HandleFunc("/v1/chat/completions", c.handleChat)

	addr := fmt.Sprintf("%s:%d", c.opts.Host, c.opts.Port)
	c.server = &http.Server{Addr: addr, Handler: mux, ReadTimeout: c.opts.Timeout, WriteTimeout: c.opts.Timeout}
	go func() {
		if err := c.server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("api channel: serve: %v", err)
		}
	}()
	log.Printf("api channel listening on http://%s", addr)
	return nil
}

// Stop shuts down the server.
func (c *Channel) Stop(ctx context.Context) error {
	if c.server == nil {
		return nil
	}
	shutdownCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return c.server.Shutdown(shutdownCtx)
}

// Send is a no-op: the API channel returns content synchronously from HandleChat.
func (c *Channel) Send(ctx context.Context, m bus.OutboundMessage) error {
	c.dispatchEvent(m, streamEvent{Final: m.Content, End: true})
	return nil
}

// SendDelta forwards streaming deltas.
func (c *Channel) SendDelta(ctx context.Context, m bus.OutboundMessage) error {
	if v, ok := m.Metadata[bus.MetaStreamEnd].(bool); ok && v {
		c.dispatchEvent(m, streamEvent{End: true})
		return nil
	}
	c.dispatchEvent(m, streamEvent{Delta: m.Content})
	return nil
}

func (c *Channel) dispatchEvent(m bus.OutboundMessage, ev streamEvent) {
	key := m.Channel + ":" + m.ChatID
	c.mu.Lock()
	ch := c.awaiting[key]
	c.mu.Unlock()
	if ch == nil {
		return
	}
	select {
	case ch <- ev:
	default:
	}
}

// --- HTTP handlers ---------------------------------------------------------

func (c *Channel) handleHealth(w http.ResponseWriter, r *http.Request) {
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func (c *Channel) handleModels(w http.ResponseWriter, r *http.Request) {
	_ = json.NewEncoder(w).Encode(map[string]any{
		"object": "list",
		"data": []map[string]any{
			{"id": c.opts.Model, "object": "model", "owned_by": "nanobot-go"},
		},
	})
}

type chatReq struct {
	Model     string         `json:"model"`
	Messages  []any          `json:"messages"`
	Stream    bool           `json:"stream"`
	SessionID string         `json:"session_id"`
}

func (c *Channel) handleChat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
		c.handleChatMultipart(w, r)
		return
	}

	var req chatReq
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 20<<20))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := json.Unmarshal(data, &req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if req.Model != "" && c.opts.Model != "" && req.Model != c.opts.Model {
		http.Error(w, fmt.Sprintf("model %q not served; configured model is %q", req.Model, c.opts.Model), http.StatusBadRequest)
		return
	}

	text := extractLastUserText(req.Messages)
	if text == "" {
		http.Error(w, "exactly one non-empty user message is required", http.StatusBadRequest)
		return
	}

	sessionID := req.SessionID
	if sessionID == "" {
		sessionID = "default"
	}
	ch := bus.InboundMessage{
		Channel: "api",
		ChatID:  sessionID,
		Content: text,
		Metadata: map[string]any{
			bus.MetaWantsStream: req.Stream,
		},
	}

	if req.Stream {
		c.streamResponse(w, r, ch)
		return
	}
	c.singleResponse(w, r, ch)
}

func (c *Channel) handleChatMultipart(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(20 << 20); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	msg := r.FormValue("message")
	if msg == "" {
		msg = "..."
	}
	sessionID := r.FormValue("session_id")
	if sessionID == "" {
		sessionID = "default"
	}
	stream := r.FormValue("stream") == "true"
	ch := bus.InboundMessage{
		Channel: "api",
		ChatID:  sessionID,
		Content: msg,
		Metadata: map[string]any{bus.MetaWantsStream: stream},
	}
	if stream {
		c.streamResponse(w, r, ch)
		return
	}
	c.singleResponse(w, r, ch)
}

func (c *Channel) singleResponse(w http.ResponseWriter, r *http.Request, msg bus.InboundMessage) {
	ctx, cancel := context.WithTimeout(r.Context(), c.opts.Timeout)
	defer cancel()
	out, err := c.opts.Loop.ProcessDirect(ctx, msg)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(chatCompletion(c.opts.Model, out))
}

func (c *Channel) streamResponse(w http.ResponseWriter, r *http.Request, msg bus.InboundMessage) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	key := msg.Channel + ":" + msg.ChatID
	ch := make(chan streamEvent, 32)
	c.mu.Lock()
	c.awaiting[key] = ch
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.awaiting, key)
		c.mu.Unlock()
	}()

	// run the agent in a goroutine
	go func() {
		ctx, cancel := context.WithTimeout(r.Context(), c.opts.Timeout)
		defer cancel()
		final, err := c.opts.Loop.ProcessDirect(ctx, msg)
		ev := streamEvent{Final: final, End: true}
		if err != nil {
			ev.Final = "error: " + err.Error()
		}
		ch <- ev
	}()

	model := c.opts.Model
	sent := false
	for ev := range ch {
		if ev.Delta != "" {
			writeSSE(w, flusher, chunk(model, ev.Delta, ""))
			sent = true
		}
		if ev.End {
			if !sent && ev.Final != "" {
				writeSSE(w, flusher, chunk(model, ev.Final, ""))
			}
			writeSSE(w, flusher, chunk(model, "", "stop"))
			fmt.Fprint(w, "data: [DONE]\n\n")
			flusher.Flush()
			return
		}
	}
}

func writeSSE(w http.ResponseWriter, flusher http.Flusher, payload any) {
	data, _ := json.Marshal(payload)
	fmt.Fprintf(w, "data: %s\n\n", data)
	flusher.Flush()
}

func chatCompletion(model, content string) map[string]any {
	return map[string]any{
		"id":      "chatcmpl-nanobot",
		"object":  "chat.completion",
		"created": time.Now().Unix(),
		"model":   model,
		"choices": []map[string]any{
			{
				"index": 0,
				"message": map[string]any{
					"role":    "assistant",
					"content": content,
				},
				"finish_reason": "stop",
			},
		},
		"usage": map[string]any{"prompt_tokens": 0, "completion_tokens": 0, "total_tokens": 0},
	}
}

func chunk(model, delta, finish string) map[string]any {
	choice := map[string]any{"index": 0, "delta": map[string]any{"content": delta}}
	if finish != "" {
		choice["finish_reason"] = finish
		choice["delta"] = map[string]any{}
	}
	return map[string]any{
		"id":      "chatcmpl-nanobot",
		"object":  "chat.completion.chunk",
		"created": time.Now().Unix(),
		"model":   model,
		"choices": []any{choice},
	}
}

func extractLastUserText(msgs []any) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		m, ok := msgs[i].(map[string]any)
		if !ok {
			continue
		}
		if role, _ := m["role"].(string); role != "user" {
			continue
		}
		switch c := m["content"].(type) {
		case string:
			if strings.TrimSpace(c) != "" {
				return c
			}
		case []any:
			var b strings.Builder
			for _, p := range c {
				if pm, ok := p.(map[string]any); ok {
					if t, _ := pm["type"].(string); t == "text" {
						b.WriteString(asString(pm["text"]))
						b.WriteByte('\n')
					}
				}
			}
			if s := strings.TrimSpace(b.String()); s != "" {
				return s
			}
		}
	}
	return ""
}

func asString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

// ensure Channel implements base.Channel
var _ base.Channel = (*Channel)(nil)
