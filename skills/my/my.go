// Package my provides documentation for the "my" self-introspection tool.
package my

import skills "github.com/kuiba3/nanobot-go/skills"

func init() { skills.Register(Skill{}) }

// Skill documents the `my` tool.
type Skill struct{}

// Name returns "my".
func (Skill) Name() string { return "my" }

// Files returns no extra files.
func (Skill) Files() map[string]string { return nil }

// SkillMD returns the body.
func (Skill) SkillMD() string {
	return `---
name: my
description: Inspect or tweak runtime agent settings via the my tool.
---

# My skill

Use the ` + "`my`" + ` tool to inspect or adjust runtime agent configuration:

- ` + "`my action=keys`" + ` — list settable keys.
- ` + "`my action=get`" + ` — dump the current snapshot as JSON.
- ` + "`my action=get key=model`" + ` — fetch one field.
- ` + "`my action=set key=temperature value=0.3`" + ` — mutate a field.

Do not flip settings aggressively; explain the change to the user first when
it affects their session (e.g. model switches).
`
}
