package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
	if cfg.MaxToolLoopSteps != 20 {
		t.Fatalf("expected default max tool loop steps 20, got %d", cfg.MaxToolLoopSteps)
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
	if cfg.UI.Spinner != "dots" {
		t.Fatalf("expected default spinner dots, got %q", cfg.UI.Spinner)
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

func TestApplyDefaultsInfersProviderKindAndAuthMethod(t *testing.T) {
	cfg := Default()
	cfg.Providers["remote"] = Provider{
		BaseURL:      "https://example.com/v1",
		APIKeyEnv:    "EXAMPLE_KEY",
		DefaultModel: "gpt-test",
	}
	cfg.Providers["local"] = Provider{
		BaseURL:      "http://127.0.0.1:11434/v1",
		DefaultModel: "local-model",
	}

	cfg.applyDefaults()

	if got := cfg.Providers["remote"].Kind; got != "openai-compatible" {
		t.Fatalf("expected inferred provider kind, got %q", got)
	}
	if got := cfg.Providers["remote"].AuthMethod; got != "api_key" {
		t.Fatalf("expected api_key auth method, got %q", got)
	}
	if got := cfg.Providers["local"].AuthMethod; got != "local_endpoint" {
		t.Fatalf("expected local endpoint auth method, got %q", got)
	}
}

func TestApplyDefaultsFillsMissingUISpinner(t *testing.T) {
	cfg := Default()
	cfg.UI.Spinner = ""

	cfg.applyDefaults()

	if cfg.UI.Spinner != "dots" {
		t.Fatalf("expected spinner default applied, got %q", cfg.UI.Spinner)
	}
}

func TestApplyDefaultsFillsMissingMaxToolLoopSteps(t *testing.T) {
	cfg := Default()
	cfg.MaxToolLoopSteps = 0

	cfg.applyDefaults()

	if cfg.MaxToolLoopSteps != 20 {
		t.Fatalf("expected default max tool loop steps applied, got %d", cfg.MaxToolLoopSteps)
	}
}
