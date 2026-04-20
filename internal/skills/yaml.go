package skills

import (
	"strconv"
	"strings"
)

// parseYAMLish is a minimal YAML subset parser for skill frontmatter.
// Supports:
//   - scalar key/value pairs
//   - nested maps (block style, indentation 2 spaces)
//   - string lists introduced with "- item"
//
// It does NOT support: flow style, anchors, quoted multi-line scalars.
func parseYAMLish(lines []string) map[string]any {
	root := make(map[string]any)
	// Each frame holds a "setter" callback so the actual container (slice or
	// map) can be stitched back into the parent regardless of value type.
	stack := []frame{{indent: -1, container: containerMap{m: root}}}

	i := 0
	for i < len(lines) {
		line := lines[i]
		if strings.TrimSpace(line) == "" || strings.HasPrefix(strings.TrimSpace(line), "#") {
			i++
			continue
		}
		indent := leadingSpaces(line)
		for len(stack) > 1 && indent <= stack[len(stack)-1].indent {
			stack = stack[:len(stack)-1]
		}
		top := stack[len(stack)-1]
		content := strings.TrimSpace(line)

		if strings.HasPrefix(content, "- ") || content == "-" {
			rest := strings.TrimSpace(strings.TrimPrefix(content, "-"))
			switch c := top.container.(type) {
			case *containerList:
				if rest == "" {
					// object list item starting on next line (unsupported for now)
					c.append("")
				} else {
					c.append(parseScalar(rest))
				}
			default:
				// not expected for a well-formed document
			}
			i++
			continue
		}

		idx := strings.Index(content, ":")
		if idx < 0 {
			i++
			continue
		}
		key := strings.TrimSpace(content[:idx])
		rest := strings.TrimSpace(content[idx+1:])
		if rest != "" {
			setMapKey(top.container, key, parseScalar(rest))
			i++
			continue
		}

		// Value starts on next line: peek to decide map vs list.
		nextLine := peekSignificant(lines, i+1)
		if nextLine == "" {
			setMapKey(top.container, key, "")
			i++
			continue
		}
		nIndent := leadingSpaces(nextLine)
		nContent := strings.TrimSpace(nextLine)
		if nIndent > indent && (strings.HasPrefix(nContent, "- ") || nContent == "-") {
			list := &containerList{}
			setMapKey(top.container, key, list)
			stack = append(stack, frame{indent: indent, container: list})
		} else if nIndent > indent {
			child := make(map[string]any)
			setMapKey(top.container, key, containerMap{m: child})
			stack = append(stack, frame{indent: indent, container: containerMap{m: child}})
		} else {
			setMapKey(top.container, key, "")
		}
		i++
	}
	// finalize: unwrap container wrappers
	return finalizeMap(root)
}

type container interface{ isContainer() }

type containerMap struct{ m map[string]any }

func (containerMap) isContainer() {}

type containerList struct{ items []any }

func (c *containerList) isContainer()            {}
func (c *containerList) append(v any)            { c.items = append(c.items, v) }

type frame struct {
	indent    int
	container container
}

func setMapKey(c container, key string, val any) {
	m, ok := c.(containerMap)
	if !ok {
		return
	}
	m.m[key] = val
}

// finalizeMap walks the tree and replaces containerMap / *containerList wrappers
// with plain Go types.
func finalizeMap(m map[string]any) map[string]any {
	for k, v := range m {
		m[k] = finalizeValue(v)
	}
	return m
}

func finalizeValue(v any) any {
	switch t := v.(type) {
	case containerMap:
		return finalizeMap(t.m)
	case *containerList:
		out := make([]any, len(t.items))
		for i, x := range t.items {
			out[i] = finalizeValue(x)
		}
		return out
	case map[string]any:
		return finalizeMap(t)
	default:
		return v
	}
}

func parseScalar(s string) any {
	s = strings.TrimSpace(s)
	if len(s) >= 2 && ((s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'')) {
		if s[0] == '"' {
			if v, err := strconv.Unquote(s); err == nil {
				return v
			}
		}
		return strings.Trim(s, `"'`)
	}
	lc := strings.ToLower(s)
	switch lc {
	case "true", "yes":
		return true
	case "false", "no":
		return false
	case "null", "~":
		return nil
	}
	if n, err := strconv.Atoi(s); err == nil {
		return n
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return f
	}
	return s
}

func leadingSpaces(line string) int {
	n := 0
	for _, r := range line {
		if r == ' ' {
			n++
			continue
		}
		if r == '\t' {
			n += 2
			continue
		}
		break
	}
	return n
}

func peekSignificant(lines []string, from int) string {
	for i := from; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "" || strings.HasPrefix(strings.TrimSpace(lines[i]), "#") {
			continue
		}
		return lines[i]
	}
	return ""
}
