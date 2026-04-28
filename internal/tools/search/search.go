// Package search implements glob + grep tools over the workspace.
package search

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/kuiba3/nanobot-go/internal/security"
	"github.com/kuiba3/nanobot-go/internal/tools"
)

const (
	globMaxResults = 500
	grepMaxResults = 500
	grepMaxBytes   = 4 * 1024 * 1024
)

// ignorePrefixes are directories we never descend into.
var ignorePrefixes = []string{".git", "node_modules", ".venv", "venv", "__pycache__", ".cache", ".idea", ".vscode", "target", "dist", "build", ".nanobot"}

// New returns the glob + grep tools.
func New(sb *security.PathSandbox) []tools.Tool {
	return []tools.Tool{
		&globTool{Base: tools.Base{
			ToolName:        "glob",
			ToolDescription: "Recursively list files matching a glob pattern within the workspace.",
			IsReadOnly:      true,
			IsConcurrent:    true,
			Params: &tools.Schema{
				Type: "object",
				Properties: map[string]*tools.Schema{
					"pattern": {Type: "string", Description: "e.g. **/*.go or src/*.md"},
					"path":    {Type: "string", Description: "subdirectory to search under (defaults to workspace root)"},
				},
				Required: []string{"pattern"},
			},
		}, sb: sb},
		&grepTool{Base: tools.Base{
			ToolName:        "grep",
			ToolDescription: "Search file contents with a regex. Returns up to 500 match lines.",
			IsReadOnly:      true,
			IsConcurrent:    true,
			Params: &tools.Schema{
				Type: "object",
				Properties: map[string]*tools.Schema{
					"pattern":      {Type: "string", Description: "regex (RE2 syntax)."},
					"path":         {Type: "string", Description: "file or directory to search. Defaults to workspace root."},
					"glob":         {Type: "string", Description: "optional glob filter (e.g. *.go)"},
					"ignore_case":  {Type: "boolean"},
					"files_only":   {Type: "boolean", Description: "return only file names that contain matches."},
				},
				Required: []string{"pattern"},
			},
		}, sb: sb},
	}
}

type globTool struct {
	tools.Base
	sb *security.PathSandbox
}

func (t *globTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	pattern := tools.ArgString(args, "pattern", "")
	root := tools.ArgString(args, "path", "")
	if pattern == "" {
		return "", fmt.Errorf("pattern must be non-empty")
	}
	start := t.sb.Workspace()
	if root != "" {
		r, err := t.sb.Resolve(root)
		if err != nil {
			return "", err
		}
		start = r
	}

	// Support **/ as walk indicator: treat pattern as a path.Match style with
	// recursive ** segments expanded.
	var matches []string
	err := filepath.Walk(start, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			if shouldIgnore(info.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(start, p)
		if err != nil {
			return nil
		}
		if matchesGlob(pattern, rel) {
			matches = append(matches, rel)
			if len(matches) >= globMaxResults {
				return filepath.SkipAll
			}
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	if len(matches) == 0 {
		return "", nil
	}
	return strings.Join(matches, "\n"), nil
}

func matchesGlob(pattern, rel string) bool {
	// Convert ** to a regex fragment.
	// simple approach: split by '/' and match segment by segment.
	pSegs := strings.Split(pattern, "/")
	fSegs := strings.Split(filepath.ToSlash(rel), "/")
	return matchSegs(pSegs, fSegs)
}

func matchSegs(p, f []string) bool {
	for len(p) > 0 && len(f) > 0 {
		if p[0] == "**" {
			// zero or more segments
			if len(p) == 1 {
				return true
			}
			for i := 0; i <= len(f); i++ {
				if matchSegs(p[1:], f[i:]) {
					return true
				}
			}
			return false
		}
		ok, _ := filepath.Match(p[0], f[0])
		if !ok {
			return false
		}
		p = p[1:]
		f = f[1:]
	}
	if len(p) == 1 && p[0] == "**" {
		return true
	}
	return len(p) == 0 && len(f) == 0
}

func shouldIgnore(name string) bool {
	for _, p := range ignorePrefixes {
		if name == p {
			return true
		}
	}
	return strings.HasPrefix(name, ".") && name != "." && name != ".."
}

type grepTool struct {
	tools.Base
	sb *security.PathSandbox
}

func (t *grepTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	pat := tools.ArgString(args, "pattern", "")
	if pat == "" {
		return "", fmt.Errorf("pattern must be non-empty")
	}
	flags := ""
	if tools.ArgBool(args, "ignore_case", false) {
		flags = "(?i)"
	}
	re, err := regexp.Compile(flags + pat)
	if err != nil {
		return "", fmt.Errorf("invalid regex: %w", err)
	}
	root := tools.ArgString(args, "path", "")
	globFilter := tools.ArgString(args, "glob", "")
	onlyFiles := tools.ArgBool(args, "files_only", false)

	start := t.sb.Workspace()
	if root != "" {
		r, err := t.sb.Resolve(root)
		if err != nil {
			return "", err
		}
		start = r
	}
	var out strings.Builder
	matchedFiles := make(map[string]struct{})
	count := 0
	err = filepath.Walk(start, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			if shouldIgnore(info.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if info.Size() > grepMaxBytes {
			return nil
		}
		rel, _ := filepath.Rel(start, p)
		if globFilter != "" {
			ok, _ := filepath.Match(globFilter, filepath.Base(p))
			if !ok {
				return nil
			}
		}
		f, err := os.Open(p)
		if err != nil {
			return nil
		}
		defer f.Close()
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		lineNo := 0
		for sc.Scan() {
			lineNo++
			line := sc.Text()
			if re.MatchString(line) {
				if onlyFiles {
					matchedFiles[rel] = struct{}{}
					break
				}
				fmt.Fprintf(&out, "%s:%d: %s\n", rel, lineNo, line)
				count++
				if count >= grepMaxResults {
					return filepath.SkipAll
				}
			}
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	if onlyFiles {
		files := make([]string, 0, len(matchedFiles))
		for k := range matchedFiles {
			files = append(files, k)
		}
		return strings.Join(files, "\n"), nil
	}
	return strings.TrimRight(out.String(), "\n"), nil
}
