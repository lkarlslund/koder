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
	toml "github.com/pelletier/go-toml/v2"
)

type UI struct {
	Theme           string `toml:"theme"`
	CodeStyle       string `toml:"code_style"`
	EditForgiveness int    `toml:"edit_forgiveness"`
	Spinner         string `toml:"spinner"`
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
	DefaultProvider           string                   `toml:"default_provider"`
	DefaultModel              string                   `toml:"default_model"`
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
					Rules: []PermissionRule{
						{Tool: domain.ToolKindRead, Pattern: "*", Action: domain.PermissionModeAllow},
						{Tool: domain.ToolKindViewImage, Pattern: "*", Action: domain.PermissionModeAllow},
						{Tool: domain.ToolKindGlob, Pattern: "*", Action: domain.PermissionModeAllow},
						{Tool: domain.ToolKindGrep, Pattern: "*", Action: domain.PermissionModeAllow},
						{Tool: domain.ToolKindBash, Pattern: "*", Action: domain.PermissionModeAsk},
						{Tool: domain.ToolKindExecCommand, Pattern: "*", Action: domain.PermissionModeAsk},
						{Tool: domain.ToolKindExecStatus, Pattern: "*", Action: domain.PermissionModeAllow},
						{Tool: domain.ToolKindExecList, Pattern: "*", Action: domain.PermissionModeAllow},
						{Tool: domain.ToolKindExecWriteStdin, Pattern: "*", Action: domain.PermissionModeAllow},
						{Tool: domain.ToolKindExecResize, Pattern: "*", Action: domain.PermissionModeAllow},
						{Tool: domain.ToolKindExecTerminate, Pattern: "*", Action: domain.PermissionModeAllow},
						{Tool: domain.ToolKindExecCleanup, Pattern: "*", Action: domain.PermissionModeAllow},
						{Tool: domain.ToolKindApplyPatch, Pattern: "*", Action: domain.PermissionModeAsk},
						{Tool: domain.ToolKindEdit, Pattern: "*", Action: domain.PermissionModeAsk},
						{Tool: domain.ToolKindWrite, Pattern: "*", Action: domain.PermissionModeAsk},
						{Tool: domain.ToolKindTask, Pattern: "*", Action: domain.PermissionModeAsk},
						{Tool: domain.ToolKindQuestion, Pattern: "*", Action: domain.PermissionModeAsk},
						{Tool: domain.ToolKindUpdatePlan, Pattern: "*", Action: domain.PermissionModeAllow},
						{Tool: domain.ToolKindMilestoneList, Pattern: "*", Action: domain.PermissionModeAllow},
						{Tool: domain.ToolKindMilestoneAdd, Pattern: "*", Action: domain.PermissionModeAllow},
						{Tool: domain.ToolKindMilestoneUpdate, Pattern: "*", Action: domain.PermissionModeAllow},
						{Tool: domain.ToolKindMilestoneWrite, Pattern: "*", Action: domain.PermissionModeAllow},
						{Tool: domain.ToolKindTodoList, Pattern: "*", Action: domain.PermissionModeAllow},
						{Tool: domain.ToolKindTodoAddItems, Pattern: "*", Action: domain.PermissionModeAllow},
						{Tool: domain.ToolKindTodoUpdateItem, Pattern: "*", Action: domain.PermissionModeAllow},
						{Tool: domain.ToolKindTodoFetchNext, Pattern: "*", Action: domain.PermissionModeAllow},
						{Tool: domain.ToolKindSkill, Pattern: "*", Action: domain.PermissionModeAsk},
						{Tool: domain.ToolKindWebFetch, Pattern: "*", Action: domain.PermissionModeAsk},
						{Tool: domain.ToolKindWebSearch, Pattern: "*", Action: domain.PermissionModeAsk},
						{Tool: domain.ToolKindMCP, Pattern: "*", Action: domain.PermissionModeAsk},
					},
				},
				"readonly": {
					Rules: []PermissionRule{
						{Tool: domain.ToolKindRead, Pattern: "*", Action: domain.PermissionModeAllow},
						{Tool: domain.ToolKindViewImage, Pattern: "*", Action: domain.PermissionModeAllow},
						{Tool: domain.ToolKindGlob, Pattern: "*", Action: domain.PermissionModeAllow},
						{Tool: domain.ToolKindGrep, Pattern: "*", Action: domain.PermissionModeAllow},
						{Tool: domain.ToolKindBash, Pattern: "*", Action: domain.PermissionModeDeny},
						{Tool: domain.ToolKindExecCommand, Pattern: "*", Action: domain.PermissionModeDeny},
						{Tool: domain.ToolKindExecStatus, Pattern: "*", Action: domain.PermissionModeAllow},
						{Tool: domain.ToolKindExecList, Pattern: "*", Action: domain.PermissionModeAllow},
						{Tool: domain.ToolKindExecWriteStdin, Pattern: "*", Action: domain.PermissionModeDeny},
						{Tool: domain.ToolKindExecResize, Pattern: "*", Action: domain.PermissionModeDeny},
						{Tool: domain.ToolKindExecTerminate, Pattern: "*", Action: domain.PermissionModeDeny},
						{Tool: domain.ToolKindExecCleanup, Pattern: "*", Action: domain.PermissionModeDeny},
						{Tool: domain.ToolKindApplyPatch, Pattern: "*", Action: domain.PermissionModeDeny},
						{Tool: domain.ToolKindEdit, Pattern: "*", Action: domain.PermissionModeDeny},
						{Tool: domain.ToolKindWrite, Pattern: "*", Action: domain.PermissionModeDeny},
						{Tool: domain.ToolKindTask, Pattern: "*", Action: domain.PermissionModeAsk},
						{Tool: domain.ToolKindQuestion, Pattern: "*", Action: domain.PermissionModeAsk},
						{Tool: domain.ToolKindUpdatePlan, Pattern: "*", Action: domain.PermissionModeAllow},
						{Tool: domain.ToolKindMilestoneList, Pattern: "*", Action: domain.PermissionModeAllow},
						{Tool: domain.ToolKindMilestoneAdd, Pattern: "*", Action: domain.PermissionModeAllow},
						{Tool: domain.ToolKindMilestoneUpdate, Pattern: "*", Action: domain.PermissionModeAllow},
						{Tool: domain.ToolKindMilestoneWrite, Pattern: "*", Action: domain.PermissionModeAllow},
						{Tool: domain.ToolKindTodoList, Pattern: "*", Action: domain.PermissionModeAllow},
						{Tool: domain.ToolKindTodoAddItems, Pattern: "*", Action: domain.PermissionModeAllow},
						{Tool: domain.ToolKindTodoUpdateItem, Pattern: "*", Action: domain.PermissionModeAllow},
						{Tool: domain.ToolKindTodoFetchNext, Pattern: "*", Action: domain.PermissionModeAllow},
						{Tool: domain.ToolKindSkill, Pattern: "*", Action: domain.PermissionModeAsk},
						{Tool: domain.ToolKindWebFetch, Pattern: "*", Action: domain.PermissionModeAsk},
						{Tool: domain.ToolKindWebSearch, Pattern: "*", Action: domain.PermissionModeAsk},
						{Tool: domain.ToolKindMCP, Pattern: "*", Action: domain.PermissionModeAsk},
					},
				},
				"auto": {
					Rules: []PermissionRule{
						{Tool: domain.ToolKindRead, Pattern: "*", Action: domain.PermissionModeAllow},
						{Tool: domain.ToolKindViewImage, Pattern: "*", Action: domain.PermissionModeAllow},
						{Tool: domain.ToolKindGlob, Pattern: "*", Action: domain.PermissionModeAllow},
						{Tool: domain.ToolKindGrep, Pattern: "*", Action: domain.PermissionModeAllow},
						{Tool: domain.ToolKindBash, Pattern: "*", Action: domain.PermissionModeAllow},
						{Tool: domain.ToolKindExecCommand, Pattern: "*", Action: domain.PermissionModeAllow},
						{Tool: domain.ToolKindExecStatus, Pattern: "*", Action: domain.PermissionModeAllow},
						{Tool: domain.ToolKindExecList, Pattern: "*", Action: domain.PermissionModeAllow},
						{Tool: domain.ToolKindExecWriteStdin, Pattern: "*", Action: domain.PermissionModeAllow},
						{Tool: domain.ToolKindExecResize, Pattern: "*", Action: domain.PermissionModeAllow},
						{Tool: domain.ToolKindExecTerminate, Pattern: "*", Action: domain.PermissionModeAllow},
						{Tool: domain.ToolKindExecCleanup, Pattern: "*", Action: domain.PermissionModeAllow},
						{Tool: domain.ToolKindApplyPatch, Pattern: "*", Action: domain.PermissionModeAllow},
						{Tool: domain.ToolKindEdit, Pattern: "*", Action: domain.PermissionModeAllow},
						{Tool: domain.ToolKindWrite, Pattern: "*", Action: domain.PermissionModeAllow},
						{Tool: domain.ToolKindTask, Pattern: "*", Action: domain.PermissionModeAllow},
						{Tool: domain.ToolKindQuestion, Pattern: "*", Action: domain.PermissionModeAllow},
						{Tool: domain.ToolKindUpdatePlan, Pattern: "*", Action: domain.PermissionModeAllow},
						{Tool: domain.ToolKindMilestoneList, Pattern: "*", Action: domain.PermissionModeAllow},
						{Tool: domain.ToolKindMilestoneAdd, Pattern: "*", Action: domain.PermissionModeAllow},
						{Tool: domain.ToolKindMilestoneUpdate, Pattern: "*", Action: domain.PermissionModeAllow},
						{Tool: domain.ToolKindMilestoneWrite, Pattern: "*", Action: domain.PermissionModeAllow},
						{Tool: domain.ToolKindTodoList, Pattern: "*", Action: domain.PermissionModeAllow},
						{Tool: domain.ToolKindTodoAddItems, Pattern: "*", Action: domain.PermissionModeAllow},
						{Tool: domain.ToolKindTodoUpdateItem, Pattern: "*", Action: domain.PermissionModeAllow},
						{Tool: domain.ToolKindTodoFetchNext, Pattern: "*", Action: domain.PermissionModeAllow},
						{Tool: domain.ToolKindSkill, Pattern: "*", Action: domain.PermissionModeAllow},
						{Tool: domain.ToolKindWebFetch, Pattern: "*", Action: domain.PermissionModeAllow},
						{Tool: domain.ToolKindWebSearch, Pattern: "*", Action: domain.PermissionModeAsk},
						{Tool: domain.ToolKindMCP, Pattern: "*", Action: domain.PermissionModeAsk},
					},
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
			Spinner:         "dots",
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
	if hasLegacyPermissions(c.Permissions) {
		c.Permissions.Profiles["default"] = legacyPermissionProfile(c.Permissions)
	}
	mergeBuiltinPermissionProfileDefaults(c.Permissions.Profiles, def.Permissions.Profiles)
	if _, ok := c.Permissions.Profiles[c.Permissions.Profile]; !ok {
		c.Permissions.Profile = def.Permissions.Profile
	}
	if c.UI.Theme == "" {
		c.UI = def.UI
	}
	if c.UI.Spinner == "" {
		c.UI.Spinner = def.UI.Spinner
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
		dst[name] = PermissionProfile{Rules: rules}
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
			{Tool: domain.ToolKindViewImage, Pattern: "*", Action: firstPermission(rules.Read, domain.PermissionModeAllow)},
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

func mergeBuiltinPermissionProfileDefaults(dst map[string]PermissionProfile, defaults map[string]PermissionProfile) {
	for name, defProfile := range defaults {
		existing, ok := dst[name]
		if !ok {
			dst[name] = PermissionProfile{Rules: slices.Clone(defProfile.Rules)}
			continue
		}
		existing.Rules = mergeMissingPermissionRules(existing.Rules, defProfile.Rules)
		dst[name] = existing
	}
}

func mergeMissingPermissionRules(existing, defaults []PermissionRule) []PermissionRule {
	if len(defaults) == 0 {
		return slices.Clone(existing)
	}
	out := slices.Clone(existing)
	for _, candidate := range defaults {
		if hasPermissionRule(out, candidate) {
			continue
		}
		out = append(out, candidate)
	}
	return out
}

func hasPermissionRule(rules []PermissionRule, candidate PermissionRule) bool {
	for _, rule := range rules {
		if rule.Tool == candidate.Tool && strings.TrimSpace(rule.Pattern) == strings.TrimSpace(candidate.Pattern) {
			return true
		}
	}
	return false
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
