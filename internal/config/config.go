package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/lkarlslund/koder/internal/accesssettings"
	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/permissionprofile"
	toml "github.com/pelletier/go-toml/v2"
)

type UI struct {
	Theme        string `toml:"theme"`
	AutoContinue bool   `toml:"auto_continue"`
	TTS          TTS    `toml:"tts"`
}

type TTS struct {
	Enabled        bool    `toml:"enabled"`
	ProviderID     string  `toml:"provider_id"`
	ModelID        string  `toml:"model_id"`
	Voice          string  `toml:"voice"`
	ResponseFormat string  `toml:"response_format"`
	Speed          float64 `toml:"speed,omitempty"`
	PCMSampleRate  int     `toml:"pcm_sample_rate,omitempty"`
}

type Store struct {
	Backend string `toml:"backend"`
}

type Thinking struct {
	CavemanEnabled     bool   `toml:"caveman_enabled"`
	CavemanProviderID  string `toml:"caveman_provider_id"`
	CavemanModelID     string `toml:"caveman_model_id"`
	CavemanPrompt      string `toml:"caveman_prompt"`
	CavemanParallelism int    `toml:"caveman_parallelism,omitempty"`
	CavemanMinTokens   int    `toml:"caveman_min_tokens,omitempty"`
}

type Defaults struct {
	ProviderID string `toml:"provider_id"`
	ModelID    string `toml:"model_id"`
}

type Compaction struct {
	ProviderID    string `toml:"provider_id"`
	ModelID       string `toml:"model_id"`
	AutoAtPercent int    `toml:"auto_at_percent"`
	KeepToolCalls int    `toml:"keep_tool_calls"`
}

type Tools struct {
	Enabled ToolDefaults `toml:"enabled"`
}

type Provider struct {
	TemplateID              string            `toml:"template_id"`
	Kind                    string            `toml:"kind"`
	AuthMethod              string            `toml:"auth_method"`
	Name                    string            `toml:"name"`
	BaseURL                 string            `toml:"base_url"`
	APIKey                  string            `toml:"api_key"`
	APIKeyEnv               string            `toml:"api_key_env"`
	Headers                 map[string]string `toml:"headers"`
	Stream                  bool              `toml:"stream"`
	Timeout                 time.Duration     `toml:"timeout"`
	Disabled                bool              `toml:"disabled"`
	PromptProgressMode      string            `toml:"prompt_progress_mode"`
	PromptProgressProbed    bool              `toml:"prompt_progress_probed"`
	PromptProgressSupported bool              `toml:"prompt_progress_supported"`
	LlamaSlots              int               `toml:"llama_slots,omitempty"`
	LlamaSlotScope          string            `toml:"llama_slot_scope,omitempty"`
}

