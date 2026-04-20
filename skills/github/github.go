// Package github provides the "github" skill — documentation for using `gh`.
package github

import skills "github.com/hkuds/nanobot-go/skills"

func init() { skills.Register(Skill{}) }

// Skill documents GitHub CLI usage.
type Skill struct{}

// Name returns "github".
func (Skill) Name() string { return "github" }

// Files returns no extra files.
func (Skill) Files() map[string]string { return nil }

// SkillMD returns the body.
func (Skill) SkillMD() string {
	return `---
name: github
description: Use the GitHub CLI (gh) for PRs, issues, runs, and gh api.
requires:
  bins:
    - gh
---

# GitHub skill

Use ` + "`gh`" + ` from the exec tool for GitHub operations. Common patterns:

- ` + "`gh pr list --state open`" + `
- ` + "`gh pr view <n> --comments`" + `
- ` + "`gh issue create --title ... --body ...`" + `
- ` + "`gh api repos/OWNER/REPO/issues/123`" + `
- ` + "`gh run watch <run-id>`" + `

Always prefer ` + "`gh`" + ` over crafting raw ` + "`curl`" + ` requests with a token; the
CLI handles authentication transparently.
`
}
