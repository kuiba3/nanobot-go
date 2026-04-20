// Package memory implements the workspace-backed long-term memory store.
// Files:
//   memory/MEMORY.md           long-term facts
//   memory/history.jsonl       append-only conversation summaries
//   memory/.cursor             next available cursor integer
//   memory/.dream_cursor       cursor the Dream process has archived to
package memory

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Entry is a single JSONL record.
type Entry struct {
	Cursor    int    `json:"cursor"`
	Timestamp string `json:"timestamp"`
	Content   string `json:"content"`
}

// Store is a process-local wrapper around workspace memory files.
type Store struct {
	workspace string
	mu        sync.Mutex
}

// NewStore returns a Store rooted at workspace/memory.
func NewStore(workspace string) *Store {
	return &Store{workspace: workspace}
}

func (s *Store) path(name string) string {
	return filepath.Join(s.workspace, "memory", name)
}

// EnsureLayout creates the memory subdirectory if missing.
func (s *Store) EnsureLayout() error {
	return os.MkdirAll(filepath.Join(s.workspace, "memory"), 0o755)
}

// ReadMemory reads MEMORY.md, returning "" if absent.
func (s *Store) ReadMemory() string {
	data, err := os.ReadFile(s.path("MEMORY.md"))
	if err != nil {
		return ""
	}
	return string(data)
}

// WriteMemory overwrites MEMORY.md.
func (s *Store) WriteMemory(content string) error {
	if err := s.EnsureLayout(); err != nil {
		return err
	}
	return os.WriteFile(s.path("MEMORY.md"), []byte(content), 0o600)
}

// MemoryContext returns "## Long-term Memory\n\n<content>" or "" if empty.
func (s *Store) MemoryContext() string {
	c := strings.TrimSpace(s.ReadMemory())
	if c == "" {
		return ""
	}
	return "## Long-term Memory\n\n" + c
}

// AppendHistory appends a new JSONL entry. Returns the assigned cursor.
func (s *Store) AppendHistory(content string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.EnsureLayout(); err != nil {
		return 0, err
	}
	cursor, err := s.readCursorLocked()
	if err != nil {
		return 0, err
	}
	entry := Entry{Cursor: cursor, Timestamp: time.Now().UTC().Format(time.RFC3339), Content: content}
	f, err := os.OpenFile(s.path("history.jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return 0, err
	}
	b, err := json.Marshal(entry)
	if err != nil {
		_ = f.Close()
		return 0, err
	}
	if _, err := f.Write(append(b, '\n')); err != nil {
		_ = f.Close()
		return 0, err
	}
	if err := f.Close(); err != nil {
		return 0, err
	}
	if err := s.writeCursorLocked(cursor + 1); err != nil {
		return 0, err
	}
	return cursor, nil
}

// ReadUnprocessedHistory returns entries with cursor > since.
func (s *Store) ReadUnprocessedHistory(since int) ([]Entry, error) {
	f, err := os.Open(s.path("history.jsonl"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	var out []Entry
	for sc.Scan() {
		if len(sc.Bytes()) == 0 {
			continue
		}
		var e Entry
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			continue
		}
		if e.Cursor > since {
			out = append(out, e)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// LastDreamCursor returns the highest cursor that Dream has processed.
func (s *Store) LastDreamCursor() int {
	data, err := os.ReadFile(s.path(".dream_cursor"))
	if err != nil {
		return 0
	}
	n, _ := strconv.Atoi(strings.TrimSpace(string(data)))
	return n
}

// SetDreamCursor advances the dream cursor.
func (s *Store) SetDreamCursor(c int) error {
	if err := s.EnsureLayout(); err != nil {
		return err
	}
	return os.WriteFile(s.path(".dream_cursor"), []byte(strconv.Itoa(c)), 0o600)
}

// CompactHistory keeps only the newest n entries. Returns number removed.
func (s *Store) CompactHistory(maxEntries int) (int, error) {
	if maxEntries <= 0 {
		maxEntries = 1000
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	path := s.path("history.jsonl")
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0, nil
		}
		return 0, err
	}
	var entries []Entry
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		var e Entry
		if err := json.Unmarshal(sc.Bytes(), &e); err == nil {
			entries = append(entries, e)
		}
	}
	f.Close()
	if err := sc.Err(); err != nil {
		return 0, err
	}
	if len(entries) <= maxEntries {
		return 0, nil
	}
	drop := len(entries) - maxEntries
	entries = entries[drop:]
	tmp := path + ".tmp"
	out, err := os.Create(tmp)
	if err != nil {
		return 0, err
	}
	bw := bufio.NewWriter(out)
	for _, e := range entries {
		b, _ := json.Marshal(e)
		if _, err := bw.Write(append(b, '\n')); err != nil {
			_ = out.Close()
			return 0, err
		}
	}
	if err := bw.Flush(); err != nil {
		_ = out.Close()
		return 0, err
	}
	if err := out.Close(); err != nil {
		return 0, err
	}
	if err := os.Rename(tmp, path); err != nil {
		return 0, err
	}
	return drop, nil
}

func (s *Store) readCursorLocked() (int, error) {
	data, err := os.ReadFile(s.path(".cursor"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 1, nil
		}
		return 0, err
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 1, nil
	}
	if n <= 0 {
		return 1, nil
	}
	return n, nil
}

func (s *Store) writeCursorLocked(c int) error {
	return os.WriteFile(s.path(".cursor"), []byte(strconv.Itoa(c)), 0o600)
}

// CopyAtomic is used by Dream/GitStore to take a snapshot. Not used in MVP
// runtime but exposed for tests.
func CopyAtomic(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	tmp := dst + ".tmp"
	out, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, dst)
}

// Describe returns a short summary of the store paths for diagnostics.
func (s *Store) Describe() string {
	return fmt.Sprintf("memory@%s", filepath.Join(s.workspace, "memory"))
}
