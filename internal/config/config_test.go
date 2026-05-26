package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/permissionprofile"
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
	if cfg.CompactionKeepToolBatches != defaultCompactionKeepToolBatches {
		t.Fatalf("expected default kept tool batches %d, got %d", defaultCompactionKeepToolBatches, cfg.CompactionKeepToolBatches)
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
	if !cfg.UI.HalfBlocks {
		t.Fatal("expected half block mode enabled by default")
	}
	if cfg.UI.CodeStyle != "github" {
		t.Fatalf("expected default code style github, got %q", cfg.UI.CodeStyle)
	}
	if cfg.UI.EditForgiveness != 3 {
		t.Fatalf("expected default edit forgiveness 3, got %d", cfg.UI.EditForgiveness)
	}
	if !cfg.UI.CursorBlink {
		t.Fatal("expected cursor blinking enabled by default")
	}
	if !cfg.UI.AutoContinue {
		t.Fatal("expected auto continue enabled by default")
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

func TestApplyDefaultsPrunesRemovedToolDefaults(t *testing.T) {
	cfg := Default()
	cfg.ToolDefaults[domain.ToolKind("apply_patch")] = true

	cfg.applyDefaults()

	if _, ok := cfg.ToolDefaults[domain.ToolKind("apply_patch")]; ok {
		t.Fatalf("expected removed tool default to be pruned: %#v", cfg.ToolDefaults)
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

func TestApplyDefaultsFillsMissingUIStyleSettings(t *testing.T) {
	cfg := Default()
	cfg.UI.CodeStyle = ""
	cfg.UI.EditForgiveness = 0

	cfg.applyDefaults()

	if cfg.UI.CodeStyle != "github" {
		t.Fatalf("expected code style default applied, got %q", cfg.UI.CodeStyle)
	}
	if cfg.UI.EditForgiveness != 3 {
		t.Fatalf("expected edit forgiveness default applied, got %d", cfg.UI.EditForgiveness)
	}
}

func TestSaveAndLoadRoundTripsSidebarWidthPreference(t *testing.T) {
	temp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", temp)
	t.Setenv("XDG_STATE_HOME", temp)
	t.Setenv("XDG_CACHE_HOME", temp)

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	cfg.UI.SidebarWidth = 37
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}

	reloaded, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.UI.SidebarWidth != 37 {
		t.Fatalf("expected sidebar width 37, got %d", reloaded.UI.SidebarWidth)
	}
}

func TestNormalizeEditForgivenessClampsRange(t *testing.T) {
	if got := NormalizeEditForgiveness(0); got != 1 {
		t.Fatalf("expected low clamp to 1, got %d", got)
	}
	if got := NormalizeEditForgiveness(6); got != 5 {
		t.Fatalf("expected high clamp to 5, got %d", got)
	}
	if got := NormalizeEditForgiveness(3); got != 3 {
		t.Fatalf("expected in-range value unchanged, got %d", got)
	}
}

func TestLoadBackfillsMissingCursorBlinkSetting(t *testing.T) {
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
	if !cfg.UI.CursorBlink {
		t.Fatal("expected missing cursor_blink setting to default to true")
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
	if cfg.CompactionKeepToolBatches != defaultCompactionKeepToolBatches {
		t.Fatalf("expected kept tool batches backfilled to %d, got %d", defaultCompactionKeepToolBatches, cfg.CompactionKeepToolBatches)
	}
}

func TestNormalizeCompactionKeepToolBatchesClampsRange(t *testing.T) {
	if got := NormalizeCompactionKeepToolBatches(-1); got != 0 {
		t.Fatalf("expected low clamp to 0, got %d", got)
	}
	if got := NormalizeCompactionKeepToolBatches(11); got != maxCompactionKeepToolBatches {
		t.Fatalf("expected high clamp to %d, got %d", maxCompactionKeepToolBatches, got)
	}
	if got := NormalizeCompactionKeepToolBatches(4); got != 4 {
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

func TestApplyDefaultsMigratesRuleProfilesToSandboxProfiles(t *testing.T) {
	cfg := Config{
		Permissions: PermissionRules{
			Profile: "default",
			Profiles: map[string]PermissionProfile{
				"default": {
					Rules: []PermissionRule{
						{Tool: "read", Pattern: "*", Action: "allow"},
						{Tool: "websearch", Pattern: "*", Action: "ask"},
					},
				},
			},
		},
	}

	cfg.applyDefaults()

	profile := cfg.Permissions.Profiles["default"]
	if len(profile.Rules) != 0 {
		t.Fatalf("expected legacy permission rules to be removed, got %#v", profile.Rules)
	}
	if profile.Root != string(permissionprofile.ModeReadOnly) || profile.Workspace != string(permissionprofile.ModeReadWrite) || profile.Network {
		t.Fatalf("unexpected sandbox defaults: %#v", profile)
	}
}

func TestLoadIgnoresLegacyPermissionFields(t *testing.T) {
	temp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", temp)
	t.Setenv("XDG_STATE_HOME", temp)
	t.Setenv("XDG_CACHE_HOME", temp)
	configDir := filepath.Join(temp, "koder")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	data := []byte(`
[permissions]
profile = "default"
read = "deny"
bash = "allow"
`)
	if err := os.WriteFile(filepath.Join(configDir, "config.toml"), data, 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	profile := cfg.Permissions.Profiles["default"]
	for _, rule := range profile.Rules {
		if rule.Tool == "read" && rule.Pattern == "*" && rule.Action != "allow" {
			t.Fatalf("expected legacy read field to be ignored, got %#v", rule)
		}
		if rule.Tool == "bash" && rule.Pattern == "*" && rule.Action != "ask" {
			t.Fatalf("expected legacy bash field to be ignored, got %#v", rule)
		}
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
