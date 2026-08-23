package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lkarlslund/koder/internal/accesssettings"
	"github.com/lkarlslund/koder/internal/domain"
	toml "github.com/pelletier/go-toml/v2"
)

func TestPromptProgressObservationInvalidatesWhenEndpointChanges(t *testing.T) {
	checkedAt := time.Now().Add(-time.Minute).UTC()
	provider := WithPromptProgressObservation(Provider{Kind: "openai-compatible", BaseURL: "http://one.example/v1", PromptProgressMode: "auto"}, true, checkedAt)
	if !PromptProgressObservationValid(provider) || !provider.PromptProgressSupported || !provider.PromptProgressCheckedAt.Equal(checkedAt) {
		t.Fatalf("expected valid prompt progress observation, got %#v", provider)
	}

	provider.BaseURL = "http://two.example/v1"
	if PromptProgressObservationValid(provider) {
		t.Fatal("observation remained valid after endpoint changed")
	}
	cfg := Default()
	cfg.Providers = map[string]Provider{"test": provider}
	cfg.applyDefaults()
	invalidated := cfg.Providers["test"]
	if invalidated.PromptProgressProbed || invalidated.PromptProgressSupported || !invalidated.PromptProgressCheckedAt.IsZero() || invalidated.PromptProgressTarget != "" {
		t.Fatalf("stale observation was not cleared: %#v", invalidated)
	}
}

func TestLoadWritesDefaultConfig(t *testing.T) {
	temp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", temp)
	t.Setenv("XDG_STATE_HOME", temp)
	t.Setenv("XDG_CACHE_HOME", temp)

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Defaults.ProviderID != "" {
		t.Fatalf("expected no default provider, got %q", cfg.Defaults.ProviderID)
	}
	if cfg.MaxToolLoopSteps != 500 {
		t.Fatalf("expected default max tool loop steps 500, got %d", cfg.MaxToolLoopSteps)
	}
	if cfg.MaxChildChats != 1 {
		t.Fatalf("expected default max child chats 1, got %d", cfg.MaxChildChats)
	}
	if cfg.Compaction.AutoAtPercent != defaultAutoCompactAt {
		t.Fatalf("expected default auto compact threshold %d, got %d", defaultAutoCompactAt, cfg.Compaction.AutoAtPercent)
	}
	if cfg.Compaction.ProviderID != "" || cfg.Compaction.ModelID != "" {
		t.Fatalf("expected chat model compaction default, got %q/%q", cfg.Compaction.ProviderID, cfg.Compaction.ModelID)
	}
	if cfg.Permissions.Profile != "default" {
		t.Fatalf("unexpected permission profile: %s", cfg.Permissions.Profile)
	}
	if cfg.Store.Backend != "pebble" {
		t.Fatalf("unexpected store backend: %s", cfg.Store.Backend)
	}
	if !cfg.Knowledge.Enabled || cfg.Knowledge.Required {
		t.Fatalf("knowledge should default to enabled but optional: %#v", cfg.Knowledge)
	}
	if !cfg.UI.AutoContinue {
		t.Fatal("expected auto continue enabled by default")
	}
	if cfg.Thinking.CavemanPrompt != DefaultCavemanThinkingPrompt {
		t.Fatalf("expected default caveman prompt, got %q", cfg.Thinking.CavemanPrompt)
	}
	if cfg.Thinking.CavemanParallelism != defaultCavemanParallelism {
		t.Fatalf("expected default caveman parallelism %d, got %d", defaultCavemanParallelism, cfg.Thinking.CavemanParallelism)
	}
	if cfg.Thinking.CavemanMinTokens != DefaultCavemanMinTokens {
		t.Fatalf("expected default caveman min tokens %d, got %d", DefaultCavemanMinTokens, cfg.Thinking.CavemanMinTokens)
	}
	if len(cfg.Permissions.Profiles) == 0 {
		t.Fatal("expected permission profiles")
	}
	if cfg.Tools.Enabled[domain.ToolKindBash] {
		t.Fatal("expected bash disabled by default")
	}
	if !cfg.Tools.Enabled[domain.ToolKindExecCommand] {
		t.Fatal("expected exec_command enabled by default")
	}
	if len(cfg.Providers) != 0 {
		t.Fatalf("expected no providers in default config, got %#v", cfg.Providers)
	}
	if _, err := os.Stat(filepath.Join(temp, "koder", "config.toml")); err != nil {
		t.Fatalf("expected config file: %v", err)
	}
}

