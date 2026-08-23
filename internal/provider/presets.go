package provider

import (
	"net/url"
	"strings"

	"github.com/lkarlslund/koder/internal/config"
	"github.com/lkarlslund/koder/internal/modeloverlay"
)

const (
	ModelPresetAuto                   = "auto"
	ModelPresetDefault                = "default"
	ModelPresetQwen36PreserveThinking = "qwen3.6-preserve-thinking"
	ModelPresetQwen38PreserveThinking = "qwen3.8-preserve-thinking"
)

type ModelPreset struct {
	ID          string
	Title       string
	Description string
}

var modelPresets = []ModelPreset{
	{ID: ModelPresetAuto, Title: "Auto", Description: "Match a preset from the selected model name"},
	{ID: ModelPresetDefault, Title: "Default", Description: "No model-specific request overrides"},
	{ID: ModelPresetQwen36PreserveThinking, Title: "Qwen 3.6 No Thinking", Description: "Disable Qwen 3.6 hidden reasoning by default on compatible servers"},
	{ID: ModelPresetQwen38PreserveThinking, Title: "Qwen 3.8 Thinking", Description: "Preserve Qwen 3.8 reasoning across turns on compatible servers"},
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
	case ModelPresetDefault, ModelPresetQwen36PreserveThinking, ModelPresetQwen38PreserveThinking:
		return selected
	default:
		return ModelPresetDefault
	}
}

func AutoMatchPresetID(modelID string) string {
	if looksLikeQwen36(modelID) {
		return ModelPresetQwen36PreserveThinking
	}
	if looksLikeQwen38(modelID) {
		return ModelPresetQwen38PreserveThinking
	}
	return ModelPresetDefault
}

func PreserveThinkingEnabled(cfg config.Provider, model config.ModelConfig, catalog modeloverlay.Catalog) bool {
	resolved := catalog.Resolve(model.ModelID, model.ModelPreset, OverlayTransport(cfg))
	return resolved.BoolValue("preserve_thinking", ModelOptionValues(model))
}

// ReasoningReplay selects how preserved assistant reasoning is serialized in
// subsequent requests. Model overlays default to a tagged content block.
func ReasoningReplay(cfg config.Provider, model config.ModelConfig, catalog modeloverlay.Catalog) string {
	resolved := catalog.Resolve(model.ModelID, model.ModelPreset, OverlayTransport(cfg))
	return resolved.ReasoningReplay
}

// WithReasoningReplay adds preserved reasoning to a historical assistant
// message using the representation selected by the model overlay.
func WithReasoningReplay(message Message, reasoning, replay string) Message {
	reasoning = strings.TrimSpace(reasoning)
	if reasoning == "" {
		return message
	}
	if replay == modeloverlay.ReasoningReplaySeparateContent {
		message.ReasoningContent = reasoning
		return message
	}
	tag, ok := strings.CutPrefix(replay, "tag:")
	if !ok || strings.TrimSpace(tag) == "" {
		tag = "think"
	}
	block := "<" + tag + ">\n" + reasoning + "\n</" + tag + ">"
	if strings.TrimSpace(message.Content) == "" {
		message.Content = block
	} else {
		message.Content = block + "\n\n" + strings.TrimSpace(message.Content)
	}
	return message
}

func RequestExtraBody(cfg config.Provider, model config.ModelConfig, catalog modeloverlay.Catalog) map[string]any {
	body := map[string]any{}
	if PromptProgressRequested(cfg) {
		body["return_progress"] = true
	}
	resolved := catalog.Resolve(model.ModelID, model.ModelPreset, OverlayTransport(cfg))
	body = resolved.Apply(body, ModelOptionValues(model), OverlayTransport(cfg))
	applyCustomExtraBody(body, model.ExtraBody)
	if len(body) == 0 {
		return nil
	}
	return body
}

