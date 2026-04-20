// Package runner implements AgentRunner — a provider-agnostic turn loop that
// drives an LLM + ToolRegistry to a final response. Mirrors Python
// agent/runner.py.
package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/hkuds/nanobot-go/internal/ctxbuilder"
	"github.com/hkuds/nanobot-go/internal/hook"
	"github.com/hkuds/nanobot-go/internal/provider"
	"github.com/hkuds/nanobot-go/internal/tools"
)

// Spec is the input bundle for a single agent turn.
type Spec struct {
	InitialMessages []provider.Message
	Registry        *tools.Registry
	Model           string
	MaxIterations   int
	MaxToolResultChars int
	Temperature     float64
	MaxTokens       int
	ReasoningEffort string
	Hook            hook.Hook
	ConcurrentTools bool
	SessionKey      string
	Workspace       string
	RetryPolicy     provider.RetryPolicy
	// InjectionCallback is consulted between iterations to drain any pending
	// user messages that arrived mid-turn. It returns the messages to merge.
	InjectionCallback func(ctx context.Context) []provider.Message
	// CheckpointCallback is invoked after tool execution and final content so
	// the loop can persist state for crash recovery.
	CheckpointCallback func(ctx context.Context, phase string, messages []provider.Message)
	// FailOnToolError requests the runner to abort on any tool error.
	FailOnToolError bool
}

// Result captures the outcome of a run.
type Result struct {
	FinalContent   string
	Messages       []provider.Message
	ToolsUsed      []string
	Usage          provider.Usage
	StopReason     string
	Error          error
	ToolEvents     []hook.ToolResult
	HadInjections  bool
}

// Constants mirroring the Python runner.
const (
	MaxEmptyRetries      = 2
	MaxLengthRecoveries  = 3
	MaxInjectionsPerTurn = 3
	MaxInjectionCycles   = 5

	DefaultErrorMessage   = "Sorry, I encountered an error calling the AI model."
	MaxIterationsMessage  = "The agent reached the maximum number of tool iterations for this turn."
)

// Runner owns a Provider and executes AgentRunSpec instances.
type Runner struct {
	Provider provider.Provider
}

// New constructs a Runner.
func New(p provider.Provider) *Runner { return &Runner{Provider: p} }

// Run executes the spec.
func (r *Runner) Run(ctx context.Context, spec Spec) (*Result, error) {
	if spec.Hook == nil {
		spec.Hook = hook.Base{}
	}
	if spec.MaxIterations <= 0 {
		spec.MaxIterations = 30
	}
	if spec.MaxToolResultChars <= 0 {
		spec.MaxToolResultChars = 12000
	}
	if spec.RetryPolicy.MaxAttempts == 0 && len(spec.RetryPolicy.Backoff) == 0 {
		spec.RetryPolicy = provider.Defaults()
	}

	msgs := append([]provider.Message{}, spec.InitialMessages...)
	result := &Result{StopReason: "unknown"}

	var (
		emptyRetries     int
		lengthRecoveries int
		hadInjections    bool
	)

	for iteration := 0; iteration < spec.MaxIterations; iteration++ {
		hctx := &hook.Context{Iteration: iteration, Messages: msgs}
		if err := spec.Hook.BeforeIteration(ctx, hctx); err != nil {
			return result, err
		}

		// Request LLM (stream if hook wants).
		llmResp, err := r.request(ctx, spec, msgs, hctx)
		if err != nil {
			result.Error = err
			result.StopReason = "error"
			return result, err
		}
		hctx.Response = llmResp
		hctx.Usage = llmResp.Usage
		hctx.ToolCalls = llmResp.ToolCalls
		result.Usage = addUsage(result.Usage, llmResp.Usage)

		if llmResp.FinishReason == "error" {
			result.Error = fmt.Errorf("%s", defaultIf(llmResp.ErrorMessage, DefaultErrorMessage))
			result.StopReason = "error"
			return result, result.Error
		}

		if llmResp.ShouldExecuteTools() {
			// Append assistant message with tool_calls
			tc := toolCallsToMessage(llmResp.ToolCalls)
			msgs = ctxbuilder.AppendAssistant(msgs, llmResp.Content, tc, llmResp.ThinkingBlocks)

			if err := spec.Hook.OnStreamEnd(ctx, hctx, true); err != nil {
				return result, err
			}
			if spec.CheckpointCallback != nil {
				spec.CheckpointCallback(ctx, "awaiting_tools", msgs)
			}
			if err := spec.Hook.BeforeExecuteTools(ctx, hctx); err != nil {
				return result, err
			}

			results, fatal := r.executeTools(ctx, spec, llmResp.ToolCalls)
			hctx.ToolResults = append(hctx.ToolResults, results...)
			result.ToolEvents = append(result.ToolEvents, results...)
			for _, tr := range results {
				result.ToolsUsed = append(result.ToolsUsed, tr.Name)
				msgs = ctxbuilder.AppendToolResult(msgs, tr.CallID, tr.Name, truncate(tr.Output, spec.MaxToolResultChars))
			}
			if fatal != nil && spec.FailOnToolError {
				result.Error = fatal
				result.StopReason = "error"
				return result, fatal
			}

			if spec.CheckpointCallback != nil {
				spec.CheckpointCallback(ctx, "tools_completed", msgs)
			}

			// drain injections
			if spec.InjectionCallback != nil {
				if injected := drainInjections(ctx, spec.InjectionCallback); len(injected) > 0 {
					msgs = append(msgs, injected...)
					hadInjections = true
				}
			}

			if err := spec.Hook.AfterIteration(ctx, hctx); err != nil {
				return result, err
			}
			continue
		}

		// No tool calls — try to finalize content.
		final := llmResp.Content
		if final == "" && emptyRetries < MaxEmptyRetries {
			emptyRetries++
			continue
		}
		if llmResp.FinishReason == "length" && lengthRecoveries < MaxLengthRecoveries {
			lengthRecoveries++
			msgs = append(msgs, provider.Message{Role: "user", Content: jsonString("Continue. The previous answer was cut off.")})
			continue
		}

		final = spec.Hook.FinalizeContent(final)
		if err := spec.Hook.OnStreamEnd(ctx, hctx, false); err != nil {
			return result, err
		}
		result.FinalContent = final
		msgs = ctxbuilder.AppendAssistant(msgs, final, nil, llmResp.ThinkingBlocks)
		if spec.CheckpointCallback != nil {
			spec.CheckpointCallback(ctx, "final_response", msgs)
		}
		hctx.FinalContent = final
		hctx.StopReason = llmResp.FinishReason
		_ = spec.Hook.AfterIteration(ctx, hctx)
		result.StopReason = llmResp.FinishReason
		result.Messages = msgs
		result.HadInjections = hadInjections
		return result, nil
	}

	// exhausted iterations
	final := MaxIterationsMessage
	final = spec.Hook.FinalizeContent(final)
	result.FinalContent = final
	msgs = ctxbuilder.AppendAssistant(msgs, final, nil, nil)
	result.Messages = msgs
	result.StopReason = "max_iterations"
	result.HadInjections = hadInjections
	return result, nil
}

