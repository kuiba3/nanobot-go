package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadMissingFileReturnsDefaults(t *testing.T) {
	cfg, err := Load(filepath.Join(t.TempDir(), "does-not-exist.json"))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Agents.Defaults.MaxTokens != 4096 {
		t.Fatalf("expected default maxTokens=4096, got %d", cfg.Agents.Defaults.MaxTokens)
	}
	if cfg.API.Port != 8900 {
		t.Fatalf("expected default api.port=8900, got %d", cfg.API.Port)
	}
}

func TestLoadEnvPlaceholder(t *testing.T) {
	t.Setenv("NANOBOT_TEST_KEY", "sk-xyz")
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{"providers":{"openai":{"apiKey":"${NANOBOT_TEST_KEY}"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Providers["openai"].APIKey != "sk-xyz" {
		t.Fatalf("expected resolved key, got %q", cfg.Providers["openai"].APIKey)
	}
}

func TestEnvOverlayAgentsModel(t *testing.T) {
	t.Setenv("NANOBOT_AGENTS__DEFAULTS__MODEL", "claude-opus-4-7")
	cfg, err := Load(filepath.Join(t.TempDir(), "missing.json"))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Agents.Defaults.Model != "claude-opus-4-7" {
		t.Fatalf("expected model override, got %q", cfg.Agents.Defaults.Model)
	}
}

func TestEnvOverlayProviderCredentials(t *testing.T) {
	t.Setenv("NANOBOT_PROVIDERS__ANTHROPIC__APIKEY", "sk-ant-xxx")
	cfg, err := Load(filepath.Join(t.TempDir(), "missing.json"))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Providers["anthropic"].APIKey != "sk-ant-xxx" {
		t.Fatalf("expected provider key, got %q", cfg.Providers["anthropic"].APIKey)
	}
}

func TestSaveRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	cfg := &Config{
		Agents: AgentsConfig{Defaults: AgentDefaults{Model: "gpt-4o-mini", Temperature: 0.3}},
	}
	cfg.ApplyDefaults()
	if err := Save(path, cfg); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got.Agents.Defaults.Model != "gpt-4o-mini" {
		t.Fatalf("round trip lost model: %+v", got.Agents.Defaults)
	}
}

func TestMissingEnvVarReturnsError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{"providers":{"openai":{"apiKey":"${__NANOBOT_DEFINITELY_MISSING__}"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("expected error for missing env var")
	}
}
