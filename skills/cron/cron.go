// Package cron provides the "cron" skill documentation.
package cron

import skills "github.com/hkuds/nanobot-go/skills"

func init() { skills.Register(Skill{}) }

// Skill documents the cron tool usage.
type Skill struct{}

// Name returns "cron".
func (Skill) Name() string { return "cron" }

// Files returns no extra files.
func (Skill) Files() map[string]string { return nil }

// SkillMD returns the body.
func (Skill) SkillMD() string {
	return `---
name: cron
description: Schedule reminders, tasks and recurring prompts via the cron tool.
---

# Cron skill

Use the ` + "`cron`" + ` tool to schedule work:

- One-shot: ` + "`cron action=add when=\"at 2026-04-21T09:00:00Z\" message=\"stand-up reminder\"`" + `
- Interval: ` + "`cron action=add when=\"every 15m\" message=\"poll the queue\"`" + `
- Cron expr: ` + "`cron action=add when=\"cron 0 9 * * 1\" message=\"Monday digest\"`" + `
- List: ` + "`cron action=list`" + `
- Cancel: ` + "`cron action=cancel id=<id>`" + `

Keep the scheduled message short and task-focused — it becomes the agent's
prompt when the job fires.
`
}
