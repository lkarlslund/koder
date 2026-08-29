package settings

import (
	"strings"
	"testing"

	"github.com/lkarlslund/koder/internal/accesssettings"
	"github.com/lkarlslund/koder/internal/config"
	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/provider"
)

func TestToolsUseGlobalEnablementOnly(t *testing.T) {
	cfg := config.Default()
	cfg.Tools.Enabled[domain.ToolKindFileRead] = false
	cfg.Tools.Enabled[domain.ToolKindBash] = true
	store := New(cfg)

	session := domain.Session{
		ToolStates: domain.ToolStates{
			domain.ToolKindFileRead: true,
			domain.ToolKindBash:     false,
		},
	}

	tools := store.Tools(session)
	if tools.Enabled[domain.ToolKindFileRead] {
		t.Fatalf("expected global file_read disable to win, got %#v", tools.Enabled)
	}
	if !tools.Enabled[domain.ToolKindBash] {
		t.Fatalf("expected global bash enable to win, got %#v", tools.Enabled)
	}
}

func TestAccessUsesSessionOverride(t *testing.T) {
	cfg := config.Default()
	cfg.Access.Network = false
	cfg.Access.Project = accesssettings.ModeReadOnly
	store := New(cfg)

	session := domain.Session{
		AccessSettings: accesssettings.Settings{
			Network: true,
			Project: accesssettings.ModeReadWrite,
		},
	}

	got := store.Access(session)
	if !got.Network || got.Project != accesssettings.ModeReadWrite {
		t.Fatalf("expected session access override, got %#v", got)
	}
}

func TestAccessInheritsGlobalMountsForExistingSession(t *testing.T) {
	cfg := config.Default()
	shared := t.TempDir()
	cfg.GlobalMounts = []accesssettings.Mount{{Path: shared, Mode: accesssettings.ModeReadWrite}}
	store := New(cfg)
	session := domain.Session{AccessSettings: accesssettings.LockedDown()}

	got := store.Access(session)
	if len(got.Mounts) != 1 || got.Mounts[0].Path != shared || got.Mounts[0].Mode != accesssettings.ModeReadWrite {
		t.Fatalf("effective access did not inherit global mount: %#v", got)
	}
	if defaults := store.NewSessionDefaults().Access; len(defaults.Mounts) != 0 {
		t.Fatalf("global mounts must not be copied into session defaults: %#v", defaults.Mounts)
	}
}

func TestModelResolvesCustomSource(t *testing.T) {
	cfg := config.Default()
	cfg.Providers["local"] = config.Provider{BaseURL: "http://127.0.0.1:8080/v1"}
	cfg.SetModelConfig(config.ModelConfig{
		ProviderID:       "local",
		ModelID:          "alias",
		SourceProviderID: "local",
		SourceModelID:    "real-model",
		ContextWindow:    12345,
		ModelPreset:      provider.ModelPresetDefault,
	})
	store := New(cfg)

	got, err := store.Model(domain.Chat{ID: "chat-1", ProviderID: "local", ModelID: "alias"})
	if err != nil {
		t.Fatal(err)
	}
	if got.SourceModelID != "real-model" || got.ContextWindow != 12345 || got.Model.ModelPreset != provider.ModelPresetDefault {
		t.Fatalf("unexpected model settings: %#v", got)
	}
}

func TestModelRejectsDisabledProvider(t *testing.T) {
	cfg := config.Default()
	cfg.Providers["offline"] = config.Provider{BaseURL: "http://127.0.0.1:8080/v1", Disabled: true}
	_, err := New(cfg).Model(domain.Chat{ID: "chat-1", ProviderID: "offline", ModelID: "model"})
	if err == nil || !strings.Contains(err.Error(), `provider "offline" is disabled`) {
		t.Fatalf("disabled provider error = %v", err)
	}
}

func TestModelReferenceFollowsUpdatedSystemDefault(t *testing.T) {
	cfg := config.Default()
	cfg.Providers["first"] = config.Provider{BaseURL: "http://127.0.0.1:8080/v1"}
	cfg.Providers["second"] = config.Provider{BaseURL: "http://127.0.0.1:8081/v1"}
	cfg.Defaults.ProviderID = "first"
	cfg.Defaults.ModelID = "model-a"
	store := New(cfg)
	chat := domain.Chat{ID: "chat-1", ProviderID: domain.DefaultModelReference, ModelID: domain.DefaultModelReference}

	first, err := store.Model(chat)
	if err != nil {
		t.Fatal(err)
	}
	if first.ProviderID != "first" || first.ModelID != "model-a" {
		t.Fatalf("first resolved model = %q/%q", first.ProviderID, first.ModelID)
	}

	cfg.Defaults.ProviderID = "second"
	cfg.Defaults.ModelID = "model-b"
	store.Update(cfg)
	second, err := store.Model(chat)
	if err != nil {
		t.Fatal(err)
	}
	if second.ProviderID != "second" || second.ModelID != "model-b" {
		t.Fatalf("updated resolved model = %q/%q", second.ProviderID, second.ModelID)
	}
	if !chat.UsesDefaultModel() {
		t.Fatalf("stored chat assignment changed: %#v", chat)
	}
}

func TestCompactionFallsBackToChatModel(t *testing.T) {
	cfg := config.Default()
	cfg.Providers["chat"] = config.Provider{BaseURL: "http://127.0.0.1:8080/v1"}
	cfg.Compaction.AutoAtPercent = 66
	cfg.SetModelConfig(config.ModelConfig{ProviderID: "chat", ModelID: "model", ContextWindow: 999})
	store := New(cfg)

	got, err := store.Compaction(domain.Chat{ID: "chat-1", ProviderID: "chat", ModelID: "model"}, "compact prompt")
	if err != nil {
		t.Fatal(err)
	}
	if got.ProviderID != "chat" || got.ModelID != "model" || got.ThresholdPercent != 66 || got.Prompt != "compact prompt" {
		t.Fatalf("unexpected compaction settings: %#v", got)
	}
}
