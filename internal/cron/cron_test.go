package cron

import (
	"testing"
	"time"
)

func TestParseAt(t *testing.T) {
	s, err := Parse("at 2030-01-01T09:00:00Z")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if s.Kind != "at" {
		t.Fatalf("kind=%s", s.Kind)
	}
}

func TestParseEvery(t *testing.T) {
	s, err := Parse("every 15m")
	if err != nil {
		t.Fatal(err)
	}
	if s.Kind != "every" || s.Interval != 15*time.Minute {
		t.Fatalf("every: %+v", s)
	}
}

func TestParseCron(t *testing.T) {
	s, err := Parse("cron 0 9 * * 1")
	if err != nil {
		t.Fatal(err)
	}
	if s.Kind != "cron" {
		t.Fatalf("kind=%s", s.Kind)
	}
	// Monday 09:00
	now := time.Date(2026, 4, 19, 12, 0, 0, 0, time.UTC) // Sunday
	next := nextRun(s, now)
	if next.Hour() != 9 || next.Weekday() != time.Monday {
		t.Fatalf("nextRun: %v", next)
	}
}

func TestAddCancel(t *testing.T) {
	dir := t.TempDir() + "/cron"
	svc := New(dir+"/jobs.json", nil)
	id, err := svc.Add("every 1h", "reminder", "ping", "cli", "default")
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if len(svc.List()) != 1 {
		t.Fatalf("expected 1 job")
	}
	if err := svc.Cancel(id); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if len(svc.List()) != 0 {
		t.Fatalf("expected 0 jobs")
	}
}