func TestKnowledgeConfigurationRoundTrip(t *testing.T) {
	temp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", temp)
	t.Setenv("XDG_STATE_HOME", temp)
	t.Setenv("XDG_CACHE_HOME", temp)

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	cfg.Knowledge.Required = true
	cfg.Knowledge.TrustedPublishers = []KnowledgeTrustedPublisher{{
		ID: "publisher:example", Name: "Example", Keys: map[string]string{"example:key": "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="},
	}}
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.Knowledge.Enabled || !loaded.Knowledge.Required || len(loaded.Knowledge.TrustedPublishers) != 1 || loaded.Knowledge.TrustedPublishers[0].Keys["example:key"] == "" {
		t.Fatalf("knowledge configuration did not round-trip: %#v", loaded.Knowledge)
	}
}

func TestLegacyConfigurationEnablesOptionalKnowledge(t *testing.T) {
	temp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", temp)
	t.Setenv("XDG_STATE_HOME", temp)
	t.Setenv("XDG_CACHE_HOME", temp)
	configDir := filepath.Join(temp, "koder")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "config.toml"), []byte("[store]\nbackend = 'pebble'\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Knowledge.Enabled || cfg.Knowledge.Required {
		t.Fatalf("legacy config knowledge policy = %#v", cfg.Knowledge)
	}
}

func TestRequiredKnowledgeCannotBeDisabled(t *testing.T) {
	cfg := Default()
	cfg.Knowledge.Enabled = false
	cfg.Knowledge.Required = true
	cfg.applyDefaults()
	if !cfg.Knowledge.Enabled {
		t.Fatalf("required knowledge remained disabled: %#v", cfg.Knowledge)
	}
}

func TestLoadWithDataDirUsesSingleRoot(t *testing.T) {
	dataDir := t.TempDir()

	cfg, err := LoadWithOptions(LoadOptions{DataDir: dataDir})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Path() != filepath.Join(dataDir, "config.toml") {
		t.Fatalf("unexpected config path: %s", cfg.Path())
	}
	if cfg.StateDir() != filepath.Join(dataDir, "state") {
		t.Fatalf("unexpected state dir: %s", cfg.StateDir())
	}
	if cfg.CacheDir() != filepath.Join(dataDir, "cache") {
		t.Fatalf("unexpected cache dir: %s", cfg.CacheDir())
	}
	if cfg.ManagedAssetsDir() != filepath.Join(dataDir, "assets") {
		t.Fatalf("unexpected managed assets dir: %s", cfg.ManagedAssetsDir())
	}
	if _, err := os.Stat(filepath.Join(dataDir, "config.toml")); err != nil {
		t.Fatalf("expected config file: %v", err)
	}
}

func TestDefaultBrowserConfiguration(t *testing.T) {
	cfg := Default()
	if !cfg.Browser.Enabled || !cfg.Browser.Headed {
		t.Fatalf("browser should default to enabled and headed: %#v", cfg.Browser)
	}
	if cfg.Browser.OperationTimeout != 30*time.Second || cfg.Browser.MaxTabsPerChat != 8 || cfg.Browser.MaxTabsGlobal != 32 {
		t.Fatalf("unexpected browser defaults: %#v", cfg.Browser)
	}
}

func TestLoadUsesKoderDataDirEnv(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("KODER_DATA_DIR", dataDir)

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Path() != filepath.Join(dataDir, "config.toml") {
		t.Fatalf("unexpected config path: %s", cfg.Path())
	}
	if cfg.ManagedAssetsDir() != filepath.Join(dataDir, "assets") {
		t.Fatalf("unexpected managed assets dir: %s", cfg.ManagedAssetsDir())
	}
}

