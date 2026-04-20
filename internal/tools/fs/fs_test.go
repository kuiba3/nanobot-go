package fs

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/hkuds/nanobot-go/internal/security"
)

func TestReadWriteEdit(t *testing.T) {
	ws := t.TempDir()
	sb := security.NewPathSandbox(ws, nil)
	ts := New(sb)
	var read, write, edit, list = ts[0], ts[1], ts[2], ts[3]

	p := filepath.Join(ws, "a.txt")
	if _, err := write.Execute(context.Background(), map[string]any{"path": p, "content": "hello"}); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := read.Execute(context.Background(), map[string]any{"path": p})
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got != "hello" {
		t.Fatalf("read got %q", got)
	}
	if _, err := edit.Execute(context.Background(), map[string]any{"path": p, "old_string": "hello", "new_string": "world"}); err != nil {
		t.Fatalf("edit: %v", err)
	}
	b, _ := os.ReadFile(p)
	if string(b) != "world" {
		t.Fatalf("edit result: %q", string(b))
	}
	listed, err := list.Execute(context.Background(), map[string]any{"path": ws})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if listed == "" {
		t.Fatalf("expected entries")
	}
}

func TestSandboxReject(t *testing.T) {
	ws := t.TempDir()
	sb := security.NewPathSandbox(ws, nil)
	tools := New(sb)
	read := tools[0]
	if _, err := read.Execute(context.Background(), map[string]any{"path": "/etc/passwd"}); err == nil {
		t.Fatal("expected sandbox rejection")
	}
}
