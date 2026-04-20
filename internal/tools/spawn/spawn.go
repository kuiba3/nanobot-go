// Package spawn implements the `spawn` tool which delegates a sub-task to a
// transient subagent. Runs via a Spawner that the AgentLoop provides.
package spawn

import (
	"context"
	"fmt"

	"github.com/hkuds/nanobot-go/internal/tools"
)

// Spawner is the bridge to SubagentManager. The loop implements it.
type Spawner interface {
	Spawn(ctx context.Context, req Request) (string, error)
}

// Request describes a subagent task.
type Request struct {
	Label       string
	Description string
	Message     string
	SessionKey  string
}

// New builds the spawn tool.
func New(sp Spawner) tools.Tool {
	return &spawn{
		Base: tools.Base{
			ToolName:        "spawn",
			ToolDescription: "Delegate a focused sub-task to a subagent; returns a task id once the subagent starts.",
			Params: &tools.Schema{
				Type: "object",
				Properties: map[string]*tools.Schema{
					"label":       {Type: "string", Description: "short label"},
					"description": {Type: "string"},
					"message":     {Type: "string", Description: "task prompt"},
				},
				Required: []string{"message"},
			},
		},
		sp: sp,
	}
}

type spawn struct {
	tools.Base
	sp Spawner
}

func (s *spawn) Execute(ctx context.Context, args map[string]any) (string, error) {
	if s.sp == nil {
		return "", fmt.Errorf("spawn not wired")
	}
	req := Request{
		Label:       tools.ArgString(args, "label", ""),
		Description: tools.ArgString(args, "description", ""),
		Message:     tools.ArgString(args, "message", ""),
	}
	if req.Message == "" {
		return "", fmt.Errorf("message required")
	}
	id, err := s.sp.Spawn(ctx, req)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("spawned task %s", id), nil
}
