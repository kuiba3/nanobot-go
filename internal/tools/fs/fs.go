// Package fs implements the read_file / write_file / edit_file / list_dir
// built-in tools. All file operations run inside the path sandbox.
package fs

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/hkuds/nanobot-go/internal/security"
	"github.com/hkuds/nanobot-go/internal/tools"
)

const (
	defaultMaxBytes = 200_000
	maxWriteBytes   = 2_000_000
)

// New registers fs tools against the given registry.
func New(sb *security.PathSandbox) []tools.Tool {
	return []tools.Tool{
		&readFile{Base: tools.Base{
			ToolName:        "read_file",
			ToolDescription: "Read a file from the workspace. Returns up to 200KB of text.",
			IsReadOnly:      true,
			IsConcurrent:    true,
			Params: &tools.Schema{
				Type: "object",
				Properties: map[string]*tools.Schema{
					"path":    {Type: "string", Description: "Absolute or workspace-relative path."},
					"offset":  {Type: "integer", Description: "Byte offset to start reading at (optional)."},
					"limit":   {Type: "integer", Description: "Maximum bytes to read (optional, default 200000)."},
				},
				Required: []string{"path"},
			},
		}, sb: sb},
		&writeFile{Base: tools.Base{
			ToolName:        "write_file",
			ToolDescription: "Overwrite (or create) a file inside the workspace.",
			Params: &tools.Schema{
				Type: "object",
				Properties: map[string]*tools.Schema{
					"path":    {Type: "string"},
					"content": {Type: "string"},
				},
				Required: []string{"path", "content"},
			},
		}, sb: sb},
		&editFile{Base: tools.Base{
			ToolName:        "edit_file",
			ToolDescription: "Replace one occurrence of old_string with new_string in a file.",
			Params: &tools.Schema{
				Type: "object",
				Properties: map[string]*tools.Schema{
					"path":        {Type: "string"},
					"old_string":  {Type: "string"},
					"new_string":  {Type: "string"},
					"replace_all": {Type: "boolean"},
				},
				Required: []string{"path", "old_string", "new_string"},
			},
		}, sb: sb},
		&listDir{Base: tools.Base{
			ToolName:        "list_dir",
			ToolDescription: "List directory entries with file sizes.",
			IsReadOnly:      true,
			IsConcurrent:    true,
			Params: &tools.Schema{
				Type: "object",
				Properties: map[string]*tools.Schema{
					"path": {Type: "string"},
				},
				Required: []string{"path"},
			},
		}, sb: sb},
	}
}

type readFile struct {
	tools.Base
	sb *security.PathSandbox
}

func (t *readFile) Execute(ctx context.Context, args map[string]any) (string, error) {
	p := tools.ArgString(args, "path", "")
	real, err := t.sb.Resolve(p)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(real)
	if err != nil {
		return "", err
	}
	if info.IsDir() {
		return "", fmt.Errorf("path is a directory")
	}
	limit := tools.ArgInt(args, "limit", defaultMaxBytes)
	if limit <= 0 || limit > defaultMaxBytes {
		limit = defaultMaxBytes
	}
	offset := tools.ArgInt(args, "offset", 0)
	f, err := os.Open(real)
	if err != nil {
		return "", err
	}
	defer f.Close()
	if offset > 0 {
		if _, err := f.Seek(int64(offset), 0); err != nil {
			return "", err
		}
	}
	buf := make([]byte, limit)
	n, _ := f.Read(buf)
	truncated := ""
	if info.Size()-int64(offset) > int64(n) {
		truncated = fmt.Sprintf("\n\n[truncated at %d bytes; file is %d bytes]", n, info.Size())
	}
	return string(buf[:n]) + truncated, nil
}

type writeFile struct {
	tools.Base
	sb *security.PathSandbox
}

func (t *writeFile) Execute(ctx context.Context, args map[string]any) (string, error) {
	p := tools.ArgString(args, "path", "")
	real, err := t.sb.Resolve(p)
	if err != nil {
		return "", err
	}
	content := tools.ArgString(args, "content", "")
	if len(content) > maxWriteBytes {
		return "", fmt.Errorf("content exceeds %d bytes", maxWriteBytes)
	}
	if err := os.MkdirAll(filepath.Dir(real), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(real, []byte(content), 0o644); err != nil {
		return "", err
	}
	return fmt.Sprintf("wrote %d bytes to %s", len(content), real), nil
}

type editFile struct {
	tools.Base
	sb *security.PathSandbox
}

func (t *editFile) Execute(ctx context.Context, args map[string]any) (string, error) {
	p := tools.ArgString(args, "path", "")
	real, err := t.sb.Resolve(p)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(real)
	if err != nil {
		return "", err
	}
	content := string(data)
	oldStr := tools.ArgString(args, "old_string", "")
	newStr := tools.ArgString(args, "new_string", "")
	if oldStr == "" {
		return "", fmt.Errorf("old_string must be non-empty")
	}
	var out string
	if tools.ArgBool(args, "replace_all", false) {
		out = strings.ReplaceAll(content, oldStr, newStr)
		if out == content {
			return "", fmt.Errorf("old_string not found")
		}
	} else {
		idx := strings.Index(content, oldStr)
		if idx < 0 {
			return "", fmt.Errorf("old_string not found")
		}
		if strings.Index(content[idx+len(oldStr):], oldStr) >= 0 {
			return "", fmt.Errorf("old_string not unique; provide more context or use replace_all")
		}
		out = content[:idx] + newStr + content[idx+len(oldStr):]
	}
	if err := os.WriteFile(real, []byte(out), 0o644); err != nil {
		return "", err
	}
	return fmt.Sprintf("edited %s (%d -> %d bytes)", real, len(content), len(out)), nil
}

type listDir struct {
	tools.Base
	sb *security.PathSandbox
}

func (t *listDir) Execute(ctx context.Context, args map[string]any) (string, error) {
	p := tools.ArgString(args, "path", "")
	real, err := t.sb.Resolve(p)
	if err != nil {
		return "", err
	}
	entries, err := os.ReadDir(real)
	if err != nil {
		return "", err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	var b strings.Builder
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			continue
		}
		kind := "file"
		if e.IsDir() {
			kind = "dir"
		}
		fmt.Fprintf(&b, "%s\t%d\t%s\n", kind, info.Size(), e.Name())
	}
	return b.String(), nil
}
