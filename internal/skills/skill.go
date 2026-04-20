// Package skills discovers workspace + built-in skills from SKILL.md files.
// Mirrors Python nanobot/agent/skills.py.
package skills

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// Entry represents a discovered skill.
type Entry struct {
	Name        string // directory name == skill id
	Path        string // absolute path to SKILL.md
	Source      string // "workspace" | "builtin"
	Frontmatter map[string]any
	Body        string // markdown body without frontmatter
}

// Loader discovers skills under a workspace/skills dir and a builtin dir,
// with workspace overriding builtins by name.
type Loader struct {
	workspaceSkills string
	builtinSkills   string
	disabled        map[string]struct{}
}

// NewLoader constructs a Loader.
func NewLoader(workspace string, builtin string, disabled []string) *Loader {
	d := make(map[string]struct{}, len(disabled))
	for _, n := range disabled {
		d[n] = struct{}{}
	}
	return &Loader{
		workspaceSkills: filepath.Join(workspace, "skills"),
		builtinSkills:   builtin,
		disabled:        d,
	}
}

// List returns all discovered, non-disabled skills. Workspace takes priority.
func (l *Loader) List() ([]*Entry, error) {
	ws, err := l.scan(l.workspaceSkills, "workspace", nil)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]struct{}, len(ws))
	for _, e := range ws {
		seen[e.Name] = struct{}{}
	}
	bi, err := l.scan(l.builtinSkills, "builtin", seen)
	if err != nil {
		return nil, err
	}
	all := append(ws, bi...)
	sort.Slice(all, func(i, j int) bool { return all[i].Name < all[j].Name })
	return all, nil
}

func (l *Loader) scan(dir, source string, skip map[string]struct{}) ([]*Entry, error) {
	if dir == "" {
		return nil, nil
	}
	if _, err := os.Stat(dir); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	out := make([]*Entry, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if skip != nil {
			if _, ok := skip[e.Name()]; ok {
				continue
			}
		}
		if _, ok := l.disabled[e.Name()]; ok {
			continue
		}
		p := filepath.Join(dir, e.Name(), "SKILL.md")
		if _, err := os.Stat(p); err != nil {
			continue
		}
		sk, err := loadSKILL(p)
		if err != nil {
			continue
		}
		sk.Name = e.Name()
		sk.Source = source
		out = append(out, sk)
	}
	return out, nil
}

