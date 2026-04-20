// Package crontool exposes the `cron` tool. The concrete schedule implementation
// is provided by internal/cron; here we just define the tool surface and hand
// off to a Scheduler interface.
package crontool

import (
	"context"
	"fmt"

	"github.com/hkuds/nanobot-go/internal/tools"
)

// Scheduler is the backend the tool talks to.
type Scheduler interface {
	AddJob(ctx context.Context, job JobRequest) (string, error)
	CancelJob(ctx context.Context, id string) error
	ListJobs(ctx context.Context) ([]JobSummary, error)
}

// JobRequest describes a new cron job.
type JobRequest struct {
	Name    string
	Message string
	Channel string
	ChatID  string
	When    string // "at ISO8601" | "every 15m" | "cron * * * * *"
}

// JobSummary is what the list action returns.
type JobSummary struct {
	ID      string
	Name    string
	Next    string
	Message string
}

// New builds the cron tool bound to a Scheduler.
func New(s Scheduler) tools.Tool {
	return &cronTool{
		Base: tools.Base{
			ToolName:        "cron",
			ToolDescription: "Schedule a reminder or recurring message.",
			Params: &tools.Schema{
				Type: "object",
				Properties: map[string]*tools.Schema{
					"action":  {Type: "string", Enum: []any{"add", "list", "cancel"}},
					"id":      {Type: "string"},
					"name":    {Type: "string"},
					"message": {Type: "string"},
					"when":    {Type: "string", Description: "at <RFC3339> | every <duration> | cron <expr>"},
				},
				Required: []string{"action"},
			},
		},
		s: s,
	}
}

type cronTool struct {
	tools.Base
	s Scheduler
}

func (t *cronTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	if t.s == nil {
		return "", fmt.Errorf("cron scheduler not wired")
	}
	action := tools.ArgString(args, "action", "")
	switch action {
	case "add":
		id, err := t.s.AddJob(ctx, JobRequest{
			Name:    tools.ArgString(args, "name", ""),
			Message: tools.ArgString(args, "message", ""),
			When:    tools.ArgString(args, "when", ""),
		})
		if err != nil {
			return "", err
		}
		return "added " + id, nil
	case "cancel":
		id := tools.ArgString(args, "id", "")
		if id == "" {
			return "", fmt.Errorf("id required")
		}
		if err := t.s.CancelJob(ctx, id); err != nil {
			return "", err
		}
		return "cancelled " + id, nil
	case "list":
		jobs, err := t.s.ListJobs(ctx)
		if err != nil {
			return "", err
		}
		if len(jobs) == 0 {
			return "(no jobs)", nil
		}
		var out string
		for _, j := range jobs {
			out += fmt.Sprintf("%s\t%s\t%s\n", j.ID, j.Next, j.Message)
		}
		return out, nil
	}
	return "", fmt.Errorf("unknown action %q", action)
}
