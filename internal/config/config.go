package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/lkarlslund/koder/internal/domain"
	toml "github.com/pelletier/go-toml/v2"
)

type UI struct {
	Theme          string `toml:"theme"`
	Spinner        string `toml:"spinner"`
	HalfBlocks     bool   `toml:"half_blocks"`
	ShowSidebar    bool   `toml:"show_sidebar"`
	ShowTimestamps bool   `toml:"show_timestamps"`
	ShowReasoning  bool   `toml:"show_reasoning"`
	Mouse          bool   `toml:"mouse"`
}

type Store struct {
	Backend string `toml:"backend"`
}

type Provider struct {
	Kind          string            `toml:"kind"`
	AuthMethod    string            `toml:"auth_method"`
	Name          string            `toml:"name"`
	BaseURL       string            `toml:"base_url"`
	APIKey        string            `toml:"api_key"`
	APIKeyEnv     string            `toml:"api_key_env"`
	Headers       map[string]string `toml:"headers"`
	DefaultModel  string            `toml:"default_model"`
	ContextWindow int               `toml:"context_window"`
	AutoCompactAt int               `toml:"auto_compact_at"`
	Stream        bool              `toml:"stream"`
	Timeout       time.Duration     `toml:"timeout"`
	Disabled      bool              `toml:"disabled"`
}

type PermissionRules struct {
	Profile  string                       `toml:"profile"`
	Profiles map[string]PermissionProfile `toml:"profiles"`

	Read       domain.PermissionMode `toml:"read"`
	Glob       domain.PermissionMode `toml:"glob"`
	Grep       domain.PermissionMode `toml:"grep"`
	Bash       domain.PermissionMode `toml:"bash"`
	ApplyPatch domain.PermissionMode `toml:"apply_patch"`
	Task       domain.PermissionMode `toml:"task"`
	Question   domain.PermissionMode `toml:"question"`
	WebFetch   domain.PermissionMode `toml:"webfetch"`
	WebSearch  domain.PermissionMode `toml:"websearch"`
}

type PermissionProfile struct {
	Rules []PermissionRule `toml:"rules"`
}

type PermissionRule struct {
	Tool    domain.ToolKind       `toml:"tool"`
	Pattern string                `toml:"pattern"`
	Action  domain.PermissionMode `toml:"action"`
}

type Config struct {
	DefaultProvider  string              `toml:"default_provider"`
	DefaultModel     string              `toml:"default_model"`
	MaxToolLoopSteps int                 `toml:"max_tool_loop_steps"`
	Providers        map[string]Provider `toml:"providers"`
	Permissions      PermissionRules     `toml:"permissions"`
	Store            Store               `toml:"store"`
	UI               UI                  `toml:"ui"`
	path             string
	configDir        string
	stateDir         string
	cacheDir         string
}

const providerConfigurationHint = "configure at least one provider in config.toml and set default_provider"
const defaultMaxToolLoopSteps = 20

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
	cfg.configDir = configDir
	cfg.stateDir = stateDir()
	cfg.cacheDir = cacheDir()
	cfg.path = filepath.Join(configDir, "config.toml")
	cfg.applyDefaults()
	return cfg, nil
}

