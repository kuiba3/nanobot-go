package config

import "github.com/kuiba3/nanobot-go/internal/provider"

// OnboardProviderNames is the set of provider entries we seed into a fresh
// config so users can edit apiKey and apiBase in one place. Must stay in sync
// with the providers block in config.sample.json.
var OnboardProviderNames = []string{"openai", "anthropic", "openrouter"}

// EnrichOnboardProviderDefaults ensures providers.<name> exists with apiKey and
// apiBase written for Save(). Empty apiKey is left as "" for the user to fill;
// apiBase is set to the known provider default when missing so the file shows
// the public endpoint users may override.
func EnrichOnboardProviderDefaults(cfg *Config) {
	if cfg.Providers == nil {
		cfg.Providers = make(ProvidersConfig)
	}
	model := cfg.Agents.Defaults.Model
	provName := cfg.Agents.Defaults.Provider
	if provName == "" {
		provName = "openai"
	}
	for _, name := range OnboardProviderNames {
		spec := provider.FindByName(name)
		if spec == nil {
			continue
		}
		cred, ok := cfg.Providers[name]
		if !ok {
			cred = ProviderCredentials{}
		}
		if cred.APIBase == "" {
			if name == provName {
				cred.APIBase = provider.Resolve(provName, model).APIBase
			} else {
				cred.APIBase = spec.APIBase
			}
		}
		cfg.Providers[name] = cred
	}
}
