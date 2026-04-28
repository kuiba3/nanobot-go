// Package skillcreator provides the "skill-creator" skill documentation.
package skillcreator

import skills "github.com/kuiba3/nanobot-go/skills"

func init() { skills.Register(Skill{}) }

// Skill explains how to create new skills.
type Skill struct{}

// Name returns "skill-creator".
func (Skill) Name() string { return "skill-creator" }

// Files returns no extra files.
func (Skill) Files() map[string]string { return nil }

// SkillMD returns the body.
func (Skill) SkillMD() string {
	return `---
name: skill-creator
description: Author new skills that the agent can discover and use.
---

# Skill creator

A skill is a directory under ` + "`workspace/skills/<name>/`" + ` containing a
` + "`SKILL.md`" + ` file. The first block is YAML frontmatter:

` + "```" + `yaml
---
name: my-skill
description: One-sentence summary shown in the skills index.
always: false            # set true to auto-load in every system prompt
requires:                 # optional dependency declaration
  bins: ["jq", "curl"]
  env:  ["MY_API_KEY"]
---
` + "```" + `

Then the markdown body describes how the skill should be used. Keep it short
and actionable; rely on the agent's built-in tools rather than bundling
scripts unless necessary.

## Workflow

1. Create ` + "`workspace/skills/<name>/SKILL.md`" + `.
2. Re-run the agent; the new skill shows up in the skills index automatically.
3. If it declares ` + "`requires.bins`" + ` that aren't installed, it will render as
   "unavailable" — install the prerequisite or adjust the declaration.
`
}
