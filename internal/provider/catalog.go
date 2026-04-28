package provider

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/lkarlslund/koder/internal/attachment"
	"github.com/lkarlslund/koder/internal/config"
	"github.com/lkarlslund/koder/internal/debugsrv"
	"github.com/lkarlslund/koder/internal/domain"
)

type AuthMethodKind string

const (
	ProviderKindCompatible string = "openai-compatible"
)

type Descriptor struct {
	ID             string
	Title          string
	Description    string
	DefaultBaseURL string
	Headers        map[string]string
	ModelHint      string
	Local          bool
	SupportsImages bool
	SupportsPDFs   bool
}

type ConnectDraft struct {
	ProviderID string
	Kind       string
	Name       string
	BaseURL    string
	APIKey     string
	Model      string
	Headers    map[string]string
}

type ProbeResult struct {
	Models []domain.Model
}

var catalog = []Descriptor{
	{ID: "openai", Title: "OpenAI", Description: "Direct OpenAI API access", DefaultBaseURL: "https://api.openai.com/v1", ModelHint: "gpt-5.4", SupportsImages: true},
	{ID: "openrouter", Title: "OpenRouter", Description: "Unified OpenAI-compatible gateway", DefaultBaseURL: "https://openrouter.ai/api/v1", ModelHint: "openai/gpt-5.4", SupportsImages: true},
	{ID: "groq", Title: "Groq", Description: "Low-latency OpenAI-compatible API", DefaultBaseURL: "https://api.groq.com/openai/v1", ModelHint: "llama-3.3-70b-versatile", SupportsImages: true},
	{ID: "xai", Title: "xAI", Description: "OpenAI-compatible xAI endpoint", DefaultBaseURL: "https://api.x.ai/v1", ModelHint: "grok-3-mini", SupportsImages: true},
	{ID: "deepseek", Title: "DeepSeek", Description: "DeepSeek OpenAI-compatible API", DefaultBaseURL: "https://api.deepseek.com/v1", ModelHint: "deepseek-chat", SupportsImages: true},
	{ID: "together", Title: "Together", Description: "Together AI OpenAI-compatible API", DefaultBaseURL: "https://api.together.xyz/v1", ModelHint: "meta-llama/Llama-3.3-70B-Instruct-Turbo", SupportsImages: true},
	{ID: "perplexity", Title: "Perplexity", Description: "Perplexity chat completions API", DefaultBaseURL: "https://api.perplexity.ai", ModelHint: "sonar"},
	{ID: "mistral", Title: "Mistral", Description: "Mistral OpenAI-compatible API", DefaultBaseURL: "https://api.mistral.ai/v1", ModelHint: "mistral-large-latest", SupportsImages: true},
	{ID: "cerebras", Title: "Cerebras", Description: "Cerebras OpenAI-compatible API", DefaultBaseURL: "https://api.cerebras.ai/v1", ModelHint: "llama-4-scout-17b-16e-instruct", SupportsImages: true},
	{ID: "ollama", Title: "Ollama", Description: "Local Ollama OpenAI-compatible endpoint", DefaultBaseURL: "http://127.0.0.1:11434/v1", ModelHint: "qwen2.5-coder:latest", Local: true, SupportsImages: true},
	{ID: "openai-compatible", Title: "OpenAI-compatible", Description: "Any OpenAI-compatible API or gateway", DefaultBaseURL: "https://api.openai.com/v1", ModelHint: "model-id", SupportsImages: true},
}

func Catalog() []Descriptor {
	out := make([]Descriptor, len(catalog))
	copy(out, catalog)
	return out
}

func Lookup(id string) (Descriptor, bool) {
	for _, item := range catalog {
		if item.ID == strings.TrimSpace(id) {
			return item, true
		}
	}
	return Descriptor{}, false
}

func BuildDraft(id string, existing map[string]config.Provider) (ConnectDraft, error) {
	desc, ok := Lookup(id)
	if !ok {
		return ConnectDraft{}, fmt.Errorf("provider %q not found", id)
	}
	draft := ConnectDraft{
		ProviderID: desc.ID,
		Kind:       ProviderKindCompatible,
		Name:       desc.Title,
		BaseURL:    desc.DefaultBaseURL,
		Model:      desc.ModelHint,
		Headers:    cloneHeaders(desc.Headers),
	}
	if existingCfg, ok := existing[desc.ID]; ok {
		draft.Kind = firstNonEmpty(existingCfg.Kind, ProviderKindCompatible)
		draft.Name = firstNonEmpty(existingCfg.Name, desc.Title)
		draft.BaseURL = firstNonEmpty(existingCfg.BaseURL, desc.DefaultBaseURL)
		draft.APIKey = existingCfg.APIKey
		draft.Model = firstNonEmpty(existingCfg.DefaultModel, desc.ModelHint)
		draft.Headers = cloneHeaders(existingCfg.Headers)
	}
	return draft, nil
}

func (d ConnectDraft) ToConfig() config.Provider {
	cfg := config.Provider{
		Kind:         firstNonEmpty(d.Kind, ProviderKindCompatible),
		Name:         strings.TrimSpace(d.Name),
		BaseURL:      strings.TrimSpace(d.BaseURL),
		APIKey:       strings.TrimSpace(d.APIKey),
		Headers:      cloneHeaders(d.Headers),
		DefaultModel: strings.TrimSpace(d.Model),
	}
	return cfg
}

func Probe(ctx context.Context, draft ConnectDraft, recorder *debugsrv.Recorder) (ProbeResult, error) {
	client, err := New(draft.ProviderID, draft.ToConfig(), recorder)
	if err != nil {
		return ProbeResult{}, err
	}
	models, err := client.ListModels(ctx)
	if err != nil {
		return ProbeResult{}, err
	}
	slices.SortFunc(models, func(a, b domain.Model) int {
		return strings.Compare(a.ID, b.ID)
	})
	return ProbeResult{Models: models}, nil
}

func ValidateDraft(draft ConnectDraft) error {
	if strings.TrimSpace(draft.ProviderID) == "" {
		return fmt.Errorf("provider id is required")
	}
	if strings.TrimSpace(draft.BaseURL) == "" {
		return fmt.Errorf("base url is required")
	}
	if strings.TrimSpace(draft.Model) == "" {
		return fmt.Errorf("model is required")
	}
	_, err := New(draft.ProviderID, draft.ToConfig(), nil)
	return err
}

func SupportsAttachment(providerID, modelID string, kind attachment.Kind) bool {
	switch kind {
	case attachment.KindText:
		return true
	case attachment.KindPDF:
		return false
	case attachment.KindImage:
		if desc, ok := Lookup(providerID); ok && desc.SupportsImages {
			return modelLikelySupportsImages(modelID)
		}
		return modelLikelySupportsImages(modelID)
	default:
		return false
	}
}

func modelLikelySupportsImages(modelID string) bool {
	model := strings.ToLower(strings.TrimSpace(modelID))
	if model == "" {
		return false
	}
	hints := []string{
		"vision",
		"gpt-4o",
		"gpt-4.1",
		"gpt-5",
		"gemini",
		"claude",
		"llava",
		"vl",
		"pixtral",
		"multimodal",
		"omni",
		"minicpm-v",
		"qwen3.6",
	}
	for _, hint := range hints {
		if strings.Contains(model, hint) {
			return true
		}
	}
	return false
}

func cloneHeaders(src map[string]string) map[string]string {
	if len(src) == 0 {
		return map[string]string{}
	}
	dst := make(map[string]string, len(src))
	for key, value := range src {
		dst[key] = value
	}
	return dst
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