// ModelConfig stores settings for one provider/model pair.
type ModelConfig struct {
	ProviderID       string   `toml:"provider_id"`
	ModelID          string   `toml:"model_id"`
	SourceProviderID string   `toml:"source_provider_id,omitempty"`
	SourceModelID    string   `toml:"source_model_id,omitempty"`
	ContextWindow    int      `toml:"context_window"`
	ModelPreset      string   `toml:"model_preset"`
	Temperature      *float64 `toml:"temperature,omitempty"`
	TopP             *float64 `toml:"top_p,omitempty"`
	MinP             *float64 `toml:"min_p,omitempty"`
	TopK             int      `toml:"top_k,omitempty"`
	RepeatPenalty    *float64 `toml:"repeat_penalty,omitempty"`
	ThinkingMode     string   `toml:"thinking_mode,omitempty"`
	ThinkingBudget   int      `toml:"thinking_budget,omitempty"`
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

type ToolDefaults map[domain.ToolKind]bool

type Config struct {
	Defaults         Defaults                `toml:"defaults"`
	Compaction       Compaction              `toml:"compaction"`
	MaxToolLoopSteps int                     `toml:"max_tool_loop_steps"`
	Tools            Tools                   `toml:"tools"`
	Providers        map[string]Provider     `toml:"providers"`
	Models           []ModelConfig           `toml:"models"`
	MCPServers       map[string]MCPServer    `toml:"mcp_servers"`
	Permissions      PermissionRules         `toml:"permissions"`
	Access           accesssettings.Settings `toml:"access"`
	Store            Store                   `toml:"store"`
	UI               UI                      `toml:"ui"`
	Thinking         Thinking                `toml:"thinking"`
	path             string
	configDir        string
	stateDir         string
	cacheDir         string
}

func (d *ToolDefaults) UnmarshalTOML(data []byte) error {
	var raw map[string]bool
	if err := toml.Unmarshal(data, &raw); err != nil {
		return err
	}
	parsed := make(ToolDefaults, len(raw))
	for name, enabled := range raw {
		kind, err := parseToolDefaultKind(name)
		if err != nil {
			continue
		}
		parsed[kind] = enabled
	}
	*d = parsed
	return nil
}

const providerConfigurationHint = "configure at least one provider in config.toml and set defaults.provider_id"
const defaultMaxToolLoopSteps = 500
const defaultAutoCompactAt = 80
const defaultCompactionKeepToolCalls = 2
const defaultCavemanParallelism = 1
const DefaultCavemanMinTokens = 64
const maxCompactionKeepToolCalls = 10
const oldDefaultCavemanThinkingPrompt = "Rewrite the following model thinking as concise caveman talk. Remove unnecessary filler words. Keep only useful intent, constraints, and decisions. Return only the rewritten thinking.\n\nThinking:\n{{thinking}}"
const previousDefaultCavemanThinkingPrompt = `Rewrite MODEL_THINKING into concise caveman notes for later context.

Rules:
- Output only the rewritten notes.
- Do not explain the task.
- Do not analyze the request.
- Do not include "Thinking Process", numbered steps, markdown headings, or filler.
- Keep useful intent, constraints, decisions, and next action only.
- Use short blunt phrases. Max 6 lines.

MODEL_THINKING:
{{thinking}}`
const DefaultCavemanThinkingPrompt = `Compress hidden model thinking into terse notes for future context replay.

Rules:
- Output only notes; no headings, markdown, labels, preamble, or explanation.
- Use short blunt caveman-style phrases.
- Keep only durable facts, constraints, decisions, plan, blockers, and next action.
- Drop self-talk, uncertainty filler, restated instructions, apologies, and scaffolding.
- If nothing durable remains, output nothing.
- Be much shorter than input; prefer 1-6 short lines.`

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

	if err := toml.NewDecoder(bytes.NewReader(data)).EnableUnmarshalerInterface().Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("parse config: %w", err)
	}
	if !strings.Contains(string(data), "auto_continue") {
		cfg.UI.AutoContinue = true
	}
	cfg.configDir = configDir
	cfg.stateDir = stateDir()
	cfg.cacheDir = cacheDir()
	cfg.path = filepath.Join(configDir, "config.toml")
	cfg.applyDefaults()
	return cfg, nil
}

func Default() Config {
	builtinToolKinds := domain.BuiltinToolKinds()
	toolDefaults := make(ToolDefaults, len(builtinToolKinds))
	for _, kind := range builtinToolKinds {
		toolDefaults[kind] = true
	}
	toolDefaults[domain.ToolKindBash] = false
	return Config{
		MaxToolLoopSteps: defaultMaxToolLoopSteps,
		Compaction: Compaction{
			AutoAtPercent: defaultAutoCompactAt,
			KeepToolCalls: defaultCompactionKeepToolCalls,
		},
		Tools:      Tools{Enabled: toolDefaults},
		Providers:  map[string]Provider{},
		Models:     []ModelConfig{},
		MCPServers: map[string]MCPServer{},
		Access:     accesssettings.Default(),
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
			Theme:        "dark",
			AutoContinue: true,
			TTS: TTS{
				Voice:          "alloy",
				ResponseFormat: "wav",
				Speed:          1,
				PCMSampleRate:  24000,
			},
		},
		Thinking: Thinking{
			CavemanPrompt:      DefaultCavemanThinkingPrompt,
			CavemanParallelism: defaultCavemanParallelism,
			CavemanMinTokens:   DefaultCavemanMinTokens,
		},
	}
}

