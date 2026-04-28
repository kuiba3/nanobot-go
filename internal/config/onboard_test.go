package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kuiba3/nanobot-go/internal/provider"
)

func TestEnrichOnboardProviderDefaults(t *testing.T) {
	cfg := &Config{}
	cfg.ApplyDefaults()
	cfg.Agents.Defaults.Model = "gpt-4o-mini"
	cfg.Agents.Defaults.Provider = "openai"
	EnrichOnboardProviderDefaults(cfg)

	if _, ok := cfg.Providers["openai"]; !ok {
		t.Fatal("expected openai provider")
	}
	if want := "https://api.openai.com/v1"; cfg.Providers["openai"].APIBase != want {
		t.Fatalf("openai apiBase: want %q, got %q", want, cfg.Providers["openai"].APIBase)
	}
	if cfg.Providers["anthropic"].APIBase != "https://api.anthropic.com" {
		t.Fatalf("anthropic apiBase: got %q", cfg.Providers["anthropic"].APIBase)
	}
	cfg2 := &Config{}
	cfg2.ApplyDefaults()
	cfg2.Agents.Defaults.Model = "claude-opus-4-0"
	cfg2.Agents.Defaults.Provider = "anthropic"
	EnrichOnboardProviderDefaults(cfg2)
	if got := provider.Resolve("anthropic", "claude-opus-4-0").APIBase; cfg2.Providers["anthropic"].APIBase != got {
		t.Fatalf("anthropic as default: want apiBase %q, got %q", got, cfg2.Providers["anthropic"].APIBase)
	}
}

func TestSaveOnboardIncludesAPIKeys(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	cfg, err := Load(filepath.Join(dir, "absent.json"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Agents.Defaults.Model == "" {
		cfg.Agents.Defaults.Model = "gpt-4o-mini"
	}
	if cfg.Agents.Defaults.Provider == "" {
		cfg.Agents.Defaults.Provider = "openai"
	}
	EnrichOnboardProviderDefaults(cfg)
	if err := Save(path, cfg); err != nil {
		t.Fatalf("save: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"apiKey"`) || !strings.Contains(string(data), `"apiBase"`) {
		t.Fatalf("expected apiKey and apiBase in file: %s", data)
	}
}
