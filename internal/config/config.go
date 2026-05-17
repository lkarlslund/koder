package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/permissionprofile"
	toml "github.com/pelletier/go-toml/v2"
)

type UI struct {
	Theme           string `toml:"theme"`
	CodeStyle       string `toml:"code_style"`
	EditForgiveness int    `toml:"edit_forgiveness"`
	CursorBlink     bool   `toml:"cursor_blink"`
	HalfBlocks      bool   `toml:"half_blocks"`
	ShowSidebar     bool   `toml:"show_sidebar"`
	SidebarWidth    int    `toml:"sidebar_width"`
	ShowTimestamps  bool   `toml:"show_timestamps"`
	ShowReasoning   bool   `toml:"show_reasoning"`
	ShowSystem      bool   `toml:"show_system"`
	Mouse           bool   `toml:"mouse"`
	AutoContinue    bool   `toml:"auto_continue"`
}

type Store struct {
	Backend string `toml:"backend"`
}

type Provider struct {
	TemplateID    string            `toml:"template_id"`
	Kind          string            `toml:"kind"`
	AuthMethod    string            `toml:"auth_method"`
	Name          string            `toml:"name"`
	BaseURL       string            `toml:"base_url"`
	APIKey        string            `toml:"api_key"`
	APIKeyEnv     string            `toml:"api_key_env"`
	Headers       map[string]string `toml:"headers"`
	DefaultModel  string            `toml:"default_model"`
	ModelPreset   string            `toml:"model_preset"`
	ContextWindow int               `toml:"context_window"`
	AutoCompactAt int               `toml:"auto_compact_at"`
	Stream        bool              `toml:"stream"`
	Timeout       time.Duration     `toml:"timeout"`
	Disabled      bool              `toml:"disabled"`
}

type MCPServer struct {
	Name                 string            `toml:"name"`
	URL                  string            `toml:"url"`
	Headers              map[string]string `toml:"headers"`
	Disabled             bool              `toml:"disabled"`
	StartupTimeout       time.Duration     `toml:"startup_timeout"`
	RequestTimeout       time.Duration     `toml:"request_timeout"`
	DisableStandaloneSSE bool              `toml:"disable_standalone_sse"`
	BearerToken          string            `toml:"bearer_token"`
	BearerTokenEnv       string            `toml:"bearer_token_env"`
}

type PermissionRules = permissionprofile.Rules
type PermissionProfile = permissionprofile.Profile
type PermissionRule = permissionprofile.Rule

type Config struct {
	DefaultProvider           string                   `toml:"default_provider"`
	DefaultModel              string                   `toml:"default_model"`
	CompactionProvider        string                   `toml:"compaction_provider"`
	CompactionModel           string                   `toml:"compaction_model"`
	MaxToolLoopSteps          int                      `toml:"max_tool_loop_steps"`
	AutoCompactAt             int                      `toml:"auto_compact_at"`
	CompactionKeepToolBatches int                      `toml:"compaction_keep_tool_batches"`
	ToolDefaults              map[domain.ToolKind]bool `toml:"tool_defaults"`
	Providers                 map[string]Provider      `toml:"providers"`
	MCPServers                map[string]MCPServer     `toml:"mcp_servers"`
	Permissions               PermissionRules          `toml:"permissions"`
	Store                     Store                    `toml:"store"`
	UI                        UI                       `toml:"ui"`
	path                      string
	configDir                 string
	stateDir                  string
	cacheDir                  string
}

const providerConfigurationHint = "configure at least one provider in config.toml and set default_provider"
const defaultMaxToolLoopSteps = 500
const defaultAutoCompactAt = 80
const defaultCompactionKeepToolBatches = 2
const maxCompactionKeepToolBatches = 10

