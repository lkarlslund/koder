package settings

import (
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
