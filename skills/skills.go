// Package skills is the registry of Go-native skills. Each skill installs a
// SKILL.md (and any helper scripts it needs) into <workspace>/skills/<name>/
// when InstallDefaults is called. The loader in internal/skills discovers
// them the same way it discovers user-authored workspace skills.
package skills

import (
	"os"
	"path/filepath"
)

// Skill represents an installable skill.
type Skill interface {
	Name() string
	SkillMD() string
	Files() map[string]string // extra files relative to skill dir
}

// defaults is the list of skills bundled with nanobot-go. Additional skills
// add themselves via Register in their own package's init.
var defaults []Skill

// Register adds a skill to the defaults list.
func Register(s Skill) { defaults = append(defaults, s) }

// InstallDefaults writes every registered skill into <workspace>/skills/.
func InstallDefaults(workspace string) error {
	base := filepath.Join(workspace, "skills")
	for _, s := range defaults {
		dir := filepath.Join(base, s.Name())
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		if err := writeIfMissing(filepath.Join(dir, "SKILL.md"), s.SkillMD()); err != nil {
			return err
		}
		for name, content := range s.Files() {
			target := filepath.Join(dir, name)
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			if err := writeIfMissing(target, content); err != nil {
				return err
			}
		}
	}
	return nil
}

func writeIfMissing(path, content string) error {
	if _, err := os.Stat(path); err == nil {
		return nil
	}
	return os.WriteFile(path, []byte(content), 0o600)
}
