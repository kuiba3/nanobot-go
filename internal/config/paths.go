package config

import (
	"os"
	"path/filepath"
)

// DefaultConfigPath returns ~/.nanobot/config.json, or /etc/nanobot/config.json
// if HOME is not set.
func DefaultConfigPath() string {
	if h, err := os.UserHomeDir(); err == nil && h != "" {
		return filepath.Join(h, ".nanobot", "config.json")
	}
	return "/etc/nanobot/config.json"
}

// DefaultWorkspacePath returns ~/.nanobot/workspace.
func DefaultWorkspacePath() string {
	if h, err := os.UserHomeDir(); err == nil && h != "" {
		return filepath.Join(h, ".nanobot", "workspace")
	}
	return "/var/lib/nanobot/workspace"
}

// DataDir returns the directory that contains the config file (used as
// the base for per-user data like cli history).
func DataDir(configPath string) string {
	return filepath.Dir(configPath)
}

// WorkspacePath returns the effective workspace directory for the given
// config. Resolves `~` and falls back to the default when empty.
func (c *Config) WorkspacePath() string {
	ws := c.Agents.Defaults.Workspace
	if ws == "" {
		return DefaultWorkspacePath()
	}
	return expandHome(ws)
}

func expandHome(p string) string {
	if len(p) >= 2 && p[0] == '~' && (p[1] == '/' || p[1] == filepath.Separator) {
		if h, err := os.UserHomeDir(); err == nil && h != "" {
			return filepath.Join(h, p[2:])
		}
	}
	return p
}