func (c *Config) applyDefaults() {
	def := Default()
	if c.MaxToolLoopSteps <= 0 {
		c.MaxToolLoopSteps = def.MaxToolLoopSteps
	}
	if c.Compaction.AutoAtPercent <= 0 {
		c.Compaction.AutoAtPercent = def.Compaction.AutoAtPercent
	}
	c.Compaction.KeepToolCalls = NormalizeCompactionKeepToolCalls(c.Compaction.KeepToolCalls)
	if c.Providers == nil {
		c.Providers = def.Providers
	}
	if c.MCPServers == nil {
		c.MCPServers = def.MCPServers
	}
	if c.Tools.Enabled == nil {
		c.Tools.Enabled = cloneToolDefaults(def.Tools.Enabled)
	}
	pruneToolDefaults(c.Tools.Enabled)
	for _, kind := range domain.BuiltinToolKinds() {
		if _, ok := c.Tools.Enabled[kind]; !ok {
			c.Tools.Enabled[kind] = def.Tools.Enabled[kind]
		}
	}
	if c.Permissions.Profile == "" {
		c.Permissions.Profile = def.Permissions.Profile
	}
	if accesssettings.IsZero(c.Access) {
		c.Access = def.Access
	}
	c.Access = accesssettings.Normalize(c.Access)
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
		c.UI.Theme = def.UI.Theme
		c.UI.AutoContinue = def.UI.AutoContinue
	}
	c.UI.TTS.ProviderID = strings.TrimSpace(c.UI.TTS.ProviderID)
	c.UI.TTS.ModelID = strings.TrimSpace(c.UI.TTS.ModelID)
	c.UI.TTS.Voice = strings.TrimSpace(c.UI.TTS.Voice)
	if c.UI.TTS.Voice == "" {
		c.UI.TTS.Voice = def.UI.TTS.Voice
	}
	c.UI.TTS.ResponseFormat = strings.ToLower(strings.TrimSpace(c.UI.TTS.ResponseFormat))
	if c.UI.TTS.ResponseFormat == "" {
		c.UI.TTS.ResponseFormat = def.UI.TTS.ResponseFormat
	}
	if c.UI.TTS.Speed <= 0 {
		c.UI.TTS.Speed = def.UI.TTS.Speed
	}
	if c.UI.TTS.PCMSampleRate <= 0 {
		c.UI.TTS.PCMSampleRate = def.UI.TTS.PCMSampleRate
	}
	c.Thinking.CavemanProviderID = strings.TrimSpace(c.Thinking.CavemanProviderID)
	c.Thinking.CavemanModelID = strings.TrimSpace(c.Thinking.CavemanModelID)
	if c.Thinking.CavemanParallelism <= 0 {
		c.Thinking.CavemanParallelism = defaultCavemanParallelism
	}
	if c.Thinking.CavemanMinTokens <= 0 {
		c.Thinking.CavemanMinTokens = DefaultCavemanMinTokens
	}
	switch strings.TrimSpace(c.Thinking.CavemanPrompt) {
	case "":
		c.Thinking.CavemanPrompt = def.Thinking.CavemanPrompt
	case oldDefaultCavemanThinkingPrompt, previousDefaultCavemanThinkingPrompt:
		c.Thinking.CavemanPrompt = DefaultCavemanThinkingPrompt
	}
	fallbackProvider := providerDefaults()
	for id, provider := range c.Providers {
		if provider.Kind == "" {
			provider.Kind = "openai-compatible"
		}
		if provider.Timeout == 0 {
			provider.Timeout = fallbackProvider.Timeout
		}
		provider.PromptProgressMode = NormalizePromptProgressMode(provider.PromptProgressMode)
		if provider.LlamaSlots < 0 {
			provider.LlamaSlots = 0
		}
		if provider.LlamaSlots > 0 {
			provider.LlamaSlotScope = NormalizeLlamaSlotScope(provider.LlamaSlotScope)
		} else {
			provider.LlamaSlotScope = ""
		}
		if provider.Headers == nil {
			provider.Headers = map[string]string{}
		}
		c.Providers[id] = provider
	}
	c.Models = normalizeModelConfigs(c.Models)
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
	if strings.TrimSpace(c.Defaults.ProviderID) == "" {
		return fmt.Errorf("defaults.provider_id is not set; %s", providerConfigurationHint)
	}
	if _, ok := c.Provider(c.Defaults.ProviderID); !ok {
		return fmt.Errorf("default provider %q not configured; %s", c.Defaults.ProviderID, providerConfigurationHint)
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
	return c.HasUsableProvider(c.Defaults.ProviderID)
}