func Default() Config {
	return Config{
		DefaultProvider:  "",
		MaxToolLoopSteps: defaultMaxToolLoopSteps,
		Providers:        map[string]Provider{},
		Permissions: PermissionRules{
			Profile: "default",
			Profiles: map[string]PermissionProfile{
				"default": {
					Rules: []PermissionRule{
						{Tool: domain.ToolKindRead, Pattern: "*", Action: domain.PermissionModeAllow},
						{Tool: domain.ToolKindGlob, Pattern: "*", Action: domain.PermissionModeAllow},
						{Tool: domain.ToolKindGrep, Pattern: "*", Action: domain.PermissionModeAllow},
						{Tool: domain.ToolKindBash, Pattern: "*", Action: domain.PermissionModeAsk},
						{Tool: domain.ToolKindApplyPatch, Pattern: "*", Action: domain.PermissionModeAsk},
						{Tool: domain.ToolKindEdit, Pattern: "*", Action: domain.PermissionModeAsk},
						{Tool: domain.ToolKindWrite, Pattern: "*", Action: domain.PermissionModeAsk},
						{Tool: domain.ToolKindTask, Pattern: "*", Action: domain.PermissionModeAsk},
						{Tool: domain.ToolKindQuestion, Pattern: "*", Action: domain.PermissionModeAsk},
						{Tool: domain.ToolKindUpdatePlan, Pattern: "*", Action: domain.PermissionModeAllow},
						{Tool: domain.ToolKindSkill, Pattern: "*", Action: domain.PermissionModeAsk},
						{Tool: domain.ToolKindWebFetch, Pattern: "*", Action: domain.PermissionModeAsk},
						{Tool: domain.ToolKindWebSearch, Pattern: "*", Action: domain.PermissionModeAsk},
					},
				},
				"readonly": {
					Rules: []PermissionRule{
						{Tool: domain.ToolKindRead, Pattern: "*", Action: domain.PermissionModeAllow},
						{Tool: domain.ToolKindGlob, Pattern: "*", Action: domain.PermissionModeAllow},
						{Tool: domain.ToolKindGrep, Pattern: "*", Action: domain.PermissionModeAllow},
						{Tool: domain.ToolKindBash, Pattern: "*", Action: domain.PermissionModeDeny},
						{Tool: domain.ToolKindApplyPatch, Pattern: "*", Action: domain.PermissionModeDeny},
						{Tool: domain.ToolKindEdit, Pattern: "*", Action: domain.PermissionModeDeny},
						{Tool: domain.ToolKindWrite, Pattern: "*", Action: domain.PermissionModeDeny},
						{Tool: domain.ToolKindTask, Pattern: "*", Action: domain.PermissionModeAsk},
						{Tool: domain.ToolKindQuestion, Pattern: "*", Action: domain.PermissionModeAsk},
						{Tool: domain.ToolKindUpdatePlan, Pattern: "*", Action: domain.PermissionModeAllow},
						{Tool: domain.ToolKindSkill, Pattern: "*", Action: domain.PermissionModeAsk},
						{Tool: domain.ToolKindWebFetch, Pattern: "*", Action: domain.PermissionModeAsk},
						{Tool: domain.ToolKindWebSearch, Pattern: "*", Action: domain.PermissionModeAsk},
					},
				},
				"auto": {
					Rules: []PermissionRule{
						{Tool: domain.ToolKindRead, Pattern: "*", Action: domain.PermissionModeAllow},
						{Tool: domain.ToolKindGlob, Pattern: "*", Action: domain.PermissionModeAllow},
						{Tool: domain.ToolKindGrep, Pattern: "*", Action: domain.PermissionModeAllow},
						{Tool: domain.ToolKindBash, Pattern: "*", Action: domain.PermissionModeAllow},
						{Tool: domain.ToolKindApplyPatch, Pattern: "*", Action: domain.PermissionModeAllow},
						{Tool: domain.ToolKindEdit, Pattern: "*", Action: domain.PermissionModeAllow},
						{Tool: domain.ToolKindWrite, Pattern: "*", Action: domain.PermissionModeAllow},
						{Tool: domain.ToolKindTask, Pattern: "*", Action: domain.PermissionModeAllow},
						{Tool: domain.ToolKindQuestion, Pattern: "*", Action: domain.PermissionModeAllow},
						{Tool: domain.ToolKindUpdatePlan, Pattern: "*", Action: domain.PermissionModeAllow},
						{Tool: domain.ToolKindSkill, Pattern: "*", Action: domain.PermissionModeAllow},
						{Tool: domain.ToolKindWebFetch, Pattern: "*", Action: domain.PermissionModeAllow},
						{Tool: domain.ToolKindWebSearch, Pattern: "*", Action: domain.PermissionModeAsk},
					},
				},
			},
		},
		Store: Store{
			Backend: "pebble",
		},
		UI: UI{
			Theme:          "tokyonight",
			Spinner:        "dots",
			HalfBlocks:     true,
			ShowSidebar:    true,
			ShowTimestamps: false,
			ShowReasoning:  false,
			Mouse:          true,
		},
	}
}

