// Package memory provides the "memory" skill — documentation that teaches
// the agent how to use the two-layer memory system (MEMORY.md + history.jsonl).
package memory

import skills "github.com/hkuds/nanobot-go/skills"

func init() { skills.Register(Skill{}) }

// Skill is the memory skill descriptor.
type Skill struct{}

// Name returns "memory".
func (Skill) Name() string { return "memory" }

// Files returns no extra files.
func (Skill) Files() map[string]string { return nil }

// SkillMD returns the skill body.
func (Skill) SkillMD() string {
	return `---
name: memory
description: Two-layer memory system — MEMORY.md (durable facts) + history.jsonl (append-only log).
always: true
---

# Memory

Your long-term state lives in two files under ` + "`workspace/memory/`" + `:

- ` + "`MEMORY.md`" + ` — concise, durable facts about the user and active projects.
  Curated by the Dream process. Do not overwrite without intent; small edits only.
- ` + "`history.jsonl`" + ` — every archived turn and Dream-generated summary.
  Append-only. Use ` + "`grep`" + ` to search.

## Guidance

- Never spell out secrets (API keys, passwords) in either file.
- If you need to recall a fact the user told you, first look in MEMORY.md, then
  grep history.jsonl for keywords.
- If an important fact is missing from MEMORY.md, tell the user and suggest
  updating it explicitly (e.g., using ` + "`write_file`" + `).
`
}
