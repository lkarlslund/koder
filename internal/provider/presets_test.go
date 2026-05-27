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
	got := RequestExtraBody(config.Provider{BaseURL: "https://dashscope-intl.aliyuncs.com/compatible-mode/v1"}, "qwen3.6-plus", ModelPresetQwen36PreserveThinking)
	want := map[string]any{
		"enable_thinking":   false,
		"preserve_thinking": false,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected dashscope body: %#v", got)
	}
}

func TestRequestExtraBodyUsesCompatibleChatTemplateKwargs(t *testing.T) {
	got := RequestExtraBody(config.Provider{BaseURL: "http://127.0.0.1:8000/v1"}, "Qwen/Qwen3.6-35B-A3B", ModelPresetAuto)
	want := map[string]any{
		"chat_template_kwargs": map[string]any{
			"enable_thinking":   false,
			"preserve_thinking": false,
		},
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
	}, "model-a", ModelPresetDefault)
	want := map[string]any{
		"return_progress": true,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected prompt progress body: %#v", got)
	}
}

func TestRequestExtraBodySkipsUnprobedPromptProgress(t *testing.T) {
	got := RequestExtraBody(config.Provider{
		PromptProgressMode: "auto",
	}, "model-a", ModelPresetDefault)
	if got != nil {
		t.Fatalf("expected no extra body before probe, got %#v", got)
	}
}
