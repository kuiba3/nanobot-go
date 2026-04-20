package bus

import (
	"context"
	"testing"
	"time"
)

func TestPublishConsumeInbound(t *testing.T) {
	b := New(4)
	defer b.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := b.PublishInbound(ctx, InboundMessage{Channel: "cli", ChatID: "a", Content: "hi"}); err != nil {
		t.Fatalf("publish: %v", err)
	}
	select {
	case m := <-b.ConsumeInbound():
		if m.SessionKey() != "cli:a" || m.Content != "hi" {
			t.Fatalf("unexpected message: %+v", m)
		}
	case <-time.After(time.Second):
		t.Fatal("no message received")
	}
}

func TestSessionKeyOverride(t *testing.T) {
	m := InboundMessage{Channel: "cli", ChatID: "a", SessionKeyOverride: "unified:default"}
	if got := m.SessionKey(); got != "unified:default" {
		t.Fatalf("expected override, got %q", got)
	}
}
