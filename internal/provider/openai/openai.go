// Package openai implements an OpenAI Chat Completions-compatible provider.
// It talks to any endpoint that speaks the same request/response shape
// (OpenAI, Azure OpenAI with an override, OpenRouter, Ollama, vLLM, ...).
package openai

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

// Options configures a Provider.
type Options struct {
	APIKey       string
	APIBase      string // e.g. https://api.openai.com/v1
	DefaultModel string
	ExtraHeaders map[string]string
	HTTPClient   *http.Client
	Generation   provider.GenerationSettings
	// StreamIdleTimeout is the max gap between stream events. 0 = 90s default.
	StreamIdleTimeout time.Duration
}

// Provider is an OpenAI-compatible chat provider.
type Provider struct {
	opts Options
}

// New constructs a Provider.
func New(opts Options) *Provider {
	if opts.APIBase == "" {
		opts.APIBase = "https://api.openai.com/v1"
	}
	opts.APIBase = strings.TrimRight(opts.APIBase, "/")
	if opts.HTTPClient == nil {
		opts.HTTPClient = &http.Client{Timeout: 120 * time.Second}
	}
	if opts.StreamIdleTimeout == 0 {
		opts.StreamIdleTimeout = 90 * time.Second
	}
	if opts.Generation.Temperature == 0 {
		opts.Generation.Temperature = 0.7
	}
	if opts.Generation.MaxTokens == 0 {
		opts.Generation.MaxTokens = 4096
	}
	return &Provider{opts: opts}
}

// Name returns the provider name.
func (p *Provider) Name() string { return "openai" }

// DefaultModel returns the configured default model.
func (p *Provider) DefaultModel() string { return p.opts.DefaultModel }

// Settings returns default generation settings.
func (p *Provider) Settings() provider.GenerationSettings { return p.opts.Generation }

// Chat issues a non-streaming chat completion.
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
		return p.errorResponse(err, 0), nil
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return p.errorResponse(err, resp.StatusCode), nil
	}
	if resp.StatusCode >= 400 {
		return p.errorFromHTTP(resp, data), nil
	}
	return parseChatCompletion(data)
}

// ChatStream issues a streaming completion, invoking onDelta for every content delta.
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
		return p.errorResponse(err, 0), nil
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		data, _ := io.ReadAll(resp.Body)
		return p.errorFromHTTP(resp, data), nil
	}
	return parseChatCompletionStream(resp.Body, onDelta)
}

func (p *Provider) newRequest(ctx context.Context, body []byte) (*http.Request, error) {
	url := p.opts.APIBase + "/chat/completions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if p.opts.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+p.opts.APIKey)
	}
	for k, v := range p.opts.ExtraHeaders {
		req.Header.Set(k, v)
	}
	return req, nil
}

func (p *Provider) buildBody(req provider.ChatRequest, stream bool) ([]byte, error) {
	msgs := sanitizeMessages(req.Messages)
	body := map[string]any{
		"model":    orDefault(req.Model, p.opts.DefaultModel),
		"messages": msgs,
	}
	if len(req.Tools) > 0 {
		body["tools"] = req.Tools
	}
	if req.ToolChoice != nil {
		body["tool_choice"] = req.ToolChoice
	}
	if req.MaxTokens > 0 {
		body["max_tokens"] = req.MaxTokens
	} else if p.opts.Generation.MaxTokens > 0 {
		body["max_tokens"] = p.opts.Generation.MaxTokens
	}
	if req.Temperature > 0 {
		body["temperature"] = req.Temperature
	} else if p.opts.Generation.Temperature > 0 {
		body["temperature"] = p.opts.Generation.Temperature
	}
	re := req.ReasoningEffort
	if re == "" {
		re = p.opts.Generation.ReasoningEffort
	}
	if re != "" {
		body["reasoning_effort"] = re
	}
	if stream {
		body["stream"] = true
	}
	for k, v := range req.Extra {
		body[k] = v
	}
	return json.Marshal(body)
}

// sanitizeMessages drops keys not accepted by the Chat Completions API and
// coalesces consecutive same-role text messages.
func sanitizeMessages(in []provider.Message) []map[string]any {
	allowed := map[string]bool{
		"role":              true,
		"content":           true,
		"name":              true,
		"tool_call_id":      true,
		"tool_calls":        true,
		"reasoning_content": true,
	}
	out := make([]map[string]any, 0, len(in))
	for _, m := range in {
		raw := map[string]any{}
		b, _ := json.Marshal(m)
		tmp := map[string]any{}
		_ = json.Unmarshal(b, &tmp)
		for k, v := range tmp {
			if allowed[k] {
				raw[k] = v
			}
		}
		// OpenAI requires content to be at minimum an empty string on user/system
		if raw["role"] == "user" || raw["role"] == "system" {
			if _, ok := raw["content"]; !ok {
				raw["content"] = ""
			}
		}
		out = append(out, raw)
	}
	return out
}

func orDefault(s, d string) string {
	if s == "" {
		return d
	}
	return s
}

// --- response parsing --------------------------------------------------------

