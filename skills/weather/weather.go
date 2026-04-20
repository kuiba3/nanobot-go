// Package weather provides a weather lookup skill via wttr.in / open-meteo.
package weather

import skills "github.com/hkuds/nanobot-go/skills"

func init() { skills.Register(Skill{}) }

// Skill documents weather access.
type Skill struct{}

// Name returns "weather".
func (Skill) Name() string { return "weather" }

// Files returns no extra files.
func (Skill) Files() map[string]string { return nil }

// SkillMD returns the body.
func (Skill) SkillMD() string {
	return `---
name: weather
description: Look up weather via wttr.in (curl) or Open-Meteo JSON (no key).
requires:
  bins:
    - curl
---

# Weather skill

- ` + "`curl wttr.in/<city>?format=3`" + ` — one-line "Paris: ⛅ +18°C".
- ` + "`curl 'https://api.open-meteo.com/v1/forecast?latitude=48.85&longitude=2.35&current_weather=true'`" + `
  — JSON response with current_weather.

Prefer Open-Meteo for programmatic access because its JSON is stable.
`
}
