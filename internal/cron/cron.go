// Package cron implements a lightweight persistent scheduler backed by a
// workspace JSON file. It supports three schedule kinds:
//
//	at <RFC3339>           — one-shot
//	every <duration>        — recurring interval
//	cron <m h dom mon dow>  — classic 5-field cron
//
// Minimal implementation without external dependencies; good enough for MVP.
package cron

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Schedule is the parsed schedule.
type Schedule struct {
	Kind     string    // "at" | "every" | "cron"
	RunAt    time.Time // for "at"
	Interval time.Duration // for "every"
	Cron     []cronField   // for "cron" (m,h,dom,mon,dow)
}

type cronField struct {
	star bool
	vals map[int]struct{}
}

// Job is a persisted cron job.
type Job struct {
	ID       string    `json:"id"`
	Name     string    `json:"name"`
	When     string    `json:"when"`
	Message  string    `json:"message"`
	Channel  string    `json:"channel"`
	ChatID   string    `json:"chat_id"`
	Next     time.Time `json:"next"`
	LastRun  time.Time `json:"last_run,omitempty"`
}

// Service owns the scheduler.
type Service struct {
	path      string
	mu        sync.Mutex
	jobs      map[string]*Job
	ticker    *time.Ticker
	stop      chan struct{}
	nextID    int
	onJob     func(ctx context.Context, j Job)
}

// New constructs a Service. jobsFile is typically
// <workspace>/cron/jobs.json.
func New(jobsFile string, onJob func(ctx context.Context, j Job)) *Service {
	return &Service{path: jobsFile, jobs: make(map[string]*Job), stop: make(chan struct{}), onJob: onJob}
}