// ModelConfig returns the configured settings for a provider/model pair.
func (c Config) ModelConfig(providerID, modelID string) (ModelConfig, bool) {
	providerID = strings.TrimSpace(providerID)
	modelID = strings.TrimSpace(modelID)
	if providerID == "" || modelID == "" {
		return ModelConfig{}, false
	}
	for _, model := range c.Models {
		if strings.TrimSpace(model.ProviderID) == providerID && strings.TrimSpace(model.ModelID) == modelID {
			return normalizeModelConfig(model), true
		}
	}
	return ModelConfig{}, false
}

// ResolveModel returns the provider/model id that should be sent to the provider.
func (c Config) ResolveModel(providerID, modelID string) (string, string) {
	providerID = strings.TrimSpace(providerID)
	modelID = strings.TrimSpace(modelID)
	if model, ok := c.ModelConfig(providerID, modelID); ok {
		sourceProviderID := strings.TrimSpace(model.SourceProviderID)
		sourceModelID := strings.TrimSpace(model.SourceModelID)
		if sourceProviderID == "" {
			sourceProviderID = providerID
		}
		if sourceModelID != "" {
			return sourceProviderID, sourceModelID
		}
	}
	return providerID, modelID
}

// SetModelConfig inserts or replaces settings for a provider/model pair.
func (c *Config) SetModelConfig(model ModelConfig) {
	model = normalizeModelConfig(model)
	if model.ProviderID == "" || model.ModelID == "" {
		return
	}
	for idx := range c.Models {
		if strings.TrimSpace(c.Models[idx].ProviderID) == model.ProviderID && strings.TrimSpace(c.Models[idx].ModelID) == model.ModelID {
			c.Models[idx] = model
			c.Models = normalizeModelConfigs(c.Models)
			return
		}
	}
	c.Models = append(c.Models, model)
	c.Models = normalizeModelConfigs(c.Models)
}

// ContextWindow returns the configured context window for a provider/model pair.
func (c Config) ContextWindow(providerID, modelID string) int {
	if model, ok := c.ModelConfig(providerID, modelID); ok && model.ContextWindow > 0 {
		return model.ContextWindow
	}
	return 32768
}

// ModelPreset returns the configured request preset for a provider/model pair.
func (c Config) ModelPreset(providerID, modelID string) string {
	if model, ok := c.ModelConfig(providerID, modelID); ok {
		return strings.TrimSpace(model.ModelPreset)
	}
	return "auto"
}

// ModelRequestOptions returns request-level settings for a provider/model pair.
func (c Config) ModelRequestOptions(providerID, modelID string) ModelConfig {
	model, _ := c.ModelConfig(providerID, modelID)
	sourceProviderID, sourceModelID := c.ResolveModel(providerID, modelID)
	if sourceProviderID != "" {
		model.ProviderID = sourceProviderID
	}
	if sourceModelID != "" {
		model.ModelID = sourceModelID
	}
	return model
}

func normalizeModelConfigs(src []ModelConfig) []ModelConfig {
	seen := map[string]int{}
	out := make([]ModelConfig, 0, len(src))
	for _, model := range src {
		model = normalizeModelConfig(model)
		if model.ProviderID == "" || model.ModelID == "" {
			continue
		}
		key := model.ProviderID + "\x00" + model.ModelID
		if idx, ok := seen[key]; ok {
			out[idx] = model
			continue
		}
		seen[key] = len(out)
		out = append(out, model)
	}
	slices.SortFunc(out, func(a, b ModelConfig) int {
		if cmp := strings.Compare(a.ProviderID, b.ProviderID); cmp != 0 {
			return cmp
		}
		return strings.Compare(a.ModelID, b.ModelID)
	})
	return out
}