func (c *Config) applyDefaults() {
	def := Default()
	if c.MaxToolLoopSteps <= 0 {
		c.MaxToolLoopSteps = def.MaxToolLoopSteps
	}
	if c.Providers == nil {
		c.Providers = def.Providers
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
	if hasLegacyPermissions(c.Permissions) {
		c.Permissions.Profiles["default"] = legacyPermissionProfile(c.Permissions)
	}
	if _, ok := c.Permissions.Profiles[c.Permissions.Profile]; !ok {
		c.Permissions.Profile = def.Permissions.Profile
	}
	if c.UI.Theme == "" {
		c.UI = def.UI
	}
	if c.UI.Spinner == "" {
		c.UI.Spinner = def.UI.Spinner
	}
	fallbackProvider := providerDefaults()
	for id, provider := range c.Providers {
		if provider.Kind == "" {
			provider.Kind = "openai-compatible"
		}
		if provider.AuthMethod == "" {
			if strings.TrimSpace(provider.APIKey) != "" || strings.TrimSpace(provider.APIKeyEnv) != "" {
				provider.AuthMethod = "api_key"
			} else {
				provider.AuthMethod = "local_endpoint"
			}
		}
		if provider.Timeout == 0 {
			provider.Timeout = fallbackProvider.Timeout
		}
		if provider.ContextWindow == 0 {
			provider.ContextWindow = fallbackProvider.ContextWindow
		}
		if provider.AutoCompactAt == 0 {
			provider.AutoCompactAt = fallbackProvider.AutoCompactAt
		}
		if provider.Headers == nil {
			provider.Headers = map[string]string{}
		}
		c.Providers[id] = provider
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
		AutoCompactAt: 85,
		Stream:        true,
		Timeout:       2 * time.Minute,
		Disabled:      false,
	}
}

func cloneProfiles(src map[string]PermissionProfile) map[string]PermissionProfile {
	dst := make(map[string]PermissionProfile, len(src))
	for name, profile := range src {
		rules := make([]PermissionRule, len(profile.Rules))
		copy(rules, profile.Rules)
		dst[name] = PermissionProfile{Rules: rules}
	}
	return dst
}

func hasLegacyPermissions(rules PermissionRules) bool {
	return rules.Read != "" ||
		rules.Glob != "" ||
		rules.Grep != "" ||
		rules.Bash != "" ||
		rules.ApplyPatch != "" ||
		rules.Task != "" ||
		rules.Question != "" ||
		rules.WebFetch != "" ||
		rules.WebSearch != ""
}

func legacyPermissionProfile(rules PermissionRules) PermissionProfile {
	return PermissionProfile{
		Rules: []PermissionRule{
			{Tool: domain.ToolKindRead, Pattern: "*", Action: firstPermission(rules.Read, domain.PermissionModeAllow)},
			{Tool: domain.ToolKindGlob, Pattern: "*", Action: firstPermission(rules.Glob, domain.PermissionModeAllow)},
			{Tool: domain.ToolKindGrep, Pattern: "*", Action: firstPermission(rules.Grep, domain.PermissionModeAllow)},
			{Tool: domain.ToolKindBash, Pattern: "*", Action: firstPermission(rules.Bash, domain.PermissionModeAsk)},
			{Tool: domain.ToolKindApplyPatch, Pattern: "*", Action: firstPermission(rules.ApplyPatch, domain.PermissionModeAsk)},
			{Tool: domain.ToolKindTask, Pattern: "*", Action: firstPermission(rules.Task, domain.PermissionModeAsk)},
			{Tool: domain.ToolKindQuestion, Pattern: "*", Action: firstPermission(rules.Question, domain.PermissionModeAsk)},
			{Tool: domain.ToolKindWebFetch, Pattern: "*", Action: firstPermission(rules.WebFetch, domain.PermissionModeAsk)},
			{Tool: domain.ToolKindWebSearch, Pattern: "*", Action: firstPermission(rules.WebSearch, domain.PermissionModeAsk)},
		},
	}
}

func firstPermission(got, fallback domain.PermissionMode) domain.PermissionMode {
	if got != "" {
		return got
	}
	return fallback
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
