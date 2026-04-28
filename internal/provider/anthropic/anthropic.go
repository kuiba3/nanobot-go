// Package anthropic implements the Anthropic Messages API provider with
// extended thinking support.
package anthropic

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/kuiba3/nanobot-go/internal/provider"
)

const anthropicVersion = "2023-06-01"

// Options configures a Provider.
type Options struct {
	APIKey       string
	APIBase      string // e.g. https://api.anthropic.com
	DefaultModel string
	ExtraHeaders map[string]string
	HTTPClient   *http.Client
	Generation   provider.GenerationSettings
}

// Provider is an Anthropic Messages API provider.
type Provider struct {
	opts Options
}

// New constructs a Provider.
func New(opts Options) *Provider {
	if opts.APIBase == "" {
		opts.APIBase = "https://api.anthropic.com"
	}
	opts.APIBase = strings.TrimRight(opts.APIBase, "/")
	if opts.HTTPClient == nil {
		opts.HTTPClient = &http.Client{Timeout: 180 * time.Second}
	}
	if opts.Generation.MaxTokens == 0 {
		opts.Generation.MaxTokens = 4096
	}
	return &Provider{opts: opts}
}

// Name returns the provider name.
func (p *Provider) Name() string { return "anthropic" }

// DefaultModel returns the configured default model.
func (p *Provider) DefaultModel() string { return p.opts.DefaultModel }

// Settings returns default generation settings.
func (p *Provider) Settings() provider.GenerationSettings { return p.opts.Generation }

// Chat issues a non-streaming Messages API request.
func (p *Provider) Chat(ctx context.Context, req provider.ChatRequest) (*provider.LLMResponse, error) {
	body, err := p.buildBody(req, false)
	if err != nil {
		return nil, err
	}
	httpReq, err := p.newRequest(ctx, body)
	if err != nil {
		return nil, err
	}
	resp, err := p.opts.HTTPClient.Do(httpReq)
	if err != nil {
		return p.errResp(err, 0), nil
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return p.errResp(err, resp.StatusCode), nil
	}
	if resp.StatusCode >= 400 {
		return p.httpErrResp(resp, data), nil
	}
	return parseResponse(data)
}

// ChatStream issues a streaming Messages request.
func (p *Provider) ChatStream(ctx context.Context, req provider.ChatRequest, onDelta func(string)) (*provider.LLMResponse, error) {
	body, err := p.buildBody(req, true)
	if err != nil {
		return nil, err
	}
	httpReq, err := p.newRequest(ctx, body)
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Accept", "text/event-stream")
	resp, err := p.opts.HTTPClient.Do(httpReq)
	if err != nil {
		return p.errResp(err, 0), nil
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		data, _ := io.ReadAll(resp.Body)
		return p.httpErrResp(resp, data), nil
	}
	return parseResponseStream(resp.Body, onDelta)
}

func (p *Provider) newRequest(ctx context.Context, body []byte) (*http.Request, error) {
	url := p.opts.APIBase + "/v1/messages"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("anthropic-version", anthropicVersion)
	if p.opts.APIKey != "" {
		req.Header.Set("x-api-key", p.opts.APIKey)
	}
	for k, v := range p.opts.ExtraHeaders {
		req.Header.Set(k, v)
	}
	return req, nil
}

