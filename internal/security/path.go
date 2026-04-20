package security

import (
	"fmt"
	"path/filepath"
	"strings"
)

// PathSandbox restricts file access to a set of allowed directories.
type PathSandbox struct {
	workspace    string
	allowedDirs  []string // absolute paths
	extraAllowed []string // read-only extras (e.g. media dir)
}

// NewPathSandbox constructs a sandbox.
func NewPathSandbox(workspace string, extra []string) *PathSandbox {
	abs, _ := filepath.Abs(workspace)
	extras := make([]string, 0, len(extra))
	for _, e := range extra {
		ea, err := filepath.Abs(e)
		if err != nil {
			continue
		}
		extras = append(extras, ea)
	}
	return &PathSandbox{workspace: abs, allowedDirs: []string{abs}, extraAllowed: extras}
}

// Workspace returns the workspace root.
func (s *PathSandbox) Workspace() string { return s.workspace }

// Resolve canonicalizes the supplied path (absolute or workspace-relative)
// and enforces it resides inside one of the allowed directories. Symlinks
// outside the sandbox are rejected.
func (s *PathSandbox) Resolve(p string) (string, error) {
	if p == "" {
		return "", fmt.Errorf("empty path")
	}
	var abs string
	if filepath.IsAbs(p) {
		abs = filepath.Clean(p)
	} else {
		abs = filepath.Join(s.workspace, p)
	}
	// Follow symlinks to their target before the check.
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		// path may not exist yet; perform textual containment check
		real = abs
	}
	if !s.contains(real) {
		return "", fmt.Errorf("path %q escapes sandbox", p)
	}
	return real, nil
}

// Contains reports whether abs is within any allowed directory (text-based).
func (s *PathSandbox) Contains(abs string) bool { return s.contains(abs) }

func (s *PathSandbox) contains(abs string) bool {
	abs = filepath.Clean(abs)
	for _, root := range append(s.allowedDirs, s.extraAllowed...) {
		if abs == root {
			return true
		}
		if strings.HasPrefix(abs, root+string(filepath.Separator)) {
			return true
		}
	}
	return false
}
