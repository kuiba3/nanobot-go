package skills

import (
	"os"
	"path/filepath"
	"testing"
)

func writeSkill(t *testing.T, root, name, front, body string) {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(front+body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestLoaderDiscovery(t *testing.T) {
	ws := t.TempDir()
	builtin := t.TempDir()

	writeSkill(t, filepath.Join(builtin), "memory",
		"---\nname: memory\ndescription: mem\nalways: true\n---\n",
		"# memory body\n")
	writeSkill(t, filepath.Join(builtin), "github",
		"---\nname: github\ndescription: gh\nrequires:\n  bins:\n    - __definitely_missing__\n---\n",
		"# gh body\n")
	writeSkill(t, filepath.Join(ws, "skills"), "memory",
		"---\nname: memory\ndescription: ws-mem\nalways: true\n---\n",
		"# ws override\n")

	l := NewLoader(ws, builtin, nil)
	list, err := l.List()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("expected 2 skills, got %d", len(list))
	}
	// workspace should override builtin "memory"
	for _, e := range list {
		if e.Name == "memory" && e.Source != "workspace" {
			t.Fatalf("expected workspace override, got source=%s", e.Source)
		}
	}
	alw, _ := l.AlwaysSkills()
	if len(alw) != 1 || alw[0] != "memory" {
		t.Fatalf("expected always=[memory], got %v", alw)
	}

	// github should be listed as unavailable due to missing bin
	sum, _ := l.BuildSummary(nil)
	if sum == "" || !contains(sum, "unavailable") {
		t.Fatalf("expected unavailable mention, got %q", sum)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