// buildBody converts OpenAI-shaped ChatRequest into Anthropic Messages API shape.
func (p *Provider) buildBody(req provider.ChatRequest, stream bool) ([]byte, error) {
	sys, msgs := splitSystemAndMessages(req.Messages)
	body := map[string]any{
		"model":    orDefault(req.Model, p.opts.DefaultModel),
		"messages": msgs,
	}
	if sys != "" {
		body["system"] = sys
	}
	mt := req.MaxTokens
	if mt <= 0 {
		mt = p.opts.Generation.MaxTokens
	}
	if mt <= 0 {
		mt = 4096
	}
	body["max_tokens"] = mt

	temp := req.Temperature
	if temp == 0 {
		temp = p.opts.Generation.Temperature
	}

	// extended thinking mapping
	re := req.ReasoningEffort
	if re == "" {
		re = p.opts.Generation.ReasoningEffort
	}
	if re != "" && re != "none" && re != "off" {
		thinking := map[string]any{"type": "enabled"}
		switch re {
		case "adaptive":
			thinking["type"] = "enabled"
			thinking["budget_tokens"] = 2048
		case "low":
			thinking["budget_tokens"] = 1024
		case "medium":
			thinking["budget_tokens"] = 4096
		case "high":
			thinking["budget_tokens"] = 16384
		}
		body["thinking"] = thinking
		temp = 1.0 // required when thinking is on
	}
	if temp > 0 {
		body["temperature"] = temp
	}

	if len(req.Tools) > 0 {
		body["tools"] = toolsToAnthropic(req.Tools)
	}
	if req.ToolChoice != nil {
		body["tool_choice"] = mapToolChoice(req.ToolChoice)
	}
	if stream {
		body["stream"] = true
	}
	for k, v := range req.Extra {
		body[k] = v
	}
	return json.Marshal(body)
}

func orDefault(s, d string) string {
	if s == "" {
		return d
	}
	return s
}

// splitSystemAndMessages extracts the system message (concatenated) and converts
// other messages to Anthropic's content-blocks shape.
func splitSystemAndMessages(in []provider.Message) (string, []map[string]any) {
	var sysParts []string
	out := make([]map[string]any, 0, len(in))

	// Buffer consecutive tool results into a single user message with tool_result blocks.
	var toolResults []map[string]any
	flushToolResults := func() {
		if len(toolResults) == 0 {
			return
		}
		out = append(out, map[string]any{
			"role":    "user",
			"content": toolResults,
		})
		toolResults = nil
	}

	for _, m := range in {
		switch m.Role {
		case "system":
			if t := textFromContent(m.Content); t != "" {
				sysParts = append(sysParts, t)
			}
		case "user":
			flushToolResults()
			blocks := userContentBlocks(m)
			out = append(out, map[string]any{"role": "user", "content": blocks})
		case "assistant":
			flushToolResults()
			blocks := assistantContentBlocks(m)
			out = append(out, map[string]any{"role": "assistant", "content": blocks})
		case "tool":
			toolResults = append(toolResults, map[string]any{
				"type":        "tool_result",
				"tool_use_id": m.ToolCallID,
				"content":     textFromContent(m.Content),
			})
		}
	}
	flushToolResults()
	return strings.Join(sysParts, "\n\n"), out
}

func textFromContent(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	// multimodal list — extract text parts
	var arr []map[string]any
	if err := json.Unmarshal(raw, &arr); err == nil {
		var out []string
		for _, p := range arr {
			if t, ok := p["text"].(string); ok {
				out = append(out, t)
			}
		}
		return strings.Join(out, "\n")
	}
	return ""
}

func userContentBlocks(m provider.Message) []map[string]any {
	if len(m.Content) == 0 {
		return []map[string]any{{"type": "text", "text": ""}}
	}
	var arr []map[string]any
	if err := json.Unmarshal(m.Content, &arr); err == nil {
		// multimodal shape: convert image_url -> image block, text -> text block
		out := make([]map[string]any, 0, len(arr))
		for _, p := range arr {
			typ, _ := p["type"].(string)
			switch typ {
			case "text":
				out = append(out, map[string]any{"type": "text", "text": p["text"]})
			case "image_url":
				if u, ok := p["image_url"].(string); ok {
					out = append(out, imageBlockFromURL(u))
				}
			}
		}
		if len(out) > 0 {
			return out
		}
	}
	return []map[string]any{{"type": "text", "text": textFromContent(m.Content)}}
}

func imageBlockFromURL(u string) map[string]any {
	if strings.HasPrefix(u, "data:") {
		semi := strings.Index(u, ";")
		comma := strings.Index(u, ",")
		if semi > 5 && comma > semi {
			mt := u[5:semi]
			data := u[comma+1:]
			return map[string]any{
				"type":   "image",
				"source": map[string]any{"type": "base64", "media_type": mt, "data": data},
			}
		}
	}
	return map[string]any{
		"type":   "image",
		"source": map[string]any{"type": "url", "url": u},
	}
}