func loadSKILL(path string) (*Entry, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	var (
		inFront    bool
		frontLines []string
		bodyLines  []string
	)
	firstLine := true
	for sc.Scan() {
		line := sc.Text()
		if firstLine {
			firstLine = false
			if strings.TrimSpace(line) == "---" {
				inFront = true
				continue
			}
			bodyLines = append(bodyLines, line)
			continue
		}
		if inFront && strings.TrimSpace(line) == "---" {
			inFront = false
			continue
		}
		if inFront {
			frontLines = append(frontLines, line)
			continue
		}
		bodyLines = append(bodyLines, line)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	e := &Entry{Path: path, Body: strings.Join(bodyLines, "\n")}
	e.Frontmatter = parseYAMLish(frontLines)
	return e, nil
}

// Description returns the frontmatter "description" if present.
func (e *Entry) Description() string {
	if v, ok := e.Frontmatter["description"].(string); ok {
		return v
	}
	return ""
}

// Always reports whether the skill opts in to always-on loading.
func (e *Entry) Always() bool {
	if v, ok := e.Frontmatter["always"].(bool); ok && v {
		return true
	}
	if meta, ok := e.Frontmatter["metadata"].(map[string]any); ok {
		if nano, ok := meta["nanobot"].(map[string]any); ok {
			if v, ok := nano["always"].(bool); ok {
				return v
			}
		}
		if clw, ok := meta["openclaw"].(map[string]any); ok {
			if v, ok := clw["always"].(bool); ok {
				return v
			}
		}
	}
	return false
}

// RequiresBins returns the list of binaries this skill declares.
func (e *Entry) RequiresBins() []string {
	return extractRequires(e.Frontmatter, "bins")
}

// RequiresEnv returns the list of env vars this skill declares.
func (e *Entry) RequiresEnv() []string {
	return extractRequires(e.Frontmatter, "env")
}

func extractRequires(fm map[string]any, key string) []string {
	if v, ok := fm["requires"].(map[string]any); ok {
		if arr, ok := v[key].([]any); ok {
			out := make([]string, 0, len(arr))
			for _, x := range arr {
				if s, ok := x.(string); ok {
					out = append(out, s)
				}
			}
			return out
		}
	}
	if meta, ok := fm["metadata"].(map[string]any); ok {
		for _, k := range []string{"nanobot", "openclaw"} {
			if nested, ok := meta[k].(map[string]any); ok {
				if r, ok := nested["requires"].(map[string]any); ok {
					if arr, ok := r[key].([]any); ok {
						out := make([]string, 0, len(arr))
						for _, x := range arr {
							if s, ok := x.(string); ok {
								out = append(out, s)
							}
						}
						return out
					}
				}
			}
		}
	}
	return nil
}

// Available reports whether all declared bin/env requirements are satisfied.
func (e *Entry) Available() (bool, []string) {
	missing := make([]string, 0)
	for _, b := range e.RequiresBins() {
		if _, err := exec.LookPath(b); err != nil {
			missing = append(missing, "bin:"+b)
		}
	}
	for _, v := range e.RequiresEnv() {
		if os.Getenv(v) == "" {
			missing = append(missing, "env:"+v)
		}
	}
	return len(missing) == 0, missing
}

// BuildSummary returns a markdown bullet list suitable for injection into
// the system prompt, excluding the given names.
func (l *Loader) BuildSummary(exclude map[string]struct{}) (string, error) {
	entries, err := l.List()
	if err != nil {
		return "", err
	}
	var b strings.Builder
	for _, e := range entries {
		if _, skip := exclude[e.Name]; skip {
			continue
		}
		avail, missing := e.Available()
		desc := e.Description()
		if avail {
			fmt.Fprintf(&b, "- **%s** — %s  `%s`\n", e.Name, desc, e.Path)
		} else {
			if len(missing) > 0 {
				fmt.Fprintf(&b, "- **%s** — %s (unavailable: %s)  `%s`\n", e.Name, desc, strings.Join(missing, ", "), e.Path)
			} else {
				fmt.Fprintf(&b, "- **%s** — %s (unavailable)  `%s`\n", e.Name, desc, e.Path)
			}
		}
	}
	return strings.TrimRight(b.String(), "\n"), nil
}

// AlwaysSkills returns the names of always-on skills whose deps are met.
func (l *Loader) AlwaysSkills() ([]string, error) {
	entries, err := l.List()
	if err != nil {
		return nil, err
	}
	out := make([]string, 0)
	for _, e := range entries {
		if !e.Always() {
			continue
		}
		if ok, _ := e.Available(); !ok {
			continue
		}
		out = append(out, e.Name)
	}
	return out, nil
}

// LoadForContext returns the concatenated bodies of the named skills, used
// by ContextBuilder when injecting "Active Skills" into the system prompt.
func (l *Loader) LoadForContext(names []string) (string, error) {
	if len(names) == 0 {
		return "", nil
	}
	want := make(map[string]struct{}, len(names))
	for _, n := range names {
		want[n] = struct{}{}
	}
	entries, err := l.List()
	if err != nil {
		return "", err
	}
	var b strings.Builder
	for _, e := range entries {
		if _, ok := want[e.Name]; !ok {
			continue
		}
		fmt.Fprintf(&b, "## %s\n\n%s\n\n", e.Name, strings.TrimSpace(e.Body))
	}
	return strings.TrimSpace(b.String()), nil
}