func normalizeModelConfig(model ModelConfig) ModelConfig {
	model.ProviderID = strings.TrimSpace(model.ProviderID)
	model.ModelID = strings.TrimSpace(model.ModelID)
	model.SourceProviderID = strings.TrimSpace(model.SourceProviderID)
	model.SourceModelID = strings.TrimSpace(model.SourceModelID)
	if model.SourceProviderID == model.ProviderID {
		model.SourceProviderID = ""
	}
	if model.SourceModelID == model.ModelID && model.SourceProviderID == "" {
		model.SourceModelID = ""
	}
	model.ModelPreset = strings.TrimSpace(model.ModelPreset)
	if model.ModelPreset == "" {
		model.ModelPreset = "auto"
	}
	model.ThinkingMode = normalizeThinkingMode(model.ThinkingMode)
	if model.ThinkingBudget < 0 {
		model.ThinkingBudget = 0
	}
	return model
}

func normalizeThinkingMode(mode string) string {
	switch strings.TrimSpace(strings.ToLower(mode)) {
	case "enabled", "disabled":
		return strings.TrimSpace(strings.ToLower(mode))
	default:
		return "auto"
	}
}

func providerDefaults() Provider {
	return Provider{
		Headers:            map[string]string{},
		Stream:             true,
		Timeout:            10 * time.Minute,
		Disabled:           false,
		PromptProgressMode: "auto",
	}
}

func NormalizePromptProgressMode(value string) string {
	switch strings.TrimSpace(strings.ToLower(value)) {
	case "enabled", "disabled":
		return strings.TrimSpace(strings.ToLower(value))
	default:
		return "auto"
	}
}

func NormalizeLlamaSlotScope(value string) string {
	switch strings.TrimSpace(strings.ToLower(value)) {
	case "session":
		return "session"
	default:
		return "chat"
	}
}

func NormalizeCompactionKeepToolCalls(value int) int {
	if value < 0 {
		return 0
	}
	if value > maxCompactionKeepToolCalls {
		return maxCompactionKeepToolCalls
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

func cloneToolDefaults(src ToolDefaults) ToolDefaults {
	dst := make(ToolDefaults, len(src))
	for kind, enabled := range src {
		dst[kind] = enabled
	}
	return dst
}

func pruneToolDefaults(defaults ToolDefaults) {
	builtinToolKinds := domain.BuiltinToolKinds()
	known := make(map[domain.ToolKind]struct{}, len(builtinToolKinds))
	for _, kind := range builtinToolKinds {
		known[kind] = struct{}{}
	}
	for kind := range defaults {
		if _, ok := known[kind]; !ok {
			delete(defaults, kind)
		}
	}
}

var toolDefaultKindAliases = map[string]domain.ToolKind{
	"execcleanupbackground":     domain.ToolKindExecCleanup,
	"milestoneadditems":         domain.ToolKindMilestoneAdd,
	"milestoneplananddecompose": domain.ToolKindMilestonePlan,
	"milestoneupdateitem":       domain.ToolKindMilestoneUpdate,
}

func parseToolDefaultKind(name string) (domain.ToolKind, error) {
	trimmed := strings.TrimSpace(name)
	normalized := strings.NewReplacer("_", "", "-", "").Replace(strings.ToLower(strings.TrimSpace(name)))
	if kind, ok := toolDefaultKindAliases[normalized]; ok {
		return kind, nil
	}
	for _, kind := range domain.BuiltinToolKinds() {
		if kind.String() == trimmed {
			return kind, nil
		}
		canonical := strings.NewReplacer("_", "", "-", "").Replace(strings.ToLower(kind.String()))
		if canonical == normalized {
			return kind, nil
		}
	}
	return "", fmt.Errorf("unknown tool default %q", name)
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
