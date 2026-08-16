package modeloverlay

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestBuiltinsResolveQwen38AndApplyBindings(t *testing.T) {
	catalog := Load(t.TempDir())
	if len(catalog.Problems) != 0 {
		t.Fatalf("Load() problems = %+v", catalog.Problems)
	}

	resolved := catalog.Resolve("qwen3.8-27b-q8-mtp", SelectionAuto, "llama")
	if !reflect.DeepEqual(resolved.IDs, []string{"generic", "qwen3.8"}) {
		t.Fatalf("Resolve() IDs = %v", resolved.IDs)
	}
	if resolved.ReasoningReplay != ReasoningReplaySeparateContent {
		t.Fatalf("ReasoningReplay = %q, want %q", resolved.ReasoningReplay, ReasoningReplaySeparateContent)
	}
	if err := resolved.ValidateValues(map[string]any{"reasoning_effort": "high"}); err == nil {
		t.Fatal("ValidateValues() accepted an unsupported Qwen3.8 reasoning level")
	}

	body := resolved.Apply(nil, map[string]any{"reasoning_effort": "medium", "temperature": 1.0}, "llama")
	want := map[string]any{
		"temperature":      1.0,
		"reasoning_effort": "medium",
		"chat_template_kwargs": map[string]any{
			"enable_thinking":   true,
			"preserve_thinking": true,
			"reasoning_effort":  "medium",
		},
	}
	if !reflect.DeepEqual(body, want) {
		gotJSON, _ := json.Marshal(body)
		wantJSON, _ := json.Marshal(want)
		t.Fatalf("Apply() = %s, want %s", gotJSON, wantJSON)
	}
}

func TestGenericReasoningReplayDefaultsToThinkTag(t *testing.T) {
	resolved := Load(t.TempDir()).Resolve("generic-model", SelectionAuto, "openai")
	if resolved.ReasoningReplay != ReasoningReplayTagThink {
		t.Fatalf("ReasoningReplay = %q, want %q", resolved.ReasoningReplay, ReasoningReplayTagThink)
	}
}

func TestUserOverlayOverridesBuiltinByFilename(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, directoryName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	override := `{
  "version": 1,
  "id": "qwen3.8-custom",
  "title": "Local Qwen",
  "priority": 380,
  "match": {"model_ids": ["*qwen3.8*"]},
  "controls": [{"id": "local_option", "label": "Local", "type": "text", "bindings": [{"path": "local_option"}]}]
}`
	if err := os.WriteFile(filepath.Join(dir, "qwen3.8.json"), []byte(override), 0o644); err != nil {
		t.Fatal(err)
	}

	catalog := Load(root)
	resolved := catalog.Resolve("qwen3.8-27b", SelectionAuto, "llama")
	if !reflect.DeepEqual(resolved.IDs, []string{"generic", "qwen3.8-custom"}) {
		t.Fatalf("Resolve() IDs = %v", resolved.IDs)
	}
	if err := resolved.ValidateValues(map[string]any{"local_option": "yes"}); err != nil {
		t.Fatalf("ValidateValues() error = %v", err)
	}
}

func TestExplicitOverlayAndTransportMatching(t *testing.T) {
	catalog := Load(t.TempDir())
	resolved := catalog.Resolve("unrelated", "qwen3.8-preserve-thinking", "llama")
	if !reflect.DeepEqual(resolved.IDs, []string{"generic", "qwen3.8"}) {
		t.Fatalf("legacy explicit selection resolved IDs = %v", resolved.IDs)
	}

	body := resolved.Apply(nil, map[string]any{"thinking_mode": "disabled"}, "openai")
	if _, exists := body["chat_template_kwargs"]; exists {
		t.Fatalf("llama-only binding leaked to openai request: %+v", body)
	}
}
