package provider

import (
	"reflect"
	"testing"

	"github.com/lkarlslund/koder/internal/config"
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
			"preserve_thinking": false,
		},
		"return_progress": true,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected compatible body: %#v", got)
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