func Load() (Config, error) {
	cfg := Default()
	configDir := configDir()
	cfg.configDir = configDir
	cfg.stateDir = stateDir()
	cfg.cacheDir = cacheDir()
	cfg.path = filepath.Join(configDir, "config.toml")

	if err := os.MkdirAll(configDir, 0o755); err != nil {
		return Config{}, fmt.Errorf("create config dir: %w", err)
	}
	if err := os.MkdirAll(cfg.stateDir, 0o755); err != nil {
		return Config{}, fmt.Errorf("create state dir: %w", err)
	}
	if err := os.MkdirAll(cfg.cacheDir, 0o755); err != nil {
		return Config{}, fmt.Errorf("create cache dir: %w", err)
	}

	data, err := os.ReadFile(cfg.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			if err := cfg.Save(); err != nil {
				return Config{}, err
			}
			return cfg, nil
		}
		return Config{}, fmt.Errorf("read config: %w", err)
	}

	if err := toml.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("parse config: %w", err)
	}
	if !strings.Contains(string(data), "cursor_blink") {
		cfg.UI.CursorBlink = true
	}
	if !strings.Contains(string(data), "auto_continue") {
		cfg.UI.AutoContinue = true
	}
	if !strings.Contains(string(data), "auto_compact_at") {
		cfg.AutoCompactAt = defaultAutoCompactAt
	}
	if !strings.Contains(string(data), "compaction_keep_tool_batches") {
		cfg.CompactionKeepToolBatches = defaultCompactionKeepToolBatches
	}
	cfg.configDir = configDir
	cfg.stateDir = stateDir()
	cfg.cacheDir = cacheDir()
	cfg.path = filepath.Join(configDir, "config.toml")
	cfg.applyDefaults()
	return cfg, nil
}

func Default() Config {
	toolDefaults := make(map[domain.ToolKind]bool, len(domain.AllToolKinds()))
	for _, kind := range domain.AllToolKinds() {
		toolDefaults[kind] = true
	}
	return Config{
		DefaultProvider:           "",
		MaxToolLoopSteps:          defaultMaxToolLoopSteps,
		AutoCompactAt:             defaultAutoCompactAt,
		CompactionKeepToolBatches: defaultCompactionKeepToolBatches,
		ToolDefaults:              toolDefaults,
		Providers:                 map[string]Provider{},
		MCPServers:                map[string]MCPServer{},
		Permissions: PermissionRules{
			Profile: "default",
			Profiles: map[string]PermissionProfile{
				"default": {
					Root:      string(permissionprofile.ModeReadOnly),
					Workspace: string(permissionprofile.ModeReadWrite),
				},
				"readonly": {
					Root:      string(permissionprofile.ModeReadOnly),
					Workspace: string(permissionprofile.ModeReadOnly),
				},
				"dev-network": {
					Network:   true,
					Root:      string(permissionprofile.ModeReadOnly),
					Workspace: string(permissionprofile.ModeReadWrite),
				},
				"full-access": {
					Network:   true,
					Root:      string(permissionprofile.ModeReadWrite),
					Workspace: string(permissionprofile.ModeReadWrite),
				},
			},
		},
		Store: Store{
			Backend: "pebble",
		},
		UI: UI{
			Theme:           "tokyonight",
			CodeStyle:       "github",
			EditForgiveness: 3,
			CursorBlink:     true,
			HalfBlocks:      true,
			ShowSidebar:     true,
			ShowTimestamps:  false,
			ShowReasoning:   false,
			ShowSystem:      false,
			Mouse:           true,
			AutoContinue:    true,
		},
	}
}

func NormalizeEditForgiveness(level int) int {
	if level < 1 {
		return 1
	}
	if level > 5 {
		return 5
	}
	return level
}

