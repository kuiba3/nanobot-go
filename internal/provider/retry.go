package provider

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"
)

// RetryPolicy captures the retry knobs shared by all providers. Mirrors
// the Python _run_with_retry contract.
type RetryPolicy struct {
	// Mode is "default" (finite backoff, fail after attempts) or "persistent"
	// (back off up to 60s forever). Empty == "default".
	Mode string
	// MaxAttempts for "default" mode. 0 means len(Backoff)+1 (i.e. 4 tries).
	MaxAttempts int
	// Backoff is the base sleep sequence in seconds. Defaults to (1, 2, 4).
	Backoff []float64
	// CapSeconds is the per-wait cap used by "persistent" mode.
	CapSeconds float64
	// OnWait is called before each sleep with the wait duration so UI layers
	// can surface "retrying in Xs" hints.
	OnWait func(attempt int, d time.Duration)
}

// Defaults returns the default retry policy.
func Defaults() RetryPolicy {
	return RetryPolicy{
		Mode:        "default",
		Backoff:     []float64{1, 2, 4},
		CapSeconds:  60,
		MaxAttempts: 4,
	}
}

// ShouldRetry classifies a response/error pair. A nil error still produces
// retry=true when the response describes a transient error.
func ShouldRetry(resp *LLMResponse, err error) bool {
	if err != nil {
		return classifyError(err)
	}
	if resp == nil {
		return false
	}
	if resp.FinishReason == "error" && resp.ErrorRetryable {
		return true
	}
	if resp.ErrorStatusCode == 429 || resp.ErrorStatusCode == 502 || resp.ErrorStatusCode == 503 || resp.ErrorStatusCode == 504 {
		return true
	}
	switch resp.ErrorKind {
	case "timeout", "connection", "rate_limit":
		return true
	}
	return false
}

// classifyError returns true for obvious transient wire-level errors.
func classifyError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	msg := strings.ToLower(err.Error())
	for _, kw := range []string{"timeout", "temporarily", "connection reset", "i/o timeout", "eof", "broken pipe", "no such host", "rate limit"} {
		if strings.Contains(msg, kw) {
			return true
		}
	}
	return false
}

// NextWait returns the wait duration for the given 0-based attempt number.
// Respects RetryAfter (from the response) if > 0.
func (p RetryPolicy) NextWait(attempt int, resp *LLMResponse) time.Duration {
	base := 0.0
	if resp != nil && resp.RetryAfter > 0 {
		base = resp.RetryAfter
	} else {
		if len(p.Backoff) == 0 {
			base = math.Pow(2, float64(attempt))
		} else if attempt < len(p.Backoff) {
			base = p.Backoff[attempt]
		} else {
			base = p.Backoff[len(p.Backoff)-1]
		}
	}
	if p.Mode == "persistent" {
		if p.CapSeconds > 0 && base > p.CapSeconds {
			base = p.CapSeconds
		}
	}
	return time.Duration(base * float64(time.Second))
}

// MaxTries returns the maximum number of attempts allowed by the policy.
// Persistent mode returns math.MaxInt (caller should still bound by ctx).
func (p RetryPolicy) MaxTries() int {
	if p.Mode == "persistent" {
		return math.MaxInt32
	}
	if p.MaxAttempts > 0 {
		return p.MaxAttempts
	}
	return len(p.Backoff) + 1
}

// RunWithRetry runs fn up to the configured number of attempts, retrying on
// transient classification. It returns the last response/error pair.
func (p RetryPolicy) RunWithRetry(ctx context.Context, fn func(ctx context.Context) (*LLMResponse, error)) (*LLMResponse, error) {
	var (
		lastResp *LLMResponse
		lastErr  error
	)
	tries := p.MaxTries()
	for attempt := 0; attempt < tries; attempt++ {
		if err := ctx.Err(); err != nil {
			return lastResp, err
		}
		resp, err := fn(ctx)
		lastResp, lastErr = resp, err
		if !ShouldRetry(resp, err) {
			return resp, err
		}
		if attempt == tries-1 {
			break
		}
		wait := p.NextWait(attempt, resp)
		if p.OnWait != nil {
			p.OnWait(attempt, wait)
		}
		select {
		case <-ctx.Done():
			return lastResp, ctx.Err()
		case <-time.After(wait):
		}
	}
	if lastErr != nil {
		return lastResp, lastErr
	}
	if lastResp != nil && lastResp.FinishReason == "error" {
		return lastResp, fmt.Errorf("%s", lastResp.ErrorMessage)
	}
	return lastResp, nil
}

// ChatWithRetry is a convenience wrapper.
func ChatWithRetry(ctx context.Context, p Provider, req ChatRequest, pol RetryPolicy) (*LLMResponse, error) {
	return pol.RunWithRetry(ctx, func(ctx context.Context) (*LLMResponse, error) {
		return p.Chat(ctx, req)
	})
}

// ChatStreamWithRetry runs the stream call with retry; the callback is called
// for each attempt's deltas, even if an earlier attempt produced none.
func ChatStreamWithRetry(ctx context.Context, p Provider, req ChatRequest, onDelta func(string), pol RetryPolicy) (*LLMResponse, error) {
	return pol.RunWithRetry(ctx, func(ctx context.Context) (*LLMResponse, error) {
		return p.ChatStream(ctx, req, onDelta)
	})
}