func TestThinkingPreferencesRoundTrip(t *testing.T) {
	temp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", temp)
	t.Setenv("XDG_STATE_HOME", temp)
	t.Setenv("XDG_CACHE_HOME", temp)

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	cfg.Thinking.CavemanEnabled = true
	cfg.Thinking.CavemanProviderID = "test"
	cfg.Thinking.CavemanModelID = "model"
	cfg.Thinking.CavemanPrompt = "rewrite:\n{{thinking}}"
	cfg.Thinking.CavemanParallelism = 3
	cfg.Thinking.CavemanMinTokens = 128
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.Thinking.CavemanEnabled || loaded.Thinking.CavemanProviderID != "test" || loaded.Thinking.CavemanModelID != "model" || loaded.Thinking.CavemanPrompt != "rewrite:\n{{thinking}}" || loaded.Thinking.CavemanParallelism != 3 || loaded.Thinking.CavemanMinTokens != 128 {
		t.Fatalf("expected thinking settings to round-trip, got %#v", loaded.Thinking)
	}
}

func TestGlobalMountsRoundTripAndExpandHome(t *testing.T) {
	temp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", temp)
	t.Setenv("XDG_STATE_HOME", temp)
	t.Setenv("XDG_CACHE_HOME", temp)
	home := t.TempDir()
	t.Setenv("HOME", home)

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	cfg.GlobalMounts = []accesssettings.Mount{
		{Path: "~/.cache", Mode: accesssettings.ModeReadWrite},
		{Path: filepath.Join(home, "go", "pkg", "mod"), Mode: accesssettings.ModeReadOnly},
	}
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.GlobalMounts) != 2 || loaded.GlobalMounts[0].Path != filepath.Join(home, ".cache") || loaded.GlobalMounts[0].Mode != accesssettings.ModeReadWrite {
		t.Fatalf("global mounts did not round-trip: %#v", loaded.GlobalMounts)
	}
}

func TestSkillsConfigurationRoundTripAndNormalize(t *testing.T) {
	temp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", temp)
	t.Setenv("XDG_STATE_HOME", temp)
	t.Setenv("XDG_CACHE_HOME", temp)
	home := t.TempDir()
	t.Setenv("HOME", home)

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	cfg.Skills = Skills{
		Disabled:        []string{"~/.agents/skills/review/SKILL.md", "~/.agents/skills/review/SKILL.md", ""},
		CatalogMaxChars: 8_000,
	}
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(home, ".agents", "skills", "review", "SKILL.md")
	if loaded.Skills.CatalogMaxChars != 8_000 || len(loaded.Skills.Disabled) != 1 || loaded.Skills.Disabled[0] != want {
		t.Fatalf("skills settings did not normalize and round-trip: %#v", loaded.Skills)
	}
}

func TestOldDefaultCavemanPromptUpgrades(t *testing.T) {
	for _, prompt := range []string{oldDefaultCavemanThinkingPrompt, previousDefaultCavemanThinkingPrompt} {
		t.Run(prompt[:min(12, len(prompt))], func(t *testing.T) {
			temp := t.TempDir()
			t.Setenv("XDG_CONFIG_HOME", temp)
			t.Setenv("XDG_STATE_HOME", temp)
			t.Setenv("XDG_CACHE_HOME", temp)

			cfg, err := Load()
			if err != nil {
				t.Fatal(err)
			}
			cfg.Thinking.CavemanPrompt = prompt
			if err := cfg.Save(); err != nil {
				t.Fatal(err)
			}
			loaded, err := Load()
			if err != nil {
				t.Fatal(err)
			}
			if loaded.Thinking.CavemanPrompt != DefaultCavemanThinkingPrompt {
				t.Fatalf("expected old default caveman prompt to upgrade, got %q", loaded.Thinking.CavemanPrompt)
			}
		})
	}
}

func TestCompactionModelPreferenceRoundTrips(t *testing.T) {
	temp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", temp)
	t.Setenv("XDG_STATE_HOME", temp)
	t.Setenv("XDG_CACHE_HOME", temp)

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	cfg.Compaction.ProviderID = "fast"
	cfg.Compaction.ModelID = "fast-model"
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Compaction.ProviderID != "fast" || loaded.Compaction.ModelID != "fast-model" {
		t.Fatalf("expected compaction override fast/fast-model, got %q/%q", loaded.Compaction.ProviderID, loaded.Compaction.ModelID)
	}
}