// ModelOptionValues combines arbitrary overlay settings with legacy typed model
// settings. Explicit overlay values win, which makes migration non-destructive.
func ModelOptionValues(model config.ModelConfig) map[string]any {
	values := make(map[string]any, len(model.Options)+8)
	for key, value := range model.Options {
		values[key] = value
	}
	setLegacy := func(key string, value any, present bool) {
		if _, exists := values[key]; !exists && present {
			values[key] = value
		}
	}
	setLegacy("temperature", pointerValue(model.Temperature), model.Temperature != nil)
	setLegacy("top_p", pointerValue(model.TopP), model.TopP != nil)
	setLegacy("min_p", pointerValue(model.MinP), model.MinP != nil)
	setLegacy("top_k", model.TopK, model.TopK > 0)
	setLegacy("repeat_penalty", pointerValue(model.RepeatPenalty), model.RepeatPenalty != nil)
	setLegacy("thinking_mode", strings.TrimSpace(strings.ToLower(model.ThinkingMode)), strings.TrimSpace(model.ThinkingMode) != "" && model.ThinkingMode != "auto")
	setLegacy("thinking_budget", model.ThinkingBudget, model.ThinkingBudget > 0)
	setLegacy("reasoning_effort", strings.TrimSpace(strings.ToLower(model.ReasoningEffort)), strings.TrimSpace(model.ReasoningEffort) != "" && model.ReasoningEffort != "auto")
	return values
}

func pointerValue(value *float64) any {
	if value == nil {
		return nil
	}
	return *value
}

// OverlayTransport names the request dialect available to JSON bindings.
func OverlayTransport(cfg config.Provider) string {
	if isDashScopeBaseURL(cfg.BaseURL) {
		return "dashscope"
	}
	if looksLikeNinferProvider(cfg) {
		return "ninfer"
	}
	if looksLikeLlamaProvider(cfg) {
		return "llama"
	}
	return "openai"
}

func applyCustomExtraBody(body map[string]any, extra map[string]any) {
	for key, value := range extra {
		key = strings.TrimSpace(key)
		if key == "" || protectedExtraBodyKey(key) {
			continue
		}
		body[key] = value
	}
}

func protectedExtraBodyKey(key string) bool {
	switch strings.ToLower(strings.TrimSpace(key)) {
	case "model", "messages", "stream", "stream_options", "tools", "tool_choice":
		return true
	default:
		return false
	}
}

func WithLlamaPromptCache(body map[string]any, cfg config.Provider) map[string]any {
	if !looksLikeLlamaProvider(cfg) {
		return body
	}
	if body == nil {
		body = map[string]any{}
	}
	body["cache_prompt"] = true
	return body
}

func PromptProgressEnabled(cfg config.Provider) bool {
	mode := config.NormalizePromptProgressMode(cfg.PromptProgressMode)
	switch mode {
	case "enabled":
		return true
	case "auto":
		return config.PromptProgressObservationValid(cfg) && cfg.PromptProgressSupported
	default:
		return false
	}
}

func PromptProgressProbePending(cfg config.Provider) bool {
	return config.NormalizePromptProgressMode(cfg.PromptProgressMode) == "auto" && !config.PromptProgressObservationValid(cfg)
}

func PromptProgressRequested(cfg config.Provider) bool {
	return PromptProgressEnabled(cfg) || PromptProgressProbePending(cfg)
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

func looksLikeQwen38(modelID string) bool {
	modelID = strings.ToLower(strings.TrimSpace(modelID))
	if modelID == "" {
		return false
	}
	return strings.Contains(modelID, "qwen3.8")
}

func isDashScopeBaseURL(raw string) bool {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return false
	}
	host := strings.ToLower(parsed.Hostname())
	return strings.Contains(host, "dashscope.aliyuncs.com") || strings.Contains(host, "dashscope-intl.aliyuncs.com")
}

func looksLikeLlamaProvider(cfg config.Provider) bool {
	if looksLikeNinferProvider(cfg) {
		return false
	}
	for _, value := range []string{cfg.Kind, cfg.TemplateID, cfg.Name} {
		if strings.Contains(strings.ToLower(strings.TrimSpace(value)), "llama") {
			return true
		}
	}
	parsed, err := url.Parse(strings.TrimSpace(cfg.BaseURL))
	if err != nil {
		return false
	}
	switch strings.ToLower(parsed.Hostname()) {
	case "localhost", "127.0.0.1", "::1":
		return true
	default:
		return false
	}
}

func looksLikeNinferProvider(cfg config.Provider) bool {
	for _, value := range []string{cfg.TemplateID, cfg.Name} {
		if strings.Contains(strings.ToLower(strings.TrimSpace(value)), "ninfer") {
			return true
		}
	}
	return false
}
