package session

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"time"
)

// Manager is a workspace-scoped session store persisting each session to
// workspace/sessions/<safe_key>.jsonl. The first line is a metadata envelope,
// subsequent lines are individual messages.
type Manager struct {
	dir   string
	mu    sync.Mutex
	cache map[string]*Session
}

// NewManager returns a Manager rooted under workspace/sessions.
func NewManager(workspace string) *Manager {
	return &Manager{
		dir:   filepath.Join(workspace, "sessions"),
		cache: make(map[string]*Session),
	}
}

// Dir returns the sessions directory.
func (m *Manager) Dir() string { return m.dir }

// metadataEnvelope is the first JSONL line.
type metadataEnvelope struct {
	Kind             string    `json:"_kind"` // always "metadata"
	Key              string    `json:"key"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
	LastConsolidated int       `json:"last_consolidated"`
	Metadata         map[string]any `json:"metadata"`
}

var safePathRe = regexp.MustCompile(`[^A-Za-z0-9._-]`)

func (m *Manager) pathFor(key string) string {
	safe := safePathRe.ReplaceAllString(key, "_")
	return filepath.Join(m.dir, safe+".jsonl")
}

// GetOrCreate returns the session for key, loading from disk or creating empty.
func (m *Manager) GetOrCreate(key string) (*Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s, ok := m.cache[key]; ok {
		return s, nil
	}
	s, err := m.loadLocked(key)
	if err != nil {
		return nil, err
	}
	if s == nil {
		now := time.Now().UTC()
		s = &Session{
			Key:       key,
			CreatedAt: now,
			UpdatedAt: now,
			Metadata:  map[string]any{},
		}
	}
	m.cache[key] = s
	return s, nil
}

// Get returns an in-memory view without loading from disk if missing. nil if absent.
func (m *Manager) Get(key string) *Session {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.cache[key]
}

// Invalidate drops the cache entry so next GetOrCreate reloads from disk.
func (m *Manager) Invalidate(key string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.cache, key)
}

// Delete removes the on-disk file and cache entry.
func (m *Manager) Delete(key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.cache, key)
	err := os.Remove(m.pathFor(key))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// Save writes the session atomically to disk.
func (m *Manager) Save(s *Session) error {
	if s == nil {
		return errors.New("save nil session")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.saveLocked(s)
}

func (m *Manager) saveLocked(s *Session) error {
	if err := os.MkdirAll(m.dir, 0o755); err != nil {
		return err
	}
	tmp := m.pathFor(s.Key) + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	bw := bufio.NewWriter(f)
	env := metadataEnvelope{
		Kind:             "metadata",
		Key:              s.Key,
		CreatedAt:        s.CreatedAt,
		UpdatedAt:        s.UpdatedAt,
		LastConsolidated: s.LastConsolidated,
		Metadata:         s.Metadata,
	}
	if err := writeJSONLine(bw, env); err != nil {
		_ = f.Close()
		return err
	}
	for _, msg := range s.Messages {
		if err := writeJSONLine(bw, msg); err != nil {
			_ = f.Close()
			return err
		}
	}
	if err := bw.Flush(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, m.pathFor(s.Key))
}

func writeJSONLine(w io.Writer, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if _, err := w.Write(b); err != nil {
		return err
	}
	_, err = w.Write([]byte{'\n'})
	return err
}

func (m *Manager) loadLocked(key string) (*Session, error) {
	path := m.pathFor(key)
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	first := true
	s := &Session{Key: key, Metadata: map[string]any{}}
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		if first {
			first = false
			var env metadataEnvelope
			if err := json.Unmarshal(line, &env); err == nil && env.Kind == "metadata" {
				s.CreatedAt = env.CreatedAt
				s.UpdatedAt = env.UpdatedAt
				s.LastConsolidated = env.LastConsolidated
				if env.Metadata != nil {
					s.Metadata = env.Metadata
				}
				continue
			}
			// No metadata header; treat first line as a message.
		}
		var msg Message
		if err := json.Unmarshal(line, &msg); err != nil {
			return nil, fmt.Errorf("parse session line: %w", err)
		}
		s.Messages = append(s.Messages, msg)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return s, nil
}

// ListSessions returns metadata snapshots of every session on disk. It does
// not load full message lists.
func (m *Manager) ListSessions() ([]SessionInfo, error) {
	entries, err := os.ReadDir(m.dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	out := make([]SessionInfo, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".jsonl" {
			continue
		}
		info, err := readHeader(filepath.Join(m.dir, e.Name()))
		if err != nil {
			continue
		}
		out = append(out, info)
	}
	return out, nil
}

// SessionInfo is a thin metadata view used for listing.
type SessionInfo struct {
	Key       string
	UpdatedAt time.Time
	CreatedAt time.Time
}

func readHeader(path string) (SessionInfo, error) {
	f, err := os.Open(path)
	if err != nil {
		return SessionInfo{}, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 4*1024), 64*1024)
	if !sc.Scan() {
		return SessionInfo{}, io.EOF
	}
	var env metadataEnvelope
	if err := json.Unmarshal(sc.Bytes(), &env); err != nil {
		return SessionInfo{}, err
	}
	return SessionInfo{Key: env.Key, UpdatedAt: env.UpdatedAt, CreatedAt: env.CreatedAt}, nil
}