type chatCompletionResp struct {
	Choices []struct {
		Index        int `json:"index"`
		FinishReason string `json:"finish_reason"`
		Message      struct {
			Role             string                   `json:"role"`
			Content          string                   `json:"content"`
			ReasoningContent string                   `json:"reasoning_content"`
			Reasoning        string                   `json:"reasoning"`
			ToolCalls        []oaiToolCall            `json:"tool_calls"`
		} `json:"message"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage"`
}

type oaiToolCall struct {
	Index    int    `json:"index"`
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

func parseChatCompletion(data []byte) (*provider.LLMResponse, error) {
	var raw chatCompletionResp
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("openai parse: %w", err)
	}
	if len(raw.Choices) == 0 {
		return &provider.LLMResponse{FinishReason: "stop"}, nil
	}
	c := raw.Choices[0]
	resp := &provider.LLMResponse{
		Content:      c.Message.Content,
		FinishReason: c.FinishReason,
		Usage: provider.Usage{
			PromptTokens:     raw.Usage.PromptTokens,
			CompletionTokens: raw.Usage.CompletionTokens,
			TotalTokens:      raw.Usage.TotalTokens,
		},
		ReasoningContent: firstNonEmpty(c.Message.ReasoningContent, c.Message.Reasoning),
	}
	for _, tc := range c.Message.ToolCalls {
		args := json.RawMessage([]byte("{}"))
		if tc.Function.Arguments != "" {
			args = json.RawMessage(tc.Function.Arguments)
		}
		resp.ToolCalls = append(resp.ToolCalls, provider.ToolCallRequest{
			ID:        firstNonEmpty(tc.ID, shortID()),
			Name:      tc.Function.Name,
			Arguments: args,
		})
	}
	if len(resp.ToolCalls) > 0 && resp.FinishReason == "" {
		resp.FinishReason = "tool_calls"
	}
	return resp, nil
}

func firstNonEmpty(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}

var idCounter uint64

func shortID() string {
	idCounter++
	return "call_" + strconv.FormatUint(idCounter, 36)
}

// --- streaming -------------------------------------------------------------

type streamChunk struct {
	Choices []struct {
		Index int `json:"index"`
		Delta struct {
			Role             string        `json:"role"`
			Content          string        `json:"content"`
			ReasoningContent string        `json:"reasoning_content"`
			Reasoning        string        `json:"reasoning"`
			ToolCalls        []oaiToolCall `json:"tool_calls"`
		} `json:"delta"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage"`
}

func parseChatCompletionStream(body io.Reader, onDelta func(string)) (*provider.LLMResponse, error) {
	sc := bufio.NewScanner(body)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	resp := &provider.LLMResponse{FinishReason: "stop"}
	var content strings.Builder
	var reasoning strings.Builder
	toolBuf := make(map[int]*provider.ToolCallRequest)
	toolArgs := make(map[int]*strings.Builder)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" || payload == "[DONE]" {
			if payload == "[DONE]" {
				break
			}
			continue
		}
		var chunk streamChunk
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			continue
		}
		if chunk.Usage.TotalTokens > 0 {
			resp.Usage.PromptTokens = chunk.Usage.PromptTokens
			resp.Usage.CompletionTokens = chunk.Usage.CompletionTokens
			resp.Usage.TotalTokens = chunk.Usage.TotalTokens
		}
		for _, c := range chunk.Choices {
			if c.Delta.Content != "" {
				content.WriteString(c.Delta.Content)
				if onDelta != nil {
					onDelta(c.Delta.Content)
				}
			}
			if c.Delta.ReasoningContent != "" {
				reasoning.WriteString(c.Delta.ReasoningContent)
			} else if c.Delta.Reasoning != "" {
				reasoning.WriteString(c.Delta.Reasoning)
			}
			for _, tc := range c.Delta.ToolCalls {
				idx := tc.Index
				existing, ok := toolBuf[idx]
				if !ok {
					existing = &provider.ToolCallRequest{ID: firstNonEmpty(tc.ID, shortID()), Name: tc.Function.Name}
					toolBuf[idx] = existing
					toolArgs[idx] = &strings.Builder{}
				}
				if tc.Function.Name != "" && existing.Name == "" {
					existing.Name = tc.Function.Name
				}
				if tc.ID != "" && existing.ID == "" {
					existing.ID = tc.ID
				}
				if tc.Function.Arguments != "" {
					toolArgs[idx].WriteString(tc.Function.Arguments)
				}
			}
			if c.FinishReason != "" {
				resp.FinishReason = c.FinishReason
			}
		}
	}
	if err := sc.Err(); err != nil {
		return resp, fmt.Errorf("stream: %w", err)
	}
	resp.Content = content.String()
	resp.ReasoningContent = reasoning.String()
	for idx, tc := range toolBuf {
		args := json.RawMessage([]byte("{}"))
		if toolArgs[idx].Len() > 0 {
			args = json.RawMessage(toolArgs[idx].String())
		}
		tc.Arguments = args
		resp.ToolCalls = append(resp.ToolCalls, *tc)
	}
	if len(resp.ToolCalls) > 0 && resp.FinishReason == "stop" {
		resp.FinishReason = "tool_calls"
	}
	return resp, nil
}

// --- error handling --------------------------------------------------------

func (p *Provider) errorResponse(err error, status int) *provider.LLMResponse {
	kind := classifyErrKind(err)
	return &provider.LLMResponse{
		FinishReason:    "error",
		ErrorKind:       kind,
		ErrorMessage:    err.Error(),
		ErrorStatusCode: status,
		ErrorRetryable:  kind != "",
	}
}

func (p *Provider) errorFromHTTP(resp *http.Response, body []byte) *provider.LLMResponse {
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