func (c *Config) applyDefaults() {
	def := Default()
	if c.MaxToolLoopSteps <= 0 {
		c.MaxToolLoopSteps = def.MaxToolLoopSteps
	}
	if c.AutoCompactAt <= 0 {
		c.AutoCompactAt = def.AutoCompactAt
	}
	c.CompactionKeepToolBatches = NormalizeCompactionKeepToolBatches(c.CompactionKeepToolBatches)
	if c.Providers == nil {
		c.Providers = def.Providers
	}
	if c.MCPServers == nil {
		c.MCPServers = def.MCPServers
	}
	if c.ToolDefaults == nil {
		c.ToolDefaults = cloneToolDefaults(def.ToolDefaults)
	}
	for _, kind := range domain.AllToolKinds() {
		if _, ok := c.ToolDefaults[kind]; !ok {
			c.ToolDefaults[kind] = true
		}
	}
	if c.Permissions.Profile == "" {
		c.Permissions.Profile = def.Permissions.Profile
	}
	if c.Store.Backend == "" {
		c.Store.Backend = def.Store.Backend
	}
	if c.Permissions.Profiles == nil {
		c.Permissions.Profiles = cloneProfiles(def.Permissions.Profiles)
	}
	mergeBuiltinPermissionProfileDefaults(c.Permissions.Profiles, def.Permissions.Profiles)
	for name, profile := range c.Permissions.Profiles {
		profile.Rules = nil
		c.Permissions.Profiles[name] = permissionprofile.Normalize(profile)
	}
	if _, ok := c.Permissions.Profiles[c.Permissions.Profile]; !ok {
		c.Permissions.Profile = def.Permissions.Profile
	}
	if c.UI.Theme == "" {
		c.UI = def.UI
	}
	if c.UI.CodeStyle == "" {
		c.UI.CodeStyle = def.UI.CodeStyle
	}
	if c.UI.EditForgiveness == 0 {
		c.UI.EditForgiveness = def.UI.EditForgiveness
	} else {
		c.UI.EditForgiveness = NormalizeEditForgiveness(c.UI.EditForgiveness)
	}
	fallbackProvider := providerDefaults()
	for id, provider := range c.Providers {
		if provider.Kind == "" {
			provider.Kind = "openai-compatible"
		}
		if provider.Timeout == 0 {
			provider.Timeout = fallbackProvider.Timeout
		}
		if provider.ContextWindow == 0 {
			provider.ContextWindow = fallbackProvider.ContextWindow
		}
		if provider.AutoCompactAt == 0 {
			provider.AutoCompactAt = c.AutoCompactAt
		}
		if provider.Headers == nil {
			provider.Headers = map[string]string{}
		}
		c.Providers[id] = provider
	}
	for id, server := range c.MCPServers {
		if server.Headers == nil {
			server.Headers = map[string]string{}
		}
		if server.StartupTimeout <= 0 {
			server.StartupTimeout = mcpServerDefaults().StartupTimeout
		}
		if server.RequestTimeout <= 0 {
			server.RequestTimeout = mcpServerDefaults().RequestTimeout
		}
		c.MCPServers[id] = server
	}
}

func (c Config) Save() error {
	data, err := toml.Marshal(c)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	if err := os.WriteFile(c.path, data, 0o644); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	return nil
}

func (c Config) Path() string {
	return c.path
}

func (c Config) StateDir() string {
	return c.stateDir
}

func (c Config) WithStateDir(path string) Config {
	c.stateDir = strings.TrimSpace(path)
	return c
}

func (c Config) CacheDir() string {
	return c.cacheDir
}

func (c Config) Provider(id string) (Provider, bool) {
	id = strings.TrimSpace(id)
	if id == "" {
		return Provider{}, false
	}
	p, ok := c.Providers[id]
	if !ok {
		return Provider{}, false
	}
	if p.APIKey == "" && p.APIKeyEnv != "" {
		p.APIKey = strings.TrimSpace(os.Getenv(p.APIKeyEnv))
	}
	return p, true
}

