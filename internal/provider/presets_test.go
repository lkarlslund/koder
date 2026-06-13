package provider

import (
	"reflect"
	"testing"

	"github.com/lkarlslund/koder/internal/config"
	"github.com/lkarlslund/koder/internal/id"
)

func TestAutoMatchPresetIDMatchesQwen36(t *testing.T) {
	if got := AutoMatchPresetID("Qwen/Qwen3.6-35B-A3B"); got != ModelPresetQwen36PreserveThinking {
		t.Fatalf("expected qwen3.6 preset, got %q", got)
	}
	if got := AutoMatchPresetID("gpt-5.4"); got != ModelPresetDefault {
		t.Fatalf("expected default preset, got %q", got)
	}
}

func TestRequestExtraBodyUsesDashScopeShape(t *testing.T) {
	got := RequestExtraBody(config.Provider{BaseURL: "https://dashscope-intl.aliyuncs.com/compatible-mode/v1"}, config.ModelConfig{ModelID: "qwen3.6-plus", ModelPreset: ModelPresetQwen36PreserveThinking})
	want := map[string]any{
		"enable_thinking":   false,
		"preserve_thinking": false,
		"return_progress":   true,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected dashscope body: %#v", got)
	}
}

func TestRequestExtraBodyUsesCompatibleChatTemplateKwargs(t *testing.T) {
	got := RequestExtraBody(config.Provider{BaseURL: "http://127.0.0.1:8000/v1"}, config.ModelConfig{ModelID: "Qwen/Qwen3.6-35B-A3B", ModelPreset: ModelPresetAuto})
	want := map[string]any{
		"chat_template_kwargs": map[string]any{
			"enable_thinking":   false,
			"preserve_thinking": true,
		},
		"return_progress": true,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected compatible body: %#v", got)
	}
}

func TestRequestExtraBodyKeepsCompatibleQwenTemplateCacheStable(t *testing.T) {
	got := RequestExtraBody(config.Provider{BaseURL: "http://127.0.0.1:8000/v1"}, config.ModelConfig{
		ModelID:      "Qwen/Qwen3.6-35B-A3B",
		ModelPreset:  ModelPresetDefault,
		ThinkingMode: "disabled",
	})
	kwargs, ok := got["chat_template_kwargs"].(map[string]any)
	if !ok {
		t.Fatalf("expected chat template kwargs, got %#v", got)
	}
	if kwargs["enable_thinking"] != false || kwargs["preserve_thinking"] != true {
		t.Fatalf("expected disabled thinking with preserved template shape, got %#v", kwargs)
	}
}

func TestRequestExtraBodyIncludesAutoDetectedPromptProgress(t *testing.T) {
	got := RequestExtraBody(config.Provider{
		PromptProgressMode:      "auto",
		PromptProgressProbed:    true,
		PromptProgressSupported: true,
	}, config.ModelConfig{ModelID: "model-a", ModelPreset: ModelPresetDefault})
	want := map[string]any{
		"return_progress": true,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected prompt progress body: %#v", got)
	}
}

func TestRequestExtraBodyIncludesPendingAutoPromptProgress(t *testing.T) {
	got := RequestExtraBody(config.Provider{
		PromptProgressMode: "auto",
	}, config.ModelConfig{ModelID: "model-a", ModelPreset: ModelPresetDefault})
	want := map[string]any{
		"return_progress": true,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected pending prompt progress body: %#v", got)
	}
}

func TestRequestExtraBodyIncludesExplicitModelOptions(t *testing.T) {
	temperature := 0.7
	topP := 0.9
	repeatPenalty := 1.05
	got := RequestExtraBody(config.Provider{BaseURL: "http://127.0.0.1:8000/v1"}, config.ModelConfig{
		ModelID:        "Qwen/Qwen3.6-35B-A3B",
		ModelPreset:    ModelPresetDefault,
		Temperature:    &temperature,
		TopP:           &topP,
		TopK:           40,
		RepeatPenalty:  &repeatPenalty,
		ThinkingMode:   "enabled",
		ThinkingBudget: 4096,
	})
	want := map[string]any{
		"temperature":     0.7,
		"top_p":           0.9,
		"top_k":           40,
		"repeat_penalty":  1.05,
		"return_progress": true,
		"chat_template_kwargs": map[string]any{
			"enable_thinking":   true,
			"preserve_thinking": true,
			"thinking_budget":   4096,
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected explicit options body: %#v", got)
	}
}

func TestRequestExtraBodyIncludesCustomModelJSON(t *testing.T) {
	temperature := 0.7
	got := RequestExtraBody(config.Provider{BaseURL: "http://127.0.0.1:8000/v1"}, config.ModelConfig{
		ModelID:     "custom-model",
		ModelPreset: ModelPresetDefault,
		Temperature: &temperature,
		ExtraBody: map[string]any{
			"temperature":      0.2,
			"reasoning_effort": "high",
			"model":            "ignored",
			"messages":         []any{"ignored"},
			"stream":           false,
			"stream_options":   map[string]any{"include_usage": false},
			"tools":            []any{"ignored"},
			"tool_choice":      "ignored",
		},
	})
	want := map[string]any{
		"temperature":      0.2,
		"reasoning_effort": "high",
		"return_progress":  true,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected custom extra body: %#v", got)
	}
}

func TestWithLlamaCacheAffinityPinsLocalChatSlot(t *testing.T) {
	chatID := id.ID("019e8888-0000-7000-8000-000000000001")
	got := WithLlamaCacheAffinity(nil, config.Provider{
		BaseURL:        "http://127.0.0.1:8000/v1",
		LlamaSlots:     8,
		LlamaSlotScope: "chat",
	}, "session-a", chatID)
	if got["cache_prompt"] != true {
		t.Fatalf("expected cache_prompt=true, got %#v", got)
	}
	slot, ok := got["id_slot"].(int)
	if !ok || slot < 0 || slot >= 8 {
		t.Fatalf("expected bounded integer id_slot, got %#v", got["id_slot"])
	}
	again := WithLlamaCacheAffinity(nil, config.Provider{
		BaseURL:        "http://127.0.0.1:8000/v1",
		LlamaSlots:     8,
		LlamaSlotScope: "chat",
	}, "session-a", chatID)
	if again["id_slot"] != slot {
		t.Fatalf("expected stable slot, got %v then %v", slot, again["id_slot"])
	}
}

func TestWithLlamaCacheAffinityUsesSessionScope(t *testing.T) {
	cfg := config.Provider{
		BaseURL:        "http://localhost:8000/v1",
		LlamaSlots:     16,
		LlamaSlotScope: "session",
	}
	first := WithLlamaCacheAffinity(nil, cfg, "session-a", "chat-a")
	second := WithLlamaCacheAffinity(nil, cfg, "session-a", "chat-b")
	if first["id_slot"] != second["id_slot"] {
		t.Fatalf("expected session scoped chats to share a slot, got %v and %v", first["id_slot"], second["id_slot"])
	}
}

func TestWithLlamaCacheAffinitySkipsRemoteCompatibleProvider(t *testing.T) {
	got := WithLlamaCacheAffinity(map[string]any{"return_progress": true}, config.Provider{
		BaseURL:    "https://api.example.com/v1",
		LlamaSlots: 8,
	}, "session-a", "chat-a")
	if _, ok := got["id_slot"]; ok {
		t.Fatalf("did not expect remote provider id_slot, got %#v", got)
	}
	if got["return_progress"] != true {
		t.Fatalf("expected existing body fields to remain, got %#v", got)
	}
}