func assistantContentBlocks(m provider.Message) []map[string]any {
	blocks := make([]map[string]any, 0)
	for _, tb := range m.ThinkingBlocks {
		var raw map[string]any
		if err := json.Unmarshal(tb, &raw); err == nil {
			blocks = append(blocks, raw)
		}
	}
	if t := textFromContent(m.Content); t != "" {
		blocks = append(blocks, map[string]any{"type": "text", "text": t})
	}
	for _, tc := range m.ToolCalls {
		var input any = map[string]any{}
		if tc.Function.Arguments != "" {
			_ = json.Unmarshal([]byte(tc.Function.Arguments), &input)
		}
		blocks = append(blocks, map[string]any{
			"type":  "tool_use",
			"id":    tc.ID,
			"name":  tc.Function.Name,
			"input": input,
		})
	}
	if len(blocks) == 0 {
		blocks = append(blocks, map[string]any{"type": "text", "text": ""})
	}
	return blocks
}

func toolsToAnthropic(tools []provider.ToolDefinition) []map[string]any {
	out := make([]map[string]any, 0, len(tools))
	for _, t := range tools {
		out = append(out, map[string]any{
			"name":         t.Function.Name,
			"description":  t.Function.Description,
			"input_schema": t.Function.Parameters,
		})
	}
	return out
}

func mapToolChoice(v any) any {
	switch s := v.(type) {
	case string:
		switch s {
		case "auto":
			return map[string]any{"type": "auto"}
		case "required", "any":
			return map[string]any{"type": "any"}
		case "none":
			return map[string]any{"type": "none"}
		}
	case map[string]any:
		if fn, ok := s["function"].(map[string]any); ok {
			if name, ok := fn["name"].(string); ok {
				return map[string]any{"type": "tool", "name": name}
			}
		}
	}
	return v
}

// --- response parsing ------------------------------------------------------

type anthResponse struct {
	Content []anthBlock `json:"content"`
	Usage   struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
	StopReason string `json:"stop_reason"`
}

type anthBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	Thinking  string          `json:"thinking,omitempty"`
	Signature string          `json:"signature,omitempty"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
}

func parseResponse(data []byte) (*provider.LLMResponse, error) {
	var raw anthResponse
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("anthropic parse: %w", err)
	}
	resp := &provider.LLMResponse{
		Usage: provider.Usage{
			PromptTokens:     raw.Usage.InputTokens,
			CompletionTokens: raw.Usage.OutputTokens,
			TotalTokens:      raw.Usage.InputTokens + raw.Usage.OutputTokens,
		},
		FinishReason: stopReasonToFinish(raw.StopReason),
	}
	var text strings.Builder
	for _, b := range raw.Content {
		switch b.Type {
		case "text":
			text.WriteString(b.Text)
		case "thinking":
			blk, _ := json.Marshal(map[string]any{"type": "thinking", "thinking": b.Thinking, "signature": b.Signature})
			resp.ThinkingBlocks = append(resp.ThinkingBlocks, blk)
		case "tool_use":
			args := b.Input
			if len(args) == 0 {
				args = json.RawMessage([]byte("{}"))
			}
			resp.ToolCalls = append(resp.ToolCalls, provider.ToolCallRequest{
				ID:        b.ID,
				Name:      b.Name,
				Arguments: args,
			})
		}
	}
	resp.Content = text.String()
	if len(resp.ToolCalls) > 0 && resp.FinishReason == "" {
		resp.FinishReason = "tool_calls"
	}
	return resp, nil
}

func stopReasonToFinish(r string) string {
	switch r {
	case "end_turn", "stop_sequence":
		return "stop"
	case "tool_use":
		return "tool_calls"
	case "max_tokens":
		return "length"
	case "":
		return "stop"
	}
	return r
}

// --- streaming -------------------------------------------------------------

func parseResponseStream(body io.Reader, onDelta func(string)) (*provider.LLMResponse, error) {
	sc := bufio.NewScanner(body)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)

	resp := &provider.LLMResponse{FinishReason: "stop"}
	var text strings.Builder

	// in-flight content-block state
	type blockState struct {
		typ       string
		name      string
		id        string
		input     strings.Builder
		thinking  strings.Builder
		signature string
	}
	blocks := make(map[int]*blockState)

	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" {
			continue
		}
		var env map[string]any
		if err := json.Unmarshal([]byte(payload), &env); err != nil {
			continue
		}
		switch env["type"] {
		case "content_block_start":
			idx := intField(env, "index")
			cb, _ := env["content_block"].(map[string]any)
			b := &blockState{typ: strField(cb, "type"), id: strField(cb, "id"), name: strField(cb, "name")}
			blocks[idx] = b
		case "content_block_delta":
			idx := intField(env, "index")
			b := blocks[idx]
			if b == nil {
				continue
			}
			d, _ := env["delta"].(map[string]any)
			switch strField(d, "type") {
			case "text_delta":
				if t, ok := d["text"].(string); ok {
					text.WriteString(t)
					if onDelta != nil {
						onDelta(t)
					}
				}
			case "thinking_delta":
				if t, ok := d["thinking"].(string); ok {
					b.thinking.WriteString(t)
				}
			case "signature_delta":
				if s, ok := d["signature"].(string); ok {
					b.signature += s
				}
			case "input_json_delta":
				if s, ok := d["partial_json"].(string); ok {
					b.input.WriteString(s)
				}
			}
		case "message_delta":
			if d, ok := env["delta"].(map[string]any); ok {
				if sr, ok := d["stop_reason"].(string); ok {
					resp.FinishReason = stopReasonToFinish(sr)
				}
			}
			if u, ok := env["usage"].(map[string]any); ok {
				if n, ok := u["output_tokens"].(float64); ok {
					resp.Usage.CompletionTokens = int(n)
				}
			}
		case "message_stop":
			// end marker
		}
	}
	if err := sc.Err(); err != nil {
		return resp, fmt.Errorf("stream: %w", err)
	}
	// finalize blocks
	for _, b := range blocks {
		switch b.typ {
		case "tool_use":
			args := json.RawMessage([]byte(b.input.String()))
			if len(args) == 0 {
				args = json.RawMessage("{}")
			}
			resp.ToolCalls = append(resp.ToolCalls, provider.ToolCallRequest{
				ID:        b.id,
				Name:      b.name,
				Arguments: args,
			})
		case "thinking":
			blk, _ := json.Marshal(map[string]any{
				"type":      "thinking",
				"thinking":  b.thinking.String(),
				"signature": b.signature,
			})
			resp.ThinkingBlocks = append(resp.ThinkingBlocks, blk)
		}
	}
	resp.Content = text.String()
	if len(resp.ToolCalls) > 0 && resp.FinishReason == "stop" {
		resp.FinishReason = "tool_calls"
	}
	return resp, nil
}

func strField(m map[string]any, k string) string {
	if m == nil {
		return ""
	}
	if v, ok := m[k].(string); ok {
		return v
	}
	return ""
}

func intField(m map[string]any, k string) int {
	if m == nil {
		return 0
	}
	if v, ok := m[k].(float64); ok {
		return int(v)
	}
	return 0
}

func (p *Provider) errResp(err error, status int) *provider.LLMResponse {
	kind := classifyErrKind(err)
	return &provider.LLMResponse{
		FinishReason:    "error",
		ErrorKind:       kind,
		ErrorMessage:    err.Error(),
		ErrorStatusCode: status,
		ErrorRetryable:  kind != "",
	}
}

func (p *Provider) httpErrResp(resp *http.Response, body []byte) *provider.LLMResponse {
	r := &provider.LLMResponse{
		FinishReason:    "error",
		ErrorStatusCode: resp.StatusCode,
		ErrorMessage:    strings.TrimSpace(string(body)),
	}
	if resp.StatusCode == 429 {
		r.ErrorKind = "rate_limit"
		r.ErrorRetryable = true
	}
	if resp.StatusCode >= 500 {
		r.ErrorKind = "server_error"
		r.ErrorRetryable = true
	}
	if h := resp.Header.Get("Retry-After"); h != "" {
		if n, err := strconv.ParseFloat(h, 64); err == nil {
			r.RetryAfter = n
		}
	}
	return r
}

func classifyErrKind(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "timeout") {
		return "timeout"
	}
	if strings.Contains(msg, "connection") {
		return "connection"
	}
	return ""
}
