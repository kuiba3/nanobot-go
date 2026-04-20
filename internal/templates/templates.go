// Package templates embeds the agent-facing markdown templates and exposes
// a Render helper that uses Go text/template.
package templates

import (
	"bytes"
	"embed"
	"fmt"
	"io/fs"
	"strings"
	"sync"
	"text/template"
)

//go:embed files
var files embed.FS

var (
	mu    sync.Mutex
	cache = make(map[string]*template.Template)
)

// Render parses and executes the template at files/<name> (relative to the
// embedded fs root) with the given data.
func Render(name string, data any) (string, error) {
	tmpl, err := get(name)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", err
	}
	return strings.TrimRight(buf.String(), "\n"), nil
}

func get(name string) (*template.Template, error) {
	mu.Lock()
	defer mu.Unlock()
	if t, ok := cache[name]; ok {
		return t, nil
	}
	data, err := fs.ReadFile(files, "files/"+name)
	if err != nil {
		return nil, fmt.Errorf("template %s: %w", name, err)
	}
	t, err := template.New(name).Parse(string(data))
	if err != nil {
		return nil, err
	}
	cache[name] = t
	return t, nil
}

// ReadFile returns raw bytes of an embedded file (e.g. the MEMORY.md default).
func ReadFile(name string) ([]byte, error) {
	return fs.ReadFile(files, "files/"+name)
}

// Names returns all embedded template paths, useful for snapshot copying.
func Names() []string {
	var out []string
	_ = fs.WalkDir(files, "files", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		out = append(out, strings.TrimPrefix(path, "files/"))
		return nil
	})
	return out
}
