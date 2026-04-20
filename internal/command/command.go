// Package command implements slash-command parsing + builtin handlers.
package command

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/hkuds/nanobot-go/internal/bus"
)

// Handler processes a command and returns a reply string (empty to send nothing).
type Handler func(ctx context.Context, env DispatchEnv, args string) (string, error)

// DispatchEnv gives handlers access to the loop through a narrow interface.
type DispatchEnv struct {
	SessionKey string
	Message    bus.InboundMessage
	Loop       LoopHandle
}

// LoopHandle is the narrow set of loop operations that commands may perform.
type LoopHandle interface {
	ClearSession(key string) error
	Status() StatusInfo
	StopSession(key string) error
	Restart() error
}

// StatusInfo is what /status prints.
type StatusInfo struct {
	Workspace string
	Model     string
	Active    int
}

// Router holds registered command handlers.
type Router struct {
	mu        sync.RWMutex
	exact     map[string]Handler
	priority  map[string]Handler
}

// NewRouter builds an empty router.
func NewRouter() *Router {
	return &Router{exact: make(map[string]Handler), priority: make(map[string]Handler)}
}

// Register adds a handler.
func (r *Router) Register(cmd string, priority bool, h Handler) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if priority {
		r.priority[cmd] = h
	}
	r.exact[cmd] = h
}

// Dispatch routes a command. Returns ("", nil) when the text is not a command.
func (r *Router) Dispatch(ctx context.Context, env DispatchEnv) (string, error) {
	cmd, args := parse(env.Message.Content)
	if cmd == "" {
		return "", nil
	}
	r.mu.RLock()
	h := r.exact[cmd]
	r.mu.RUnlock()
	if h == nil {
		return "", nil
	}
	return h(ctx, env, args)
}

// IsPriority returns true for commands that bypass the session lock.
func IsPriority(text string) bool {
	cmd, _ := parse(text)
	switch cmd {
	case "/stop", "/restart", "/status":
		return true
	}
	return false
}

// Looks reports whether the text starts with a slash command.
func Looks(text string) bool {
	return strings.HasPrefix(strings.TrimSpace(text), "/")
}

func parse(s string) (string, string) {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "/") {
		return "", ""
	}
	sp := strings.IndexAny(s, " \t\n")
	if sp < 0 {
		return strings.ToLower(s), ""
	}
	return strings.ToLower(s[:sp]), strings.TrimSpace(s[sp:])
}

// RegisterBuiltins wires /status, /new, /help, /stop (and /restart as TODO).
func RegisterBuiltins(r *Router) {
	r.Register("/status", true, func(ctx context.Context, env DispatchEnv, args string) (string, error) {
		s := env.Loop.Status()
		return fmt.Sprintf("workspace: %s\nmodel: %s\nactive sessions: %d", s.Workspace, s.Model, s.Active), nil
	})
	r.Register("/new", false, func(ctx context.Context, env DispatchEnv, args string) (string, error) {
		if err := env.Loop.ClearSession(env.SessionKey); err != nil {
			return "", err
		}
		return "(session cleared)", nil
	})
	r.Register("/stop", true, func(ctx context.Context, env DispatchEnv, args string) (string, error) {
		if err := env.Loop.StopSession(env.SessionKey); err != nil {
			return "", err
		}
		return "(stopped)", nil
	})
	r.Register("/restart", true, func(ctx context.Context, env DispatchEnv, args string) (string, error) {
		if err := env.Loop.Restart(); err != nil {
			return "", err
		}
		return "(restarting)", nil
	})
	r.Register("/help", false, func(ctx context.Context, env DispatchEnv, args string) (string, error) {
		return "Commands:\n/status\t— show runtime info\n/new\t— clear current session\n/stop\t— stop the current turn\n/restart\t— restart the agent\n/help\t— show this help", nil
	})
}