func TestLegacyLlamaSlotSettingsAreIgnoredAndDroppedOnSave(t *testing.T) {
	temp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", temp)
	t.Setenv("XDG_STATE_HOME", temp)
	t.Setenv("XDG_CACHE_HOME", temp)

	configDir := filepath.Join(temp, "koder")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(configDir, "config.toml")
	raw := []byte("[providers.local]\nbase_url = 'http://127.0.0.1:8000/v1'\nllama_slots = 2\nllama_slot_scope = 'chat'\n")
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatalf("load legacy config: %v", err)
	}
	if err := cfg.Save(); err != nil {
		t.Fatalf("save migrated config: %v", err)
	}
	saved, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(saved), "llama_slot") {
		t.Fatalf("legacy llama slot settings survived save:\n%s", saved)
	}
}

func TestLoadAcceptsTextToolDefaultKeys(t *testing.T) {
	temp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", temp)
	t.Setenv("XDG_STATE_HOME", temp)
	t.Setenv("XDG_CACHE_HOME", temp)

	configRoot := filepath.Join(temp, "koder")
	if err := os.MkdirAll(configRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	raw := []byte(`
[tools.enabled]
bash = false
exec_write_stdin = false
exec_cleanup_background = false
milestone_add_items = false
milestone_plan_and_decompose = false
milestone_update_item = false
`)
	if err := os.WriteFile(filepath.Join(configRoot, "config.toml"), raw, 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []domain.ToolKind{
		domain.ToolKindBash,
		domain.ToolKindExecWriteStdin,
		domain.ToolKindExecCleanup,
		domain.ToolKindMilestoneAdd,
		domain.ToolKindMilestonePlan,
		domain.ToolKindMilestoneUpdate,
	} {
		if cfg.Tools.Enabled[kind] {
			t.Fatalf("expected %s to stay disabled: %#v", kind, cfg.Tools.Enabled)
		}
	}
	if !cfg.Tools.Enabled[domain.ToolKindFileRead] {
		t.Fatal("expected missing tool default to be backfilled enabled")
	}
}

func TestLoadIgnoresUnknownToolDefaultKeys(t *testing.T) {
	temp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", temp)
	t.Setenv("XDG_STATE_HOME", temp)
	t.Setenv("XDG_CACHE_HOME", temp)

	configRoot := filepath.Join(temp, "koder")
	if err := os.MkdirAll(configRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	raw := []byte(`
[tools.enabled]
grep = false
write = false
file_read = false
`)
	if err := os.WriteFile(filepath.Join(configRoot, "config.toml"), raw, 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Tools.Enabled[domain.ToolKindFileRead] {
		t.Fatalf("expected current file_read setting to stay disabled: %#v", cfg.Tools.Enabled)
	}
	for _, kind := range domain.BuiltinToolKinds() {
		if kind.String() == "" {
			t.Fatalf("loaded invalid tool kind %q", kind)
		}
	}
}

func TestLoadBackfillsMissingToolDefaultsFromCurrentDefaults(t *testing.T) {
	temp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", temp)
	t.Setenv("XDG_STATE_HOME", temp)
	t.Setenv("XDG_CACHE_HOME", temp)

	configRoot := filepath.Join(temp, "koder")
	if err := os.MkdirAll(configRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	raw := []byte(`
[tools.enabled]
file_read = true
`)
	if err := os.WriteFile(filepath.Join(configRoot, "config.toml"), raw, 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Tools.Enabled[domain.ToolKindBash] {
		t.Fatalf("expected missing bash default to backfill disabled: %#v", cfg.Tools.Enabled)
	}
	if !cfg.Tools.Enabled[domain.ToolKindExecCommand] {
		t.Fatalf("expected missing exec_command default to backfill enabled: %#v", cfg.Tools.Enabled)
	}
}

func TestRequireProviderRejectsMissingProviderConfiguration(t *testing.T) {
	cfg := Default()

	err := cfg.RequireProvider()
	if err == nil {
		t.Fatal("expected missing provider configuration error")
	}
	if !strings.Contains(err.Error(), "configure at least one provider") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestApplyDefaultsInfersProviderKind(t *testing.T) {
	cfg := Default()
	cfg.Providers["remote"] = Provider{
		BaseURL:   "https://example.com/v1",
		APIKeyEnv: "EXAMPLE_KEY",
	}
	cfg.Providers["local"] = Provider{
		BaseURL: "http://127.0.0.1:11434/v1",
	}

	cfg.applyDefaults()

	if got := cfg.Providers["remote"].Kind; got != "openai-compatible" {
		t.Fatalf("expected inferred provider kind, got %q", got)
	}
}

func TestLoadBackfillsMissingAutoContinueSetting(t *testing.T) {
	temp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", temp)
	t.Setenv("XDG_STATE_HOME", temp)
	t.Setenv("XDG_CACHE_HOME", temp)

	configRoot := filepath.Join(temp, "koder")
	if err := os.MkdirAll(configRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	raw := []byte("[ui]\ntheme = \"tokyonight\"\n")
	if err := os.WriteFile(filepath.Join(configRoot, "config.toml"), raw, 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.UI.AutoContinue {
		t.Fatal("expected missing auto_continue setting to default to true")
	}
}

func TestApplyDefaultsFillsMissingMaxToolLoopSteps(t *testing.T) {
	cfg := Default()
	cfg.MaxToolLoopSteps = 0
	cfg.MaxChildChats = 0

	cfg.applyDefaults()

	if cfg.MaxToolLoopSteps != 500 {
		t.Fatalf("expected default max tool loop steps applied, got %d", cfg.MaxToolLoopSteps)
	}
	if cfg.MaxChildChats != 1 {
		t.Fatalf("expected default max child chats applied, got %d", cfg.MaxChildChats)
	}
}

func TestApplyDefaultsFillsVoiceAudioSettings(t *testing.T) {
	cfg := Default()
	cfg.Voice = Voice{
		STTProviderID: " speech ",
		STTModelID:    " asr ",
		TTSProviderID: " speech ",
		TTSModelID:    " tts ",
	}

	cfg.applyDefaults()

	if cfg.Voice.STTProviderID != "speech" || cfg.Voice.STTModelID != "asr" || cfg.Voice.TTSProviderID != "speech" || cfg.Voice.TTSModelID != "tts" {
		t.Fatalf("expected voice model ids to be normalized, got %#v", cfg.Voice)
	}
	if cfg.Voice.STTLanguage != "auto" || cfg.Voice.TTSLanguage != "en" || cfg.Voice.TTSVoice != "alloy" {
		t.Fatalf("expected voice text defaults, got %#v", cfg.Voice)
	}
	if cfg.Voice.InputSampleRate != 16000 || cfg.Voice.OutputSampleRate != 24000 || cfg.Voice.MaxUtteranceSeconds != 60 {
		t.Fatalf("expected voice audio defaults, got %#v", cfg.Voice)
	}
}

func TestLoadBackfillsMissingCompactionPreferences(t *testing.T) {
	temp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", temp)
	t.Setenv("XDG_STATE_HOME", temp)
	t.Setenv("XDG_CACHE_HOME", temp)

	configRoot := filepath.Join(temp, "koder")
	if err := os.MkdirAll(configRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	raw := []byte("[ui]\ntheme = \"tokyonight\"\n")
	if err := os.WriteFile(filepath.Join(configRoot, "config.toml"), raw, 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Compaction.AutoAtPercent != defaultAutoCompactAt {
		t.Fatalf("expected auto compact threshold backfilled to %d, got %d", defaultAutoCompactAt, cfg.Compaction.AutoAtPercent)
	}
	if cfg.Compaction.KeepToolCalls != defaultCompactionKeepToolCalls {
		t.Fatalf("expected keep tool calls backfilled to %d, got %d", defaultCompactionKeepToolCalls, cfg.Compaction.KeepToolCalls)
	}
}

func TestModelConfigHelpersNormalizeAndDefault(t *testing.T) {
	cfg := Default()
	cfg.Models = []ModelConfig{
		{ProviderID: " test ", ModelID: " model ", ContextWindow: 12345, ReasoningEffort: " XHIGH "},
	}

	cfg.applyDefaults()

	if got := cfg.ContextWindow("test", "model"); got != 12345 {
		t.Fatalf("expected configured context window, got %d", got)
	}
	if got := cfg.ContextWindow("test", "missing"); got != 32768 {
		t.Fatalf("expected default context window, got %d", got)
	}
	if got := cfg.ModelPreset("test", "model"); got != "auto" {
		t.Fatalf("expected default model preset, got %q", got)
	}
	model, ok := cfg.ModelConfig("test", "model")
	if !ok || model.ReasoningEffort != "xhigh" {
		t.Fatalf("expected normalized reasoning effort, got %#v", model)
	}
}

func TestModelConfigResolvesCustomModelSource(t *testing.T) {
	temperature := 0.2
	cfg := Default()
	cfg.Models = []ModelConfig{
		{
			ProviderID:       "local",
			ModelID:          "qwen coding",
			SourceProviderID: "local",
			SourceModelID:    "Qwen/Qwen3.6-35B-A3B",
			ContextWindow:    65536,
			Temperature:      &temperature,
		},
	}
	cfg.applyDefaults()

	providerID, modelID := cfg.ResolveModel("local", "qwen coding")
	if providerID != "local" || modelID != "Qwen/Qwen3.6-35B-A3B" {
		t.Fatalf("resolved model = %s/%s", providerID, modelID)
	}
	if got := cfg.ContextWindow("local", "qwen coding"); got != 65536 {
		t.Fatalf("context window = %d", got)
	}
	request := cfg.ModelRequestOptions("local", "qwen coding")
	if request.ModelID != "Qwen/Qwen3.6-35B-A3B" || request.Temperature == nil || *request.Temperature != 0.2 {
		t.Fatalf("unexpected request options: %#v", request)
	}
}

func TestModelConfigExtraBodyTOMLRoundTrip(t *testing.T) {
	cfg := Default()
	cfg.Models = []ModelConfig{
		{
			ProviderID:    "local",
			ModelID:       "custom",
			ContextWindow: 32768,
			ExtraBody: map[string]any{
				"reasoning_effort": "high",
				"temperature":      0.2,
				"chat_template_kwargs": map[string]any{
					"enable_thinking": false,
				},
			},
		},
	}
	data, err := toml.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	var got Config
	if err := toml.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	got.applyDefaults()
	request := got.ModelRequestOptions("local", "custom")
	if request.ExtraBody["reasoning_effort"] != "high" || request.ExtraBody["temperature"] != 0.2 {
		t.Fatalf("unexpected extra body after round trip: %#v", request.ExtraBody)
	}
	nested, ok := request.ExtraBody["chat_template_kwargs"].(map[string]any)
	if !ok || nested["enable_thinking"] != false {
		t.Fatalf("unexpected nested extra body after round trip: %#v", request.ExtraBody)
	}
}

func TestApplyDefaultsFillsMissingMCPServerDefaults(t *testing.T) {
	cfg := Default()
	cfg.MCPServers["docs"] = MCPServer{
		URL: "https://mcp.example.com",
	}

	cfg.applyDefaults()

	server := cfg.MCPServers["docs"]
	if server.StartupTimeout != 10*time.Second {
		t.Fatalf("expected startup timeout default, got %s", server.StartupTimeout)
	}
	if server.RequestTimeout != 30*time.Second {
		t.Fatalf("expected request timeout default, got %s", server.RequestTimeout)
	}
	if server.Headers == nil {
		t.Fatal("expected headers map to be initialized")
	}
}

func TestMCPServerResolvesBearerTokenEnv(t *testing.T) {
	t.Setenv("MCP_TOKEN", "secret")
	cfg := Default()
	cfg.MCPServers["docs"] = MCPServer{
		URL:            "https://mcp.example.com",
		BearerTokenEnv: "MCP_TOKEN",
	}

	server, ok := cfg.MCPServer("docs")
	if !ok {
		t.Fatal("expected MCP server lookup to succeed")
	}
	if server.BearerToken != "secret" {
		t.Fatalf("expected bearer token from env, got %q", server.BearerToken)
	}
}
