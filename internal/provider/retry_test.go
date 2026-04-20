package provider

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestRunWithRetry_Success(t *testing.T) {
	pol := RetryPolicy{Backoff: []float64{0.01}, MaxAttempts: 3}
	ctx := context.Background()
	calls := 0
	resp, err := pol.RunWithRetry(ctx, func(ctx context.Context) (*LLMResponse, error) {
		calls++
		return &LLMResponse{Content: "ok", FinishReason: "stop"}, nil
	})
	if err != nil || resp.Content != "ok" || calls != 1 {
		t.Fatalf("unexpected: calls=%d resp=%+v err=%v", calls, resp, err)
	}
}

func TestRunWithRetry_Transient(t *testing.T) {
	pol := RetryPolicy{Backoff: []float64{0.001, 0.001}, MaxAttempts: 3}
	calls := 0
	resp, err := pol.RunWithRetry(context.Background(), func(ctx context.Context) (*LLMResponse, error) {
		calls++
		if calls < 3 {
			return nil, errors.New("timeout while reading")
		}
		return &LLMResponse{Content: "ok", FinishReason: "stop"}, nil
	})
	if err != nil || resp == nil || calls != 3 {
		t.Fatalf("expected 3 calls, got %d err=%v", calls, err)
	}
}

func TestRunWithRetry_StopOnNonTransient(t *testing.T) {
	pol := RetryPolicy{Backoff: []float64{0.001}, MaxAttempts: 3}
	calls := 0
	_, err := pol.RunWithRetry(context.Background(), func(ctx context.Context) (*LLMResponse, error) {
		calls++
		return nil, errors.New("invalid api key")
	})
	if err == nil || calls != 1 {
		t.Fatalf("expected single call non-retryable, got calls=%d err=%v", calls, err)
	}
}

func TestNextWaitRetryAfter(t *testing.T) {
	pol := Defaults()
	resp := &LLMResponse{RetryAfter: 2.5}
	w := pol.NextWait(0, resp)
	if w != 2500*time.Millisecond {
		t.Fatalf("expected 2.5s, got %v", w)
	}
}
