// Package config defines the nanobot configuration schema and loader.
// Mirrors Python nanobot/config/schema.py with camelCase JSON keys.
package config

// Config is the root config object serialized to ~/.nanobot/config.json.
type Config struct {
	Agents    AgentsConfig    `json:"agents"`
	Channels  ChannelsConfig  `json:"channels"`
	Providers ProvidersConfig `json:"providers"`
	API       APIConfig       `json:"api"`
	Gateway   GatewayConfig   `json:"gateway"`
	Tools     ToolsConfig     `json:"tools"`
}

// AgentsConfig groups agent-level settings.
type AgentsConfig struct {
	Defaults AgentDefaults `json:"defaults"`
}

// AgentDefaults is the per-agent runtime configuration.
type AgentDefaults struct {
	Workspace            string      `json:"workspace,omitempty"`
	Model                string      `json:"model,omitempty"`
	Provider             string      `json:"provider,omitempty"`
	MaxTokens            int         `json:"maxTokens,omitempty"`
	ContextWindowTokens  int         `json:"contextWindowTokens,omitempty"`
	ContextBlockLimit    int         `json:"contextBlockLimit,omitempty"`
	Temperature          float64     `json:"temperature,omitempty"`
	MaxToolIterations    int         `json:"maxToolIterations,omitempty"`
	MaxToolResultChars   int         `json:"maxToolResultChars,omitempty"`
	ProviderRetryMode    string      `json:"providerRetryMode,omitempty"`
	ReasoningEffort      string      `json:"reasoningEffort,omitempty"`
	Timezone             string      `json:"timezone,omitempty"`
	UnifiedSession       bool        `json:"unifiedSession,omitempty"`
	DisabledSkills       []string    `json:"disabledSkills,omitempty"`
	SessionTTLMinutes    int         `json:"sessionTtlMinutes,omitempty"`
	Dream                DreamConfig `json:"dream,omitempty"`
}

// DreamConfig configures the Dream background consolidation process.
type DreamConfig struct {
	IntervalH        float64 `json:"intervalH,omitempty"`
	Cron             string  `json:"cron,omitempty"`
	ModelOverride    string  `json:"modelOverride,omitempty"`
	MaxBatchSize     int     `json:"maxBatchSize,omitempty"`
	MaxIterations    int     `json:"maxIterations,omitempty"`
	AnnotateLineAges bool    `json:"annotateLineAges,omitempty"`
}

// ChannelsConfig holds shared channel flags plus per-channel arbitrary sub-trees.
// Extra fields are preserved via Extra so webhook/telegram/etc. configs survive
// a roundtrip without knowing their shape.
type ChannelsConfig struct {
	SendProgress           bool           `json:"sendProgress,omitempty"`
	SendToolHints          bool           `json:"sendToolHints,omitempty"`
	SendMaxRetries         int            `json:"sendMaxRetries,omitempty"`
	TranscriptionProvider  string         `json:"transcriptionProvider,omitempty"`
	WebSocket              WebSocketConf  `json:"websocket,omitempty"`
	API                    APIChannelConf `json:"api,omitempty"`
	Extra                  map[string]any `json:"-"`
}

// WebSocketConf mirrors the Python WebSocketConfig subset needed by MVP.
type WebSocketConf struct {
	Enabled          bool     `json:"enabled,omitempty"`
	Host             string   `json:"host,omitempty"`
	Port             int      `json:"port,omitempty"`
	Path             string   `json:"path,omitempty"`
	AllowFrom        []string `json:"allowFrom,omitempty"`
	Streaming        bool     `json:"streaming,omitempty"`
	Token            string   `json:"token,omitempty"`
	TokenIssuePath   string   `json:"tokenIssuePath,omitempty"`
	TokenIssueSecret string   `json:"tokenIssueSecret,omitempty"`
	StaticDir        string   `json:"staticDir,omitempty"`
	PingIntervalS    int      `json:"pingIntervalS,omitempty"`
	PingTimeoutS     int      `json:"pingTimeoutS,omitempty"`
	MaxMessageBytes  int      `json:"maxMessageBytes,omitempty"`
}

// APIChannelConf toggles whether the OpenAI-compatible HTTP API channel should
// be registered as an Egress (most users enable it via `nanobot serve`).
type APIChannelConf struct {
	Enabled   bool     `json:"enabled,omitempty"`
	Streaming bool     `json:"streaming,omitempty"`
	AllowFrom []string `json:"allowFrom,omitempty"`
}

// ProvidersConfig is a map of provider name -> credentials + endpoint overrides.
type ProvidersConfig map[string]ProviderCredentials

// ProviderCredentials is what users fill per provider in config.json.
// apiKey and apiBase use plain json tags (no omitempty) so that `nanobot onboard`
// and Save round-trips can keep these fields visible for hand-editing; empty
// apiBase is ignored at runtime in favor of the provider's built-in default.
type ProviderCredentials struct {
	APIKey       string            `json:"apiKey"`
	APIBase      string            `json:"apiBase"`
	ExtraHeaders map[string]string `json:"extraHeaders,omitempty"`
}

// APIConfig controls the standalone OpenAI-compat HTTP server (`nanobot serve`).
type APIConfig struct {
	Host    string `json:"host,omitempty"`
	Port    int    `json:"port,omitempty"`
	Timeout int    `json:"timeout,omitempty"`
}

