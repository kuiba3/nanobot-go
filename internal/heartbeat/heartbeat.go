// Package heartbeat runs a periodic tick that reads HEARTBEAT.md from the
// workspace and, when non-empty, asks the agent to process it. Mirrors Python
// nanobot/heartbeat/service.py (minus the LLM gate — we just fire the turn).
package heartbeat

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/hkuds/nanobot-go/internal/bus"
	"github.com/hkuds/nanobot-go/internal/loop"
)

// Service is the heartbeat runner.
type Service struct {
	workspace string
	interval  time.Duration
	loop      *loop.Loop

	stop chan struct{}
}

// New constructs a Service.
func New(workspace string, intervalS int, l *loop.Loop) *Service {
	if intervalS <= 0 {
		intervalS = 1800
	}
	return &Service{workspace: workspace, interval: time.Duration(intervalS) * time.Second, loop: l, stop: make(chan struct{})}
}

// Start begins the heartbeat loop.
func (s *Service) Start(ctx context.Context) {
	go s.run(ctx)
}

// Stop halts the loop.
func (s *Service) Stop() { close(s.stop) }

func (s *Service) run(ctx context.Context) {
	t := time.NewTicker(s.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.stop:
			return
		case <-t.C:
			s.tick(ctx)
		}
	}
}

func (s *Service) tick(ctx context.Context) {
	data, err := os.ReadFile(filepath.Join(s.workspace, "HEARTBEAT.md"))
	if err != nil {
		return
	}
	text := strings.TrimSpace(string(data))
	if text == "" {
		return
	}
	tctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	if _, err := s.loop.ProcessDirect(tctx, bus.InboundMessage{
		Channel:            "heartbeat",
		ChatID:             "direct",
		Content:            text,
		SessionKeyOverride: "heartbeat",
	}); err != nil {
		log.Printf("heartbeat turn: %v", err)
	}
}
