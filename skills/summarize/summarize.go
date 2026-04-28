// Package summarize provides the "summarize" skill documentation.
package summarize

import skills "github.com/kuiba3/nanobot-go/skills"

func init() { skills.Register(Skill{}) }

// Skill documents the summarize approach.
type Skill struct{}

// Name returns "summarize".
func (Skill) Name() string { return "summarize" }

// Files returns no extra files.
func (Skill) Files() map[string]string { return nil }

// SkillMD returns the body.
func (Skill) SkillMD() string {
	return `---
name: summarize
description: Summarize URLs, local files, or long pastes using web_fetch + agent reasoning.
---

# Summarize skill

To summarize arbitrary content:

1. If it's a URL, call ` + "`web_fetch`" + ` with a sensible max-bytes cap.
2. If it's a local file, use ` + "`read_file`" + `.
3. Produce a bullet-point summary: goals, key findings, action items.
4. Offer to save the summary via ` + "`write_file`" + ` into the workspace on request.

Keep summaries concise and faithful; never fabricate claims the source does
not support.
`
}
