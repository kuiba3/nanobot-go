// Package provider defines the LLM provider abstraction. Concrete
// implementations live under sub-packages (openai, anthropic).
package provider

import (
	"context"
	"encoding/json"
)

// GenerationSettings is the set of sampling/knobs applied to requests.
type GenerationSettings struct {
	Temperature     float64
	MaxTokens       int
	ReasoningEffort string // "", "low", "medium", "high", "adaptive"
}

// ToolCallRequest is a normalized tool invocation produced by a provider.
type ToolCallRequest struct {
	ID                             string          `json:"id"`
	Name                           string          `json:"name"`
	Arguments                      json.RawMessage `json:"arguments"`
	ProviderSpecificFields         map[string]any  `json:"provider_specific_fields,omitempty"`
	FunctionProviderSpecificFields map[string]any  `json:"function_provider_specific_fields,omitempty"`
}

// ArgumentsMap decodes Arguments into a map[string]any.
func (t ToolCallRequest) ArgumentsMap() (map[string]any, error) {
	if len(t.Arguments) == 0 || string(t.Arguments) == "null" {
		return map[string]any{}, nil
	}
	var m map[string]any
	err := json.Unmarshal(t.Arguments, &m)
	return m, err
}

// LLMResponse is a normalized completion from any provider.
type LLMResponse struct {
	Content          string            `json:"content"`
	ToolCalls        []ToolCallRequest `json:"tool_calls,omitempty"`
	FinishReason     string            `json:"finish_reason"`
	Usage            Usage             `json:"usage"`
	RetryAfter       float64           `json:"retry_after,omitempty"`
	ReasoningContent string            `json:"reasoning_content,omitempty"`
	ThinkingBlocks   []json.RawMessage `json:"thinking_blocks,omitempty"`
	ErrorStatusCode  int               `json:"error_status_code,omitempty"`
	ErrorKind        string            `json:"error_kind,omitempty"`
	ErrorMessage     string            `json:"error_message,omitempty"`
	ErrorRetryable   bool              `json:"error_retryable,omitempty"`
}

// Usage captures token accounting.
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// ShouldExecuteTools reports whether the response should trigger tool execution.
// Matches the Python behavior: tool execution is permitted only when there are
// tool calls and finish_reason is "tool_calls" or "stop".
func (r *LLMResponse) ShouldExecuteTools() bool {
	if len(r.ToolCalls) == 0 {
		return false
	}
	switch r.FinishReason {
	case "tool_calls", "stop":
		return true
	}
	return false
}

// ChatRequest is the parameter bundle passed to Provider.Chat / ChatStream.
type ChatRequest struct {
	Messages        []Message         `json:"messages"`
	Tools           []ToolDefinition  `json:"tools,omitempty"`
	Model           string            `json:"model,omitempty"`
	MaxTokens       int               `json:"max_tokens,omitempty"`
	Temperature     float64           `json:"temperature,omitempty"`
	ReasoningEffort string            `json:"reasoning_effort,omitempty"`
	ToolChoice      any               `json:"tool_choice,omitempty"` // string | map[string]any
	Stream          bool              `json:"stream,omitempty"`
	Extra           map[string]any    `json:"extra,omitempty"`
	Headers         map[string]string `json:"headers,omitempty"`
}

// Message is the OpenAI-style chat message shape. Use RawMessage for Content
// so the loop can pass multimodal structures through unchanged.
type Message struct {
	Role             string            `json:"role"`
	Content          json.RawMessage   `json:"content,omitempty"`
	Name             string            `json:"name,omitempty"`
	ToolCallID       string            `json:"tool_call_id,omitempty"`
	ToolCalls        []ToolCall        `json:"tool_calls,omitempty"`
	ReasoningContent string            `json:"reasoning_content,omitempty"`
	ThinkingBlocks   []json.RawMessage `json:"thinking_blocks,omitempty"`
	ExtraContent     map[string]any    `json:"extra_content,omitempty"`
}

// ToolCall is the assistant-side representation of a past tool invocation.
type ToolCall struct {
	ID       string           `json:"id"`
	Type     string           `json:"type"`
	Function ToolCallFunction `json:"function"`
}

// ToolCallFunction holds the function name and a raw JSON arguments string.
type ToolCallFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// ToolDefinition is the OpenAI function definition shape. Anthropic adapter
// converts to input_schema on the wire but we accept a common shape here.
type ToolDefinition struct {
	Type     string   `json:"type"` // "function"
	Function Function `json:"function"`
}

// Function is the nested definition.
type Function struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

// Provider is the contract the agent loop speaks to the LLM over.
type Provider interface {
	// Chat issues a non-streaming completion.
	Chat(ctx context.Context, req ChatRequest) (*LLMResponse, error)
	// ChatStream issues a streaming completion. The callback receives content
	// deltas (stripped of reasoning where applicable). Implementations MUST
	// return the fully-aggregated LLMResponse when the stream completes.
	ChatStream(ctx context.Context, req ChatRequest, onDelta func(string)) (*LLMResponse, error)
	// DefaultModel returns the model the agent should use when the request model is empty.
	DefaultModel() string
	// Name returns a stable identifier (e.g. "openai", "anthropic") for logging.
	Name() string
	// Settings returns the generation defaults that are applied when fields are
	// zero in ChatRequest (Chat*WithRetry helpers consult this).
	Settings() GenerationSettings
}

// MessagePart is a helper to build a multimodal content list.
type MessagePart struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	ImageURL string `json:"image_url,omitempty"`
}