func (r *Runner) request(ctx context.Context, spec Spec, msgs []provider.Message, hctx *hook.Context) (*provider.LLMResponse, error) {
	req := provider.ChatRequest{
		Messages:        msgs,
		Model:           spec.Model,
		MaxTokens:       spec.MaxTokens,
		Temperature:     spec.Temperature,
		ReasoningEffort: spec.ReasoningEffort,
	}
	if spec.Registry != nil {
		req.Tools = spec.Registry.Definitions()
	}
	if spec.Hook.WantsStreaming() {
		return provider.ChatStreamWithRetry(ctx, r.Provider, req, func(delta string) {
			_ = spec.Hook.OnStream(ctx, hctx, delta)
		}, spec.RetryPolicy)
	}
	return provider.ChatWithRetry(ctx, r.Provider, req, spec.RetryPolicy)
}

func (r *Runner) executeTools(ctx context.Context, spec Spec, calls []provider.ToolCallRequest) ([]hook.ToolResult, error) {
	if spec.Registry == nil {
		return nil, errors.New("no registry")
	}
	results := make([]hook.ToolResult, len(calls))
	var firstErr error
	if !spec.ConcurrentTools {
		for i, call := range calls {
			results[i] = runOne(ctx, spec, call)
			if results[i].Err != nil && firstErr == nil {
				firstErr = results[i].Err
			}
		}
		return results, firstErr
	}
	var wg sync.WaitGroup
	for i, call := range calls {
		if t := spec.Registry.Get(call.Name); t != nil && !t.ConcurrencySafe() {
			results[i] = runOne(ctx, spec, call)
			if results[i].Err != nil && firstErr == nil {
				firstErr = results[i].Err
			}
			continue
		}
		wg.Add(1)
		go func(i int, call provider.ToolCallRequest) {
			defer wg.Done()
			results[i] = runOne(ctx, spec, call)
		}(i, call)
	}
	wg.Wait()
	for _, r := range results {
		if r.Err != nil && firstErr == nil {
			firstErr = r.Err
		}
	}
	return results, firstErr
}

func runOne(ctx context.Context, spec Spec, call provider.ToolCallRequest) hook.ToolResult {
	start := time.Now()
	args, err := call.ArgumentsMap()
	if err != nil {
		return hook.ToolResult{CallID: call.ID, Name: call.Name, Output: "invalid arguments: " + err.Error(), Err: err}
	}
	out, err := spec.Registry.Execute(ctx, call.Name, args)
	if err != nil {
		out = "Tool error: " + err.Error()
	}
	return hook.ToolResult{
		CallID:   call.ID,
		Name:     call.Name,
		Output:   out,
		Err:      err,
		Duration: time.Since(start).Milliseconds(),
	}
}

func toolCallsToMessage(calls []provider.ToolCallRequest) []provider.ToolCall {
	out := make([]provider.ToolCall, 0, len(calls))
	for _, c := range calls {
		out = append(out, provider.ToolCall{
			ID:   c.ID,
			Type: "function",
			Function: provider.ToolCallFunction{
				Name:      c.Name,
				Arguments: string(c.Arguments),
			},
		})
	}
	return out
}

func drainInjections(ctx context.Context, cb func(context.Context) []provider.Message) []provider.Message {
	injected := make([]provider.Message, 0)
	for cycle := 0; cycle < MaxInjectionCycles; cycle++ {
		batch := cb(ctx)
		if len(batch) == 0 {
			break
		}
		if len(batch) > MaxInjectionsPerTurn {
			batch = batch[:MaxInjectionsPerTurn]
		}
		injected = append(injected, batch...)
	}
	return injected
}

func truncate(s string, limit int) string {
	if limit <= 0 || len(s) <= limit {
		return s
	}
	return s[:limit] + fmt.Sprintf("\n\n[truncated at %d chars]", limit)
}

func jsonString(s string) json.RawMessage {
	b, _ := json.Marshal(s)
	return b
}

func addUsage(a, b provider.Usage) provider.Usage {
	return provider.Usage{
		PromptTokens:     a.PromptTokens + b.PromptTokens,
		CompletionTokens: a.CompletionTokens + b.CompletionTokens,
		TotalTokens:      a.TotalTokens + b.TotalTokens,
	}
}

func defaultIf(a, fallback string) string {
	if a != "" {
		return a
	}
	return fallback
}
