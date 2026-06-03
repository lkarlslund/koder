package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lkarlslund/koder/internal/toolkind"
)

func TestLoadWritesDefaultConfig(t *testing.T) {
	temp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", temp)
	t.Setenv("XDG_STATE_HOME", temp)
	t.Setenv("XDG_CACHE_HOME", temp)

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DefaultProvider != "" {
		t.Fatalf("expected no default provider, got %q", cfg.DefaultProvider)
	}
	if cfg.MaxToolLoopSteps != 500 {
		t.Fatalf("expected default max tool loop steps 500, got %d", cfg.MaxToolLoopSteps)
	}
	if cfg.AutoCompactAt != defaultAutoCompactAt {
		t.Fatalf("expected default auto compact threshold %d, got %d", defaultAutoCompactAt, cfg.AutoCompactAt)
	}
	if cfg.CompactionKeepToolCalls != defaultCompactionKeepToolCalls {
		t.Fatalf("expected default kept tool calls %d, got %d", defaultCompactionKeepToolCalls, cfg.CompactionKeepToolCalls)
	}
	if cfg.CompactionProvider != "" || cfg.CompactionModel != "" {
		t.Fatalf("expected chat model compaction default, got %q/%q", cfg.CompactionProvider, cfg.CompactionModel)
	}
	if cfg.Permissions.Profile != "default" {
		t.Fatalf("unexpected permission profile: %s", cfg.Permissions.Profile)
	}
	if cfg.Store.Backend != "pebble" {
		t.Fatalf("unexpected store backend: %s", cfg.Store.Backend)
	}
	if !cfg.UI.AutoContinue {
		t.Fatal("expected auto continue enabled by default")
	}
	if cfg.Thinking.CavemanPrompt != DefaultCavemanThinkingPrompt {
		t.Fatalf("expected default caveman prompt, got %q", cfg.Thinking.CavemanPrompt)
	}
	if len(cfg.Permissions.Profiles) == 0 {
		t.Fatal("expected permission profiles")
	}
	if len(cfg.Providers) != 0 {
		t.Fatalf("expected no providers in default config, got %#v", cfg.Providers)
	}
	if _, err := os.Stat(filepath.Join(temp, "koder", "config.toml")); err != nil {
		t.Fatalf("expected config file: %v", err)
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
	cfg.Thinking.CavemanProvider = "test"
	cfg.Thinking.CavemanModel = "model"
	cfg.Thinking.CavemanPrompt = "rewrite:\n{{thinking}}"
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.Thinking.CavemanEnabled || loaded.Thinking.CavemanProvider != "test" || loaded.Thinking.CavemanModel != "model" || loaded.Thinking.CavemanPrompt != "rewrite:\n{{thinking}}" {
		t.Fatalf("expected thinking settings to round-trip, got %#v", loaded.Thinking)
	}
}

func TestProviderLlamaSlotSettingsNormalizeAndRoundTrip(t *testing.T) {
	temp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", temp)
	t.Setenv("XDG_STATE_HOME", temp)
	t.Setenv("XDG_CACHE_HOME", temp)

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	cfg.Providers["local"] = Provider{
		BaseURL:        "http://127.0.0.1:8000/v1",
		LlamaSlots:     4,
		LlamaSlotScope: "SESSION",
	}
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	provider := loaded.Providers["local"]
	if provider.LlamaSlots != 4 || provider.LlamaSlotScope != "session" {
		t.Fatalf("expected normalized llama slot settings, got %#v", provider)
	}
}

func TestNormalizeLlamaSlotScopeDefaultsToChat(t *testing.T) {
	for _, value := range []string{"", "invalid", "CHAT"} {
		if got := NormalizeLlamaSlotScope(value); got != "chat" {
			t.Fatalf("expected %q to normalize to chat, got %q", value, got)
		}
	}
	if got := NormalizeLlamaSlotScope("SESSION"); got != "session" {
		t.Fatalf("expected session scope, got %q", got)
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
	cfg.CompactionProvider = "fast"
	cfg.CompactionModel = "fast-model"
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.CompactionProvider != "fast" || loaded.CompactionModel != "fast-model" {
		t.Fatalf("expected compaction override fast/fast-model, got %q/%q", loaded.CompactionProvider, loaded.CompactionModel)
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
[tool_defaults]
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
	for _, kind := range []toolkind.Kind{
		toolkind.ToolKindBash,
		toolkind.ToolKindExecWriteStdin,
		toolkind.ToolKindExecCleanup,
		toolkind.ToolKindMilestoneAdd,
		toolkind.ToolKindMilestonePlan,
		toolkind.ToolKindMilestoneUpdate,
	} {
		if cfg.ToolDefaults[kind] {
			t.Fatalf("expected %s to stay disabled: %#v", kind, cfg.ToolDefaults)
		}
	}
	if !cfg.ToolDefaults[toolkind.ToolKindFileRead] {
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
[tool_defaults]
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
	if cfg.ToolDefaults[toolkind.ToolKindFileRead] {
		t.Fatalf("expected current file_read setting to stay disabled: %#v", cfg.ToolDefaults)
	}
	for _, kind := range toolkind.KindValues() {
		if !kind.IsAKind() {
			t.Fatalf("loaded invalid tool kind %d", kind)
		}
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

	cfg.applyDefaults()

	if cfg.MaxToolLoopSteps != 500 {
		t.Fatalf("expected default max tool loop steps applied, got %d", cfg.MaxToolLoopSteps)
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
	if cfg.AutoCompactAt != defaultAutoCompactAt {
		t.Fatalf("expected auto compact threshold backfilled to %d, got %d", defaultAutoCompactAt, cfg.AutoCompactAt)
	}
	if cfg.CompactionKeepToolCalls != defaultCompactionKeepToolCalls {
		t.Fatalf("expected kept tool calls backfilled to %d, got %d", defaultCompactionKeepToolCalls, cfg.CompactionKeepToolCalls)
	}
}

func TestNormalizeCompactionKeepToolCallsClampsRange(t *testing.T) {
	if got := NormalizeCompactionKeepToolCalls(-1); got != 0 {
		t.Fatalf("expected low clamp to 0, got %d", got)
	}
	if got := NormalizeCompactionKeepToolCalls(11); got != maxCompactionKeepToolCalls {
		t.Fatalf("expected high clamp to %d, got %d", maxCompactionKeepToolCalls, got)
	}
	if got := NormalizeCompactionKeepToolCalls(4); got != 4 {
		t.Fatalf("expected in-range value unchanged, got %d", got)
	}
}

func TestModelConfigHelpersNormalizeAndDefault(t *testing.T) {
	cfg := Default()
	cfg.Models = []ModelConfig{
		{ProviderID: " test ", ModelID: " model ", ContextWindow: 12345},
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