// Load reads jobs from disk.
func (s *Service) Load() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var doc struct {
		Version int    `json:"version"`
		Jobs    []*Job `json:"jobs"`
		NextID  int    `json:"next_id"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return err
	}
	for _, j := range doc.Jobs {
		s.jobs[j.ID] = j
	}
	s.nextID = doc.NextID
	return nil
}

func (s *Service) saveLocked() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	arr := make([]*Job, 0, len(s.jobs))
	for _, j := range s.jobs {
		arr = append(arr, j)
	}
	doc := map[string]any{
		"version": 1,
		"next_id": s.nextID,
		"jobs":    arr,
	}
	data, _ := json.MarshalIndent(doc, "", "  ")
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// Start begins the scheduler loop.
func (s *Service) Start(ctx context.Context) {
	s.ticker = time.NewTicker(15 * time.Second)
	go s.loop(ctx)
}

// Stop halts the scheduler.
func (s *Service) Stop() {
	if s.ticker != nil {
		s.ticker.Stop()
	}
	close(s.stop)
}

func (s *Service) loop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.stop:
			return
		case now := <-s.ticker.C:
			s.tick(ctx, now)
		}
	}
}

func (s *Service) tick(ctx context.Context, now time.Time) {
	s.mu.Lock()
	due := make([]*Job, 0)
	for _, j := range s.jobs {
		if !j.Next.IsZero() && !now.Before(j.Next) {
			due = append(due, j)
		}
	}
	s.mu.Unlock()
	for _, j := range due {
		if s.onJob != nil {
			s.onJob(ctx, *j)
		}
		s.mu.Lock()
		j.LastRun = now
		// compute next run
		sc, err := Parse(j.When)
		if err == nil {
			j.Next = nextRun(sc, now)
			if sc.Kind == "at" {
				// one-shot: remove
				delete(s.jobs, j.ID)
			}
		} else {
			delete(s.jobs, j.ID)
		}
		_ = s.saveLocked()
		s.mu.Unlock()
	}
}

// Add registers a new job.
func (s *Service) Add(when, name, message, channel, chat string) (string, error) {
	sc, err := Parse(when)
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextID++
	id := fmt.Sprintf("job_%d", s.nextID)
	j := &Job{ID: id, When: when, Name: name, Message: message, Channel: channel, ChatID: chat}
	j.Next = nextRun(sc, time.Now())
	s.jobs[id] = j
	if err := s.saveLocked(); err != nil {
		delete(s.jobs, id)
		return "", err
	}
	return id, nil
}

// Cancel removes a job.
func (s *Service) Cancel(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.jobs[id]; !ok {
		return fmt.Errorf("no such job %q", id)
	}
	delete(s.jobs, id)
	return s.saveLocked()
}

// List returns a snapshot of jobs.
func (s *Service) List() []Job {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Job, 0, len(s.jobs))
	for _, j := range s.jobs {
		out = append(out, *j)
	}
	return out
}

// Parse decodes the user-facing schedule string.
func Parse(w string) (*Schedule, error) {
	w = strings.TrimSpace(w)
	fields := strings.SplitN(w, " ", 2)
	if len(fields) < 2 {
		return nil, fmt.Errorf("invalid schedule %q", w)
	}
	kind := strings.ToLower(fields[0])
	rest := strings.TrimSpace(fields[1])
	switch kind {
	case "at":
		t, err := time.Parse(time.RFC3339, rest)
		if err != nil {
			return nil, fmt.Errorf("at requires RFC3339 time: %w", err)
		}
		return &Schedule{Kind: "at", RunAt: t}, nil
	case "every":
		d, err := time.ParseDuration(rest)
		if err != nil {
			return nil, fmt.Errorf("every requires duration (e.g. 15m): %w", err)
		}
		return &Schedule{Kind: "every", Interval: d}, nil
	case "cron":
		fs, err := parseCronFields(rest)
		if err != nil {
			return nil, err
		}
		return &Schedule{Kind: "cron", Cron: fs}, nil
	}
	return nil, fmt.Errorf("unknown schedule kind %q", kind)
}

func parseCronFields(expr string) ([]cronField, error) {
	parts := strings.Fields(expr)
	if len(parts) != 5 {
		return nil, fmt.Errorf("cron expression must have 5 fields")
	}
	ranges := [][2]int{{0, 59}, {0, 23}, {1, 31}, {1, 12}, {0, 6}}
	out := make([]cronField, 5)
	for i, p := range parts {
		f, err := parseField(p, ranges[i][0], ranges[i][1])
		if err != nil {
			return nil, err
		}
		out[i] = f
	}
	return out, nil
}

func parseField(s string, lo, hi int) (cronField, error) {
	if s == "*" {
		return cronField{star: true}, nil
	}
	m := make(map[int]struct{})
	for _, part := range strings.Split(s, ",") {
		var step int = 1
		body := part
		if idx := strings.Index(part, "/"); idx > 0 {
			step, _ = strconv.Atoi(part[idx+1:])
			body = part[:idx]
		}
		a, b := lo, hi
		if body != "*" {
			if idx := strings.Index(body, "-"); idx > 0 {
				a, _ = strconv.Atoi(body[:idx])
				b, _ = strconv.Atoi(body[idx+1:])
			} else {
				n, err := strconv.Atoi(body)
				if err != nil {
					return cronField{}, err
				}
				a, b = n, n
			}
		}
		for v := a; v <= b; v += step {
			m[v] = struct{}{}
		}
	}
	return cronField{vals: m}, nil
}

func nextRun(s *Schedule, now time.Time) time.Time {
	switch s.Kind {
	case "at":
		if now.Before(s.RunAt) {
			return s.RunAt
		}
		return time.Time{}
	case "every":
		return now.Add(s.Interval)
	case "cron":
		t := now.Truncate(time.Minute).Add(time.Minute)
		for i := 0; i < 366*24*60; i++ {
			if cronMatch(s.Cron, t) {
				return t
			}
			t = t.Add(time.Minute)
		}
	}
	return time.Time{}
}

func cronMatch(fs []cronField, t time.Time) bool {
	fields := []int{t.Minute(), t.Hour(), t.Day(), int(t.Month()), int(t.Weekday())}
	for i, f := range fs {
		if f.star {
			continue
		}
		if _, ok := f.vals[fields[i]]; !ok {
			return false
		}
	}
	return true
}
