package memory

import (
	"path/filepath"
	"testing"
)

func TestAppendAndReadHistory(t *testing.T) {
	ws := t.TempDir()
	s := NewStore(ws)
	for i := 0; i < 3; i++ {
		if _, err := s.AppendHistory("entry"); err != nil {
			t.Fatalf("append: %v", err)
		}
	}
	entries, err := s.ReadUnprocessedHistory(0)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(entries))
	}
	if entries[0].Cursor != 1 || entries[2].Cursor != 3 {
		t.Fatalf("cursor sequence: %+v", entries)
	}
}

func TestDreamCursor(t *testing.T) {
	ws := t.TempDir()
	s := NewStore(ws)
	if s.LastDreamCursor() != 0 {
		t.Fatalf("expected 0 when missing")
	}
	if err := s.SetDreamCursor(42); err != nil {
		t.Fatalf("set: %v", err)
	}
	if s.LastDreamCursor() != 42 {
		t.Fatalf("expected 42, got %d", s.LastDreamCursor())
	}
}

func TestCompactHistory(t *testing.T) {
	ws := t.TempDir()
	s := NewStore(ws)
	for i := 0; i < 10; i++ {
		_, _ = s.AppendHistory("x")
	}
	removed, err := s.CompactHistory(5)
	if err != nil {
		t.Fatalf("compact: %v", err)
	}
	if removed != 5 {
		t.Fatalf("expected 5 removed, got %d", removed)
	}
	entries, _ := s.ReadUnprocessedHistory(0)
	if len(entries) != 5 {
		t.Fatalf("expected 5 remaining, got %d", len(entries))
	}
	_ = filepath.Join(ws, "memory")
}

func TestMemoryContext(t *testing.T) {
	ws := t.TempDir()
	s := NewStore(ws)
	if got := s.MemoryContext(); got != "" {
		t.Fatalf("expected empty when missing, got %q", got)
	}
	_ = s.WriteMemory("knows Go")
	got := s.MemoryContext()
	if got == "" || !containsAll(got, "Long-term Memory", "knows Go") {
		t.Fatalf("unexpected: %q", got)
	}
}

func containsAll(s string, subs ...string) bool {
	for _, sub := range subs {
		if !contains(s, sub) {
			return false
		}
	}
	return true
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (stringIndex(s, sub) >= 0)
}

func stringIndex(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
