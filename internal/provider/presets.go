package provider

import (
	"net/url"
	"strings"

	"github.com/lkarlslund/koder/internal/config"
)

const (
	ModelPresetAuto                   = "auto"
	ModelPresetDefault                = "default"
	ModelPresetQwen36PreserveThinking = "qwen3.6-preserve-thinking"
	qwen36ThinkingTokenBudget         = 128
)

type ModelPreset struct {
	ID          string
	Title       string
	Description string
}

var modelPresets = []ModelPreset{
	{ID: ModelPresetAuto, Title: "Auto", Description: "Match a preset from the selected model name"},
	{ID: ModelPresetDefault, Title: "Default", Description: "No model-specific request overrides"},
	{ID: ModelPresetQwen36PreserveThinking, Title: "Qwen 3.6 Preserve Thinking", Description: "Enable Qwen 3.6 thinking preservation request options with a 128-token reasoning budget on compatible servers"},
}

func Presets() []ModelPreset {
	out := make([]ModelPreset, len(modelPresets))
	copy(out, modelPresets)
	return out
}

func LookupPreset(id string) (ModelPreset, bool) {
	id = normalizePresetID(id)
	for _, preset := range modelPresets {
		if preset.ID == id {
			return preset, true
		}
	}
	return ModelPreset{}, false
}

func NormalizePresetSelection(id string) string {
	id = normalizePresetID(id)
	if _, ok := LookupPreset(id); ok {
		return id
	}
	return ModelPresetAuto
}

func ResolvePresetID(modelID, selected string) string {
	selected = NormalizePresetSelection(selected)
	switch selected {
	case ModelPresetAuto:
		return AutoMatchPresetID(modelID)
	case ModelPresetDefault, ModelPresetQwen36PreserveThinking:
		return selected
	default:
		return ModelPresetDefault
	}
}

func AutoMatchPresetID(modelID string) string {
	if looksLikeQwen36(modelID) {
		return ModelPresetQwen36PreserveThinking
	}
	return ModelPresetDefault
}

func PreserveThinkingEnabled(modelID, selected string) bool {
	return ResolvePresetID(modelID, selected) == ModelPresetQwen36PreserveThinking
}

func RequestExtraBody(cfg config.Provider, modelID, selected string) map[string]any {
	if !PreserveThinkingEnabled(modelID, selected) {
		return nil
	}
	if isDashScopeBaseURL(cfg.BaseURL) {
		return map[string]any{
			"enable_thinking":   true,
			"preserve_thinking": true,
		}
	}
	return map[string]any{
		"thinking_token_budget": qwen36ThinkingTokenBudget,
		"chat_template_kwargs": map[string]any{
			"enable_thinking":   true,
			"preserve_thinking": true,
		},
	}
}

func normalizePresetID(id string) string {
	id = strings.TrimSpace(strings.ToLower(id))
	if id == "" {
		return ModelPresetAuto
	}
	return id
}

func looksLikeQwen36(modelID string) bool {
	modelID = strings.ToLower(strings.TrimSpace(modelID))
	if modelID == "" {
		return false
	}
	return strings.Contains(modelID, "qwen3.6")
}

func isDashScopeBaseURL(raw string) bool {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return false
	}
	host := strings.ToLower(parsed.Hostname())
	return strings.Contains(host, "dashscope.aliyuncs.com") || strings.Contains(host, "dashscope-intl.aliyuncs.com")
}
