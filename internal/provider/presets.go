package provider

import (
	"strings"

	"github.com/lkarlslund/koder/internal/config"
	"github.com/lkarlslund/koder/internal/modeloverlay"
)

// Stored model_preset values; the overlay catalog resolves them.
const (
	ModelPresetAuto                   = "auto"
	ModelPresetDefault                = "default"
	ModelPresetQwen36PreserveThinking = "qwen3.6-preserve-thinking"
	ModelPresetQwen38PreserveThinking = "qwen3.8-preserve-thinking"
)

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
// Config loading records it per provider; providers built in memory without
// one fall back to the same inference.
func OverlayTransport(cfg config.Provider) string {
	if transport := config.NormalizeTransport(cfg.Transport); transport != "" {
		return transport
	}
	return config.InferTransport(cfg)
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
	if OverlayTransport(cfg) != config.TransportLlama {
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