// GatewayConfig controls the long-running gateway + WebUI server.
type GatewayConfig struct {
	Host      string          `json:"host,omitempty"`
	Port      int             `json:"port,omitempty"`
	Heartbeat HeartbeatConfig `json:"heartbeat,omitempty"`
}

// HeartbeatConfig controls the HEARTBEAT.md periodic loop.
type HeartbeatConfig struct {
	Enabled            bool `json:"enabled,omitempty"`
	IntervalS          int  `json:"intervalS,omitempty"`
	KeepRecentMessages int  `json:"keepRecentMessages,omitempty"`
}

// ToolsConfig groups built-in tool settings.
type ToolsConfig struct {
	Web                 WebToolConfig      `json:"web,omitempty"`
	Exec                ExecToolConfig     `json:"exec,omitempty"`
	My                  MyToolConfig       `json:"my,omitempty"`
	RestrictToWorkspace bool               `json:"restrictToWorkspace,omitempty"`
	MCPServers          map[string]MCPConf `json:"mcpServers,omitempty"`
	SSRFWhitelist       []string           `json:"ssrfWhitelist,omitempty"`
}

// WebToolConfig configures web search + fetch.
type WebToolConfig struct {
	Enable   bool              `json:"enable,omitempty"`
	Provider string            `json:"provider,omitempty"`
	APIKey   string            `json:"apiKey,omitempty"`
	Proxy    string            `json:"proxy,omitempty"`
	Options  map[string]any    `json:"options,omitempty"`
	Headers  map[string]string `json:"headers,omitempty"`
}

// ExecToolConfig configures the shell / exec tool.
type ExecToolConfig struct {
	Enable           bool     `json:"enable,omitempty"`
	TimeoutS         int      `json:"timeoutS,omitempty"`
	MaxOutputChars   int      `json:"maxOutputChars,omitempty"`
	Sandbox          bool     `json:"sandbox,omitempty"`
	DenyPatterns     []string `json:"denyPatterns,omitempty"`
	AllowPatterns    []string `json:"allowPatterns,omitempty"`
	AllowedEnvKeys   []string `json:"allowedEnvKeys,omitempty"`
}

// MyToolConfig controls the self-introspection tool.
type MyToolConfig struct {
	Enable bool `json:"enable,omitempty"`
}

// MCPConf configures a single MCP server.
type MCPConf struct {
	Type          string            `json:"type,omitempty"` // "stdio" | "sse" | "http"
	Command       string            `json:"command,omitempty"`
	Args          []string          `json:"args,omitempty"`
	Env           map[string]string `json:"env,omitempty"`
	URL           string            `json:"url,omitempty"`
	Headers       map[string]string `json:"headers,omitempty"`
	EnabledTools  []string          `json:"enabledTools,omitempty"`
	Timeout       int               `json:"timeout,omitempty"`
}

// ApplyDefaults fills zero-valued fields with sensible defaults so the rest of
// the system never has to check for zero. Mirrors Python Pydantic defaults.
func (c *Config) ApplyDefaults() {
	if c.Agents.Defaults.MaxTokens == 0 {
		c.Agents.Defaults.MaxTokens = 4096
	}
	if c.Agents.Defaults.Temperature == 0 {
		c.Agents.Defaults.Temperature = 0.7
	}
	if c.Agents.Defaults.MaxToolIterations == 0 {
		c.Agents.Defaults.MaxToolIterations = 30
	}
	if c.Agents.Defaults.ContextWindowTokens == 0 {
		c.Agents.Defaults.ContextWindowTokens = 128000
	}
	if c.Agents.Defaults.ContextBlockLimit == 0 {
		c.Agents.Defaults.ContextBlockLimit = 8000
	}
	if c.Agents.Defaults.MaxToolResultChars == 0 {
		c.Agents.Defaults.MaxToolResultChars = 12000
	}
	if c.API.Host == "" {
		c.API.Host = "127.0.0.1"
	}
	if c.API.Port == 0 {
		c.API.Port = 8900
	}
	if c.API.Timeout == 0 {
		c.API.Timeout = 120
	}
	if c.Gateway.Host == "" {
		c.Gateway.Host = "127.0.0.1"
	}
	if c.Gateway.Port == 0 {
		c.Gateway.Port = 18790
	}
	if !c.Gateway.Heartbeat.Enabled && c.Gateway.Heartbeat.IntervalS == 0 {
		c.Gateway.Heartbeat.IntervalS = 1800
		c.Gateway.Heartbeat.KeepRecentMessages = 8
	}
	if c.Channels.SendMaxRetries == 0 {
		c.Channels.SendMaxRetries = 3
	}
	if c.Channels.WebSocket.Port == 0 {
		c.Channels.WebSocket.Port = 18790
	}
	if c.Channels.WebSocket.Path == "" {
		c.Channels.WebSocket.Path = "/ws"
	}
	if c.Channels.WebSocket.PingIntervalS == 0 {
		c.Channels.WebSocket.PingIntervalS = 20
	}
	if c.Channels.WebSocket.PingTimeoutS == 0 {
		c.Channels.WebSocket.PingTimeoutS = 20
	}
	if c.Channels.WebSocket.MaxMessageBytes == 0 {
		c.Channels.WebSocket.MaxMessageBytes = 1 << 20 // 1 MiB
	}
}
