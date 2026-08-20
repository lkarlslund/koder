package provider

import (
	"reflect"
	"testing"
	"time"

	"github.com/lkarlslund/koder/internal/config"
	"github.com/lkarlslund/koder/internal/modeloverlay"
)

func requestExtraBody(t *testing.T, cfg config.Provider, model config.ModelConfig) map[string]any {
	t.Helper()
	return RequestExtraBody(cfg, model, modeloverlay.Load(t.TempDir()))
}

func TestAutoMatchPresetIDMatchesQwen36(t *testing.T) {
	if got := AutoMatchPresetID("Qwen/Qwen3.6-35B-A3B"); got != ModelPresetQwen36PreserveThinking {
		t.Fatalf("expected qwen3.6 preset, got %q", got)
	}
	if got := AutoMatchPresetID("gpt-5.4"); got != ModelPresetDefault {
		t.Fatalf("expected default preset, got %q", got)
	}
}

func TestAutoMatchPresetIDMatchesQwen38(t *testing.T) {
	if got := AutoMatchPresetID("ggml-org/Qwen3.8-27B-GGUF"); got != ModelPresetQwen38PreserveThinking {
		t.Fatalf("expected qwen3.8 preset, got %q", got)
	}
}

func TestRequestExtraBodyUsesQwen38ThinkingOptions(t *testing.T) {
	got := requestExtraBody(t, config.Provider{BaseURL: "http://127.0.0.1:8000/v1"}, config.ModelConfig{
		ModelID:         "qwen3.8-27b-q8-mtp",
		ModelPreset:     ModelPresetAuto,
		ThinkingMode:    "enabled",
		ReasoningEffort: "xhigh",
	})
	want := map[string]any{
		"reasoning_effort": "xhigh",
		"return_progress":  true,
		"chat_template_kwargs": map[string]any{
			"enable_thinking":   true,
			"preserve_thinking": true,
			"reasoning_effort":  "xhigh",
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected qwen3.8 body: %#v", got)
	}
}

func TestRequestExtraBodyDoesNotGuessUnsupportedReasoningEffort(t *testing.T) {
	got := requestExtraBody(t, config.Provider{BaseURL: "https://api.example.invalid/v1"}, config.ModelConfig{
		ModelID:         "reasoning-model",
		ModelPreset:     ModelPresetDefault,
		ReasoningEffort: "high",
	})
	if _, ok := got["reasoning_effort"]; ok {
		t.Fatalf("unknown models must not receive guessed reasoning options: %#v", got)
	}
	if _, ok := got["chat_template_kwargs"]; ok {
		t.Fatalf("remote provider must not receive llama.cpp template kwargs: %#v", got)
	}
}

func TestRequestExtraBodyUsesDashScopeShape(t *testing.T) {
	got := requestExtraBody(t, config.Provider{BaseURL: "https://dashscope-intl.aliyuncs.com/compatible-mode/v1"}, config.ModelConfig{ModelID: "qwen3.6-plus", ModelPreset: ModelPresetQwen36PreserveThinking})
	want := map[string]any{
		"enable_thinking":   false,
		"preserve_thinking": true,
		"return_progress":   true,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected dashscope body: %#v", got)
	}
}

func TestRequestExtraBodyUsesCompatibleChatTemplateKwargs(t *testing.T) {
	got := requestExtraBody(t, config.Provider{BaseURL: "http://127.0.0.1:8000/v1"}, config.ModelConfig{ModelID: "Qwen/Qwen3.6-35B-A3B", ModelPreset: ModelPresetAuto})
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

func TestDefaultOverlaySkipsModelSpecificOptions(t *testing.T) {
	got := requestExtraBody(t, config.Provider{BaseURL: "http://127.0.0.1:8000/v1"}, config.ModelConfig{
		ModelID:      "Qwen/Qwen3.6-35B-A3B",
		ModelPreset:  ModelPresetDefault,
		ThinkingMode: "disabled",
	})
	if _, ok := got["chat_template_kwargs"]; ok {
		t.Fatalf("default overlay should omit model-specific options, got %#v", got)
	}
}

func TestRequestExtraBodyIncludesAutoDetectedPromptProgress(t *testing.T) {
	providerCfg := config.WithPromptProgressObservation(config.Provider{PromptProgressMode: "auto"}, true, time.Now())
	got := requestExtraBody(t, providerCfg, config.ModelConfig{ModelID: "model-a", ModelPreset: ModelPresetDefault})
	want := map[string]any{
		"return_progress": true,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected prompt progress body: %#v", got)
	}
}

func TestRequestExtraBodyIncludesPendingAutoPromptProgress(t *testing.T) {
	got := requestExtraBody(t, config.Provider{
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
	got := requestExtraBody(t, config.Provider{BaseURL: "http://127.0.0.1:8000/v1"}, config.ModelConfig{
		ModelID:       "Qwen/Qwen3.6-35B-A3B",
		ModelPreset:   ModelPresetAuto,
		Temperature:   &temperature,
		TopP:          &topP,
		TopK:          40,
		RepeatPenalty: &repeatPenalty,
		ThinkingMode:  "enabled",
		Options: map[string]any{
			"thinking_mode": "enabled",
		},
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
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected explicit options body: %#v", got)
	}
}

func TestRequestExtraBodyIncludesCustomModelJSON(t *testing.T) {
	temperature := 0.7
	got := requestExtraBody(t, config.Provider{BaseURL: "http://127.0.0.1:8000/v1"}, config.ModelConfig{
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

func TestWithLlamaPromptCacheUsesAutomaticSlotScheduling(t *testing.T) {
	got := WithLlamaPromptCache(nil, config.Provider{
		BaseURL: "http://127.0.0.1:8000/v1",
	})
	if got["cache_prompt"] != true {
		t.Fatalf("expected cache_prompt=true, got %#v", got)
	}
	if _, ok := got["id_slot"]; ok {
		t.Fatalf("expected llama.cpp to select the slot, got %#v", got)
	}
}

func TestWithLlamaPromptCacheSkipsRemoteCompatibleProvider(t *testing.T) {
	got := WithLlamaPromptCache(map[string]any{"return_progress": true}, config.Provider{
		BaseURL: "https://api.example.com/v1",
	})
	if _, ok := got["id_slot"]; ok {
		t.Fatalf("did not expect remote provider id_slot, got %#v", got)
	}
	if got["return_progress"] != true {
		t.Fatalf("expected existing body fields to remain, got %#v", got)
	}
}
