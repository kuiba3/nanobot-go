package provider

import (
	"fmt"
	"strings"
)

// Spec identifies a known provider backend by name/keyword prefixes on the
// model string. Only the MVP-required backends are registered here; extending
// the registry is a matter of appending Spec entries.
type Spec struct {
	Name     string   // "openai", "anthropic"
	Backend  string   // "openai_compat" | "anthropic"
	Keywords []string // lower-case prefixes matching model names
	APIBase  string   // default API base if none configured
}

// KnownProviders is the built-in registry.
var KnownProviders = []Spec{
	{Name: "openai", Backend: "openai_compat", Keywords: []string{"gpt-", "o1", "o3", "o4", "chatgpt-"}, APIBase: "https://api.openai.com/v1"},
	{Name: "anthropic", Backend: "anthropic", Keywords: []string{"claude-"}, APIBase: "https://api.anthropic.com"},
	{Name: "openrouter", Backend: "openai_compat", APIBase: "https://openrouter.ai/api/v1"},
	{Name: "groq", Backend: "openai_compat", APIBase: "https://api.groq.com/openai/v1"},
	{Name: "together", Backend: "openai_compat", APIBase: "https://api.together.xyz/v1"},
	{Name: "deepseek", Backend: "openai_compat", Keywords: []string{"deepseek-"}, APIBase: "https://api.deepseek.com/v1"},
	{Name: "moonshot", Backend: "openai_compat", Keywords: []string{"kimi-", "moonshot-"}, APIBase: "https://api.moonshot.cn/v1"},
	{Name: "qwen", Backend: "openai_compat", Keywords: []string{"qwen-"}, APIBase: "https://dashscope.aliyuncs.com/compatible-mode/v1"},
	{Name: "ollama", Backend: "openai_compat", APIBase: "http://localhost:11434/v1"},
}

// FindByName returns the spec with the given name (case/space insensitive).
func FindByName(name string) *Spec {
	n := strings.ToLower(strings.TrimSpace(name))
	for i := range KnownProviders {
		if KnownProviders[i].Name == n {
			return &KnownProviders[i]
		}
	}
	return nil
}

// DetectByModel best-effort picks a spec whose keywords prefix-match the model.
func DetectByModel(model string) *Spec {
	m := strings.ToLower(model)
	for i := range KnownProviders {
		for _, kw := range KnownProviders[i].Keywords {
			if strings.HasPrefix(m, kw) {
				return &KnownProviders[i]
			}
		}
	}
	return nil
}

// Resolve chooses a provider Spec. `name` takes precedence; otherwise falls
// back to keyword detection by model; otherwise openai_compat with unknown base.
func Resolve(name, model string) *Spec {
	if s := FindByName(name); s != nil {
		return s
	}
	if s := DetectByModel(model); s != nil {
		return s
	}
	return &Spec{Name: "openai", Backend: "openai_compat", APIBase: "https://api.openai.com/v1"}
}

// BuildParams bundles what the concrete constructor needs.
type BuildParams struct {
	Spec         *Spec
	APIKey       string
	APIBase      string
	DefaultModel string
	ExtraHeaders map[string]string
	Generation   GenerationSettings
}

// Mismatch returns a helpful error when the selected backend lacks a required
// credential; for the rest, the build is delegated to the concrete provider
// packages by importing them explicitly in the composition root.
func (b BuildParams) Mismatch() error {
	switch b.Spec.Backend {
	case "openai_compat":
		if b.APIKey == "" && !strings.Contains(b.Spec.APIBase, "localhost") {
			return fmt.Errorf("provider %q requires apiKey", b.Spec.Name)
		}
	case "anthropic":
		if b.APIKey == "" {
			return fmt.Errorf("provider %q requires apiKey", b.Spec.Name)
		}
	}
	return nil
}
