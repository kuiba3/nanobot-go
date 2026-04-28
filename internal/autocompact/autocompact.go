// Package autocompact archives idle sessions after a TTL. Mirrors Python
// agent/autocompact.py.
package autocompact

import (
	"context"
	"sync"
	"time"

	"github.com/kuiba3/nanobot-go/internal/consolidator"
	"github.com/kuiba3/nanobot-go/internal/session"
)

// AutoCompact is owned by the AgentLoop; it decides when to archive idle
// sessions and hands them to the Consolidator.
type AutoCompact struct {
	Sessions      *session.Manager
	Consolidator  *consolidator.Consolidator
	TTL           time.Duration
	RecentSuffix  int

	mu         sync.Mutex
	archiving  map[string]struct{}
	lastSummary map[string]string
}

// New constructs AutoCompact.
func New(sessions *session.Manager, cons *consolidator.Consolidator, ttlMinutes int) *AutoCompact {
	return &AutoCompact{
		Sessions:      sessions,
		Consolidator:  cons,
		TTL:           time.Duration(ttlMinutes) * time.Minute,
		RecentSuffix:  8,
		archiving:     make(map[string]struct{}),
		lastSummary:   make(map[string]string),
	}
}

// Enabled reports whether AutoCompact is active (TTL > 0).
func (a *AutoCompact) Enabled() bool { return a != nil && a.TTL > 0 }

// CheckExpired enqueues archival for sessions that are idle beyond TTL. Takes
// a set of session keys to skip because they have active turns.
func (a *AutoCompact) CheckExpired(ctx context.Context, active map[string]struct{}, schedule func(func())) {
	if !a.Enabled() {
		return
	}
	infos, err := a.Sessions.ListSessions()
	if err != nil {
		return
	}
	now := time.Now()
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, info := range infos {
		if _, ok := active[info.Key]; ok {
			continue
		}
		if _, busy := a.archiving[info.Key]; busy {
			continue
		}
		if info.UpdatedAt.IsZero() {
			continue
		}
		if now.Sub(info.UpdatedAt) < a.TTL {
			continue
		}
		a.archiving[info.Key] = struct{}{}
		key := info.Key
		schedule(func() {
			a.archiveKey(ctx, key)
		})
	}
}

func (a *AutoCompact) archiveKey(ctx context.Context, key string) {
	defer func() {
		a.mu.Lock()
		delete(a.archiving, key)
		a.mu.Unlock()
	}()
	a.Sessions.Invalidate(key)
	s, err := a.Sessions.GetOrCreate(key)
	if err != nil || s == nil {
		return
	}
	// snapshot into memory, trim session
	if a.Consolidator != nil {
		_ = a.Consolidator.MaybeArchive(ctx, s, 0, nil)
	}
	s.RetainRecentLegalSuffix(a.RecentSuffix)
	s.Metadata = withSummary(s.Metadata, "archived at "+time.Now().UTC().Format(time.RFC3339))
	_ = a.Sessions.Save(s)
	a.mu.Lock()
	a.lastSummary[key] = s.Metadata["_last_summary"].(string)
	a.mu.Unlock()
}

// PrepareSession returns (session, summary) to inject into runtime context.
// Called at the start of every turn.
func (a *AutoCompact) PrepareSession(s *session.Session) (*session.Session, string) {
	if s == nil {
		return s, ""
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	summary, _ := s.Metadata["_last_summary"].(string)
	if summary == "" {
		summary = a.lastSummary[s.Key]
	}
	return s, summary
}

func withSummary(meta map[string]any, summary string) map[string]any {
	if meta == nil {
		meta = make(map[string]any)
	}
	meta["_last_summary"] = summary
	return meta
}