func (c Config) MCPServer(id string) (MCPServer, bool) {
	id = strings.TrimSpace(id)
	if id == "" {
		return MCPServer{}, false
	}
	server, ok := c.MCPServers[id]
	if !ok {
		return MCPServer{}, false
	}
	if server.BearerToken == "" && server.BearerTokenEnv != "" {
		server.BearerToken = strings.TrimSpace(os.Getenv(server.BearerTokenEnv))
	}
	return server, true
}

func (c Config) RequireProvider() error {
	if len(c.Providers) == 0 {
		return fmt.Errorf("no providers configured; %s", providerConfigurationHint)
	}
	if strings.TrimSpace(c.DefaultProvider) == "" {
		return fmt.Errorf("default_provider is not set; %s", providerConfigurationHint)
	}
	if _, ok := c.Provider(c.DefaultProvider); !ok {
		return fmt.Errorf("default provider %q not configured; %s", c.DefaultProvider, providerConfigurationHint)
	}
	return nil
}

func (c Config) HasUsableProvider(id string) bool {
	provider, ok := c.Provider(id)
	if !ok {
		return false
	}
	return !provider.Disabled
}

func (c Config) HasUsableDefaultProvider() bool {
	return c.HasUsableProvider(c.DefaultProvider)
}

func providerDefaults() Provider {
	return Provider{
		Headers:       map[string]string{},
		ContextWindow: 32768,
		AutoCompactAt: defaultAutoCompactAt,
		Stream:        true,
		Timeout:       10 * time.Minute,
		Disabled:      false,
	}
}

func NormalizeCompactionKeepToolBatches(value int) int {
	if value < 0 {
		return 0
	}
	if value > maxCompactionKeepToolBatches {
		return maxCompactionKeepToolBatches
	}
	return value
}

func mcpServerDefaults() MCPServer {
	return MCPServer{
		Headers:        map[string]string{},
		StartupTimeout: 10 * time.Second,
		RequestTimeout: 30 * time.Second,
		Disabled:       false,
	}
}

func cloneProfiles(src map[string]PermissionProfile) map[string]PermissionProfile {
	dst := make(map[string]PermissionProfile, len(src))
	for name, profile := range src {
		rules := make([]PermissionRule, len(profile.Rules))
		copy(rules, profile.Rules)
		mounts := slices.Clone(profile.Mounts)
		dst[name] = PermissionProfile{
			Network:   profile.Network,
			Root:      profile.Root,
			Workspace: profile.Workspace,
			Mounts:    mounts,
			Rules:     rules,
		}
	}
	return dst
}

func cloneToolDefaults(src map[domain.ToolKind]bool) map[domain.ToolKind]bool {
	dst := make(map[domain.ToolKind]bool, len(src))
	for kind, enabled := range src {
		dst[kind] = enabled
	}
	return dst
}

func mergeBuiltinPermissionProfileDefaults(dst map[string]PermissionProfile, defaults map[string]PermissionProfile) {
	for name, defProfile := range defaults {
		existing, ok := dst[name]
		if !ok {
			dst[name] = permissionprofile.Normalize(defProfile)
			continue
		}
		if existing.Root == "" {
			existing.Root = defProfile.Root
		}
		if existing.Workspace == "" {
			existing.Workspace = defProfile.Workspace
		}
		if name == "dev-network" || name == permissionprofile.ProfileFullAccess {
			existing.Network = defProfile.Network
		}
		dst[name] = permissionprofile.Normalize(existing)
	}
}

func configDir() string {
	if dir := os.Getenv("XDG_CONFIG_HOME"); dir != "" {
		return filepath.Join(dir, "koder")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "koder")
}

func stateDir() string {
	if dir := os.Getenv("XDG_STATE_HOME"); dir != "" {
		return filepath.Join(dir, "koder")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "state", "koder")
}

func cacheDir() string {
	if dir := os.Getenv("XDG_CACHE_HOME"); dir != "" {
		return filepath.Join(dir, "koder")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".cache", "koder")
}
