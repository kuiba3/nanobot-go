package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// Load reads a config file from disk, resolves ${VAR} placeholders, overlays
// NANOBOT_* environment variables, and fills defaults. A missing file yields
// an empty config with defaults applied (not an error), matching Python behavior.
func Load(path string) (*Config, error) {
	if path == "" {
		path = DefaultConfigPath()
	}
	cfg := &Config{}
	data, err := os.ReadFile(path)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("read config: %w", err)
		}
	} else {
		resolved, err := expandEnvPlaceholders(data)
		if err != nil {
			return nil, err
		}
		if err := json.Unmarshal(resolved, cfg); err != nil {
			return nil, fmt.Errorf("parse config: %w", err)
		}
	}
	if err := applyEnvOverlay(cfg); err != nil {
		return nil, err
	}
	cfg.ApplyDefaults()
	return cfg, nil
}

// Save writes the config back as pretty-printed JSON.
func Save(path string, c *Config) error {
	if path == "" {
		path = DefaultConfigPath()
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

var envRe = regexp.MustCompile(`\$\{([A-Z_][A-Z0-9_]*)\}`)

// expandEnvPlaceholders replaces every ${VAR} in JSON *string values* only.
// Unset variables yield an error, matching Python resolve_config_env_vars.
func expandEnvPlaceholders(data []byte) ([]byte, error) {
	// Walk the JSON generically to avoid expanding keys or structural tokens.
	var root any
	if err := json.Unmarshal(data, &root); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	resolved, err := walkEnv(root)
	if err != nil {
		return nil, err
	}
	return json.Marshal(resolved)
}

func walkEnv(v any) (any, error) {
	switch t := v.(type) {
	case string:
		return expandStr(t)
	case []any:
		out := make([]any, len(t))
		for i, e := range t {
			r, err := walkEnv(e)
			if err != nil {
				return nil, err
			}
			out[i] = r
		}
		return out, nil
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, e := range t {
			r, err := walkEnv(e)
			if err != nil {
				return nil, err
			}
			out[k] = r
		}
		return out, nil
	default:
		return v, nil
	}
}

func expandStr(s string) (string, error) {
	var missing string
	out := envRe.ReplaceAllStringFunc(s, func(m string) string {
		name := m[2 : len(m)-1]
		val, ok := os.LookupEnv(name)
		if !ok {
			if missing == "" {
				missing = name
			}
			return ""
		}
		return val
	})
	if missing != "" {
		return "", fmt.Errorf("config references unset env var %q", missing)
	}
	return out, nil
}

// applyEnvOverlay allows NANOBOT_<PATH>__<PATH2> to override config fields.
// The path components are lowercased and matched against JSON tag names (snake
// or camel) via a reflect-free textual overlay on a JSON round trip.
// Practical coverage for MVP: scalars on agents.defaults, api, gateway, tools.
func applyEnvOverlay(c *Config) error {
	const prefix = "NANOBOT_"
	for _, env := range os.Environ() {
		eq := strings.IndexByte(env, '=')
		if eq <= 0 {
			continue
		}
		key := env[:eq]
		val := env[eq+1:]
		if !strings.HasPrefix(key, prefix) {
			continue
		}
		path := strings.Split(strings.TrimPrefix(key, prefix), "__")
		if err := setByPath(c, path, val); err != nil {
			return fmt.Errorf("env overlay %s: %w", key, err)
		}
	}
	return nil
}

// setByPath is a small, explicit switch covering the most useful fields.
// Rather than reflect-walking arbitrary paths it hard-codes the subset that
// real deployments override via env.
func setByPath(c *Config, parts []string, val string) error {
	lc := make([]string, len(parts))
	for i, p := range parts {
		lc[i] = strings.ToUpper(p)
	}
	join := strings.Join(lc, "__")
	switch join {
	case "AGENTS__DEFAULTS__MODEL":
		c.Agents.Defaults.Model = val
	case "AGENTS__DEFAULTS__PROVIDER":
		c.Agents.Defaults.Provider = val
	case "AGENTS__DEFAULTS__WORKSPACE":
		c.Agents.Defaults.Workspace = val
	case "AGENTS__DEFAULTS__MAXTOKENS":
		n, err := strconv.Atoi(val)
		if err != nil {
			return err
		}
		c.Agents.Defaults.MaxTokens = n
	case "AGENTS__DEFAULTS__TEMPERATURE":
		f, err := strconv.ParseFloat(val, 64)
		if err != nil {
			return err
		}
		c.Agents.Defaults.Temperature = f
	case "AGENTS__DEFAULTS__MAXTOOLITERATIONS":
		n, err := strconv.Atoi(val)
		if err != nil {
			return err
		}
		c.Agents.Defaults.MaxToolIterations = n
	case "AGENTS__DEFAULTS__REASONINGEFFORT":
		c.Agents.Defaults.ReasoningEffort = val
	case "AGENTS__DEFAULTS__TIMEZONE":
		c.Agents.Defaults.Timezone = val
	case "API__HOST":
		c.API.Host = val
	case "API__PORT":
		n, err := strconv.Atoi(val)
		if err != nil {
			return err
		}
		c.API.Port = n
	case "API__TIMEOUT":
		n, err := strconv.Atoi(val)
		if err != nil {
			return err
		}
		c.API.Timeout = n
	case "GATEWAY__HOST":
		c.Gateway.Host = val
	case "GATEWAY__PORT":
		n, err := strconv.Atoi(val)
		if err != nil {
			return err
		}
		c.Gateway.Port = n
	case "TOOLS__RESTRICTTOWORKSPACE":
		c.Tools.RestrictToWorkspace = parseBool(val)
	}
	// Provider credentials: NANOBOT_PROVIDERS__<NAME>__APIKEY etc.
	if len(lc) == 3 && lc[0] == "PROVIDERS" {
		if c.Providers == nil {
			c.Providers = make(ProvidersConfig)
		}
		name := strings.ToLower(parts[1])
		cred := c.Providers[name]
		switch lc[2] {
		case "APIKEY":
			cred.APIKey = val
		case "APIBASE":
			cred.APIBase = val
		}
		c.Providers[name] = cred
	}
	return nil
}

func parseBool(s string) bool {
	switch strings.ToLower(s) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}
