package provider

import (
	"hash/fnv"
	"net/url"
	"strings"

	"github.com/lkarlslund/koder/internal/config"
	"github.com/lkarlslund/koder/internal/id"
)

const (
	ModelPresetAuto                   = "auto"
	ModelPresetDefault                = "default"
	ModelPresetQwen36PreserveThinking = "qwen3.6-preserve-thinking"
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

func RequestExtraBody(cfg config.Provider, model config.ModelConfig) map[string]any {
	body := map[string]any{}
	if PromptProgressRequested(cfg) {
		body["return_progress"] = true
	}
	modelID := strings.TrimSpace(model.ModelID)
	selected := strings.TrimSpace(model.ModelPreset)
	if PreserveThinkingEnabled(modelID, selected) {
		if isDashScopeBaseURL(cfg.BaseURL) {
			body["enable_thinking"] = false
			body["preserve_thinking"] = false
		} else {
			body["chat_template_kwargs"] = map[string]any{
				"enable_thinking":   false,
				"preserve_thinking": true,
			}
		}
	}
	applyModelRequestOptions(body, cfg, model)
	if len(body) == 0 {
		return nil
	}
	return body
}

func WithLlamaCacheAffinity(body map[string]any, cfg config.Provider, sessionID, chatID id.ID) map[string]any {
	if cfg.LlamaSlots <= 0 || !looksLikeLlamaProvider(cfg) {
		return body
	}
	key := strings.TrimSpace(string(chatID))
	if config.NormalizeLlamaSlotScope(cfg.LlamaSlotScope) == "session" {
		key = strings.TrimSpace(string(sessionID))
	}
	if key == "" {
		return body
	}
	if body == nil {
		body = map[string]any{}
	}
	body["cache_prompt"] = true
	body["id_slot"] = stableSlot(key, cfg.LlamaSlots)
	return body
}

func stableSlot(key string, slots int) int {
	if slots <= 0 {
		return 0
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(key))
	return int(h.Sum32() % uint32(slots))
}

func applyModelRequestOptions(body map[string]any, cfg config.Provider, model config.ModelConfig) {
	if model.Temperature != nil {
		body["temperature"] = *model.Temperature
	}
	if model.TopP != nil {
		body["top_p"] = *model.TopP
	}
	if model.MinP != nil {
		body["min_p"] = *model.MinP
	}
	if model.TopK > 0 {
		body["top_k"] = model.TopK
	}
	if model.RepeatPenalty != nil {
		body["repeat_penalty"] = *model.RepeatPenalty
	}
	switch strings.TrimSpace(strings.ToLower(model.ThinkingMode)) {
	case "enabled":
		setThinkingOptions(body, cfg, true, model.ThinkingBudget)
	case "disabled":
		setThinkingOptions(body, cfg, false, 0)
	case "auto", "":
		if model.ThinkingBudget > 0 {
			setThinkingBudget(body, cfg, model.ThinkingBudget)
		}
	}
}

func setThinkingOptions(body map[string]any, cfg config.Provider, enabled bool, budget int) {
	if isDashScopeBaseURL(cfg.BaseURL) {
		body["enable_thinking"] = enabled
		body["preserve_thinking"] = enabled
		if budget > 0 {
			body["thinking_budget"] = budget
		}
		return
	}
	kwargs := chatTemplateKwargs(body)
	kwargs["enable_thinking"] = enabled
	// Qwen 3.6's llama.cpp-compatible Jinja template changes older assistant
	// formatting when preserve_thinking is false and a new user message is
	// appended. Keep the template shape stable even when thinking is disabled.
	kwargs["preserve_thinking"] = true
	if budget > 0 {
		kwargs["thinking_budget"] = budget
	}
}

func setThinkingBudget(body map[string]any, cfg config.Provider, budget int) {
	if budget <= 0 {
		return
	}
	if isDashScopeBaseURL(cfg.BaseURL) {
		body["thinking_budget"] = budget
		return
	}
	chatTemplateKwargs(body)["thinking_budget"] = budget
}

func chatTemplateKwargs(body map[string]any) map[string]any {
	if existing, ok := body["chat_template_kwargs"].(map[string]any); ok {
		return existing
	}
	next := map[string]any{}
	body["chat_template_kwargs"] = next
	return next
}

func PromptProgressEnabled(cfg config.Provider) bool {
	mode := config.NormalizePromptProgressMode(cfg.PromptProgressMode)
	switch mode {
	case "enabled":
		return true
	case "auto":
		return cfg.PromptProgressProbed && cfg.PromptProgressSupported
	default:
		return false
	}
}

func PromptProgressProbePending(cfg config.Provider) bool {
	return config.NormalizePromptProgressMode(cfg.PromptProgressMode) == "auto" && !cfg.PromptProgressProbed
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

func isDashScopeBaseURL(raw string) bool {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return false
	}
	host := strings.ToLower(parsed.Hostname())
	return strings.Contains(host, "dashscope.aliyuncs.com") || strings.Contains(host, "dashscope-intl.aliyuncs.com")
}

func looksLikeLlamaProvider(cfg config.Provider) bool {
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
