package session

import (
	"testing"
)

func TestSessionRoundTrip(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(dir)
	s, err := m.GetOrCreate("cli:default")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	s.AddMessage(Message{Role: RoleUser, Content: StringContent("hi")})
	s.AddMessage(Message{Role: RoleAssistant, Content: StringContent("hello!")})
	if err := m.Save(s); err != nil {
		t.Fatalf("save: %v", err)
	}

	m2 := NewManager(dir)
	got, err := m2.GetOrCreate("cli:default")
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if len(got.Messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(got.Messages))
	}
	if got.Messages[0].TextOf() != "hi" {
		t.Fatalf("expected text 'hi', got %q", got.Messages[0].TextOf())
	}
}

func TestRetainRecentLegalSuffix(t *testing.T) {
	s := &Session{Key: "k"}
	s.Messages = []Message{
		{Role: RoleUser, Content: StringContent("m1")},
		{Role: RoleAssistant, Content: StringContent("r1"), ToolCalls: []ToolCall{{ID: "c1"}}},
		{Role: RoleTool, ToolCallID: "c1", Content: StringContent("result")},
		{Role: RoleUser, Content: StringContent("m2")},
		{Role: RoleAssistant, Content: StringContent("r2")},
	}
	s.RetainRecentLegalSuffix(3)
	if len(s.Messages) != 2 {
		t.Fatalf("expected 2 remaining (user+assistant), got %d", len(s.Messages))
	}
	if s.Messages[0].Role != RoleUser {
		t.Fatalf("expected leading user, got %s", s.Messages[0].Role)
	}
}
