package provider

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/lkarlslund/koder/internal/attachment"
	"github.com/lkarlslund/koder/internal/config"
	"github.com/lkarlslund/koder/internal/debugsrv"
	"github.com/lkarlslund/koder/internal/domain"
)

// ProviderKindCompatible is the only kind of provider: a server reached by
// URL, whose endpoints and request options are probed rather than chosen.
const ProviderKindCompatible string = "openai-compatible"

type ConnectDraft struct {
	OriginalProviderID      string
	ProviderID              string
	Name                    string
	BaseURL                 string
	Transport               string
	APIKey                  string
	APIKeyEnv               string
	Model                   string
	Stream                  bool
	Timeout                 time.Duration
	Disabled                bool
	Headers                 map[string]string
	PromptProgressMode      string
	PromptProgressProbed    bool
	PromptProgressSupported bool
	PromptProgressCheckedAt time.Time
	PromptProgressTarget    string
	Features                *config.ProviderFeatures
}

type ProbeResult struct {
	Models                  []domain.Model
	SelectedModel           string
	BaseURL                 string
	Transport               string
	Features                config.ProviderFeatures
	PromptProgressProbed    bool
	PromptProgressSupported bool
	PromptProgressCheckedAt time.Time
	PromptProgressTarget    string
}

// NewDraft returns an empty draft for a new provider: probing fills in what
// the server supports and names it.
func NewDraft() ConnectDraft {
	return ConnectDraft{
		Stream:             true,
		Timeout:            2 * time.Minute,
		Headers:            map[string]string{},
		PromptProgressMode: config.NormalizePromptProgressMode("auto"),
	}
}

func BuildDraftForExisting(id string, existing config.Provider) ConnectDraft {
	return ConnectDraft{
		OriginalProviderID:      id,
		ProviderID:              id,
		Name:                    firstNonEmpty(existing.Name, id),
		BaseURL:                 existing.BaseURL,
		Transport:               existing.Transport,
		APIKey:                  existing.APIKey,
		APIKeyEnv:               existing.APIKeyEnv,
		Stream:                  existing.Stream,
		Timeout:                 existing.Timeout,
		Disabled:                existing.Disabled,
		Headers:                 cloneHeaders(existing.Headers),
		PromptProgressMode:      config.NormalizePromptProgressMode(existing.PromptProgressMode),
		PromptProgressProbed:    existing.PromptProgressProbed,
		PromptProgressSupported: existing.PromptProgressSupported,
		PromptProgressCheckedAt: existing.PromptProgressCheckedAt,
		PromptProgressTarget:    existing.PromptProgressTarget,
		Features:                existing.Features,
	}
}

func (d ConnectDraft) ToConfig() config.Provider {
	cfg := config.Provider{
		Kind:                    ProviderKindCompatible,
		Name:                    strings.TrimSpace(d.Name),
		BaseURL:                 strings.TrimSpace(d.BaseURL),
		Transport:               config.NormalizeTransport(d.Transport),
		APIKey:                  strings.TrimSpace(d.APIKey),
		APIKeyEnv:               strings.TrimSpace(d.APIKeyEnv),
		Headers:                 cloneHeaders(d.Headers),
		Stream:                  d.Stream,
		Timeout:                 d.Timeout,
		Disabled:                d.Disabled,
		PromptProgressMode:      config.NormalizePromptProgressMode(d.PromptProgressMode),
		PromptProgressProbed:    d.PromptProgressProbed,
		PromptProgressSupported: d.PromptProgressSupported,
		PromptProgressCheckedAt: d.PromptProgressCheckedAt,
		PromptProgressTarget:    d.PromptProgressTarget,
		Features:                d.Features,
	}
	return cfg
}

// Probe looks for everything Koder knows how to use on the draft's server:
// the endpoints Detect checks and, when it serves chat, which request
// options it takes (llama.cpp-style chat template arguments, streamed prompt
// progress). The transport follows from what it found, so nobody has to
// know what kind of server it is.
func Probe(ctx context.Context, draft ConnectDraft, recorder *debugsrv.Recorder) (ProbeResult, error) {
	detection, err := Detect(ctx, draft.ProviderID, draft.ToConfig(), recorder)
	if err != nil {
		return ProbeResult{}, err
	}
	draft.BaseURL = detection.BaseURL
	draft.Transport = detection.Transport
	draft.Features = &detection.Features
	models := detection.Models
	slices.SortFunc(models, func(a, b domain.Model) int {
		return strings.Compare(a.ID, b.ID)
	})
	selected, err := selectedProbeModel(models, draft.Model)
	if err != nil {
		return ProbeResult{}, err
	}
	result := ProbeResult{
		Models:        models,
		SelectedModel: selected,
		BaseURL:       detection.BaseURL,
		Transport:     detection.Transport,
		Features:      detection.Features,
	}
	if !detection.Features.Chat {
		return result, nil
	}
	previous := draft.Features != nil && draft.Features.TemplateKwargs
	result.Features.TemplateKwargs = acceptsTemplateKwargs(ctx, draft, selected, previous, recorder)
	if result.Features.TemplateKwargs && result.Transport == config.TransportOpenAI {
		result.Transport = config.TransportLlama
	}
	draft.Transport = result.Transport
	probed, supported := probePromptProgress(ctx, draft, selected, recorder)
	result.PromptProgressProbed, result.PromptProgressSupported = probed, supported
	if probed {
		result.PromptProgressCheckedAt = time.Now()
		result.PromptProgressTarget = config.PromptProgressObservationTarget(draft.ToConfig())
	}
	return result, nil
}

// acceptsTemplateKwargs reports whether the server takes chat template
// arguments, which llama.cpp, vLLM, SGLang and similar servers pass to the
// model's template (enable_thinking among them), and which hosted APIs
// reject as unknown. A failure that is not a rejection, such as a busy
// server, keeps what was found before.
func acceptsTemplateKwargs(ctx context.Context, draft ConnectDraft, modelID string, previous bool, recorder *debugsrv.Recorder) bool {
	cfg := draft.ToConfig()
	cfg.Stream = false
	client, err := New(draft.ProviderID, cfg, recorder)
	if err != nil {
		return previous
	}
	_, err = client.CompleteChat(ctx, ChatRequest{
		Model:    strings.TrimSpace(modelID),
		Messages: []Message{{Role: RoleUser, Content: "ping"}},
		ExtraBody: map[string]any{
			"max_tokens":           1,
			"chat_template_kwargs": map[string]any{"enable_thinking": false},
		},
	})
	if err == nil {
		return true
	}
	if apiErr, ok := errors.AsType[*APIError](err); ok && apiErr.StatusCode >= 400 && apiErr.StatusCode < 500 {
		return false
	}
	return previous
}

// probePromptProgress streams a one-token reply asking for prompt progress
// and reports whether the server sent any.
func probePromptProgress(ctx context.Context, draft ConnectDraft, modelID string, recorder *debugsrv.Recorder) (bool, bool) {
	mode := config.NormalizePromptProgressMode(draft.PromptProgressMode)
	if mode == "disabled" {
		return false, false
	}
	probe := draft
	probe.PromptProgressMode = "enabled"
	probe.PromptProgressProbed = true
	probe.PromptProgressSupported = true
	client, err := New(probe.ProviderID, probe.ToConfig(), recorder)
	if err != nil {
		return true, false
	}
	req := ChatRequest{
		Model:    strings.TrimSpace(modelID),
		Messages: []Message{{Role: RoleUser, Content: "ping"}},
		Stream:   true,
		ExtraBody: map[string]any{
			"max_tokens":      1,
			"return_progress": true,
		},
	}
	resp, err := client.StreamChatResponse(ctx, req, nil)
	return true, err == nil && resp.PromptProgressSeen
}

// selectedProbeModel is the requested model when the server lists it, or
// else its first chat model.
func selectedProbeModel(models []domain.Model, requested string) (string, error) {
	if len(models) == 0 {
		return "", fmt.Errorf("provider returned no models")
	}
	requested = strings.TrimSpace(requested)
	if requested != "" {
		for _, model := range models {
			if model.ID == requested {
				return requested, nil
			}
		}
	}
	for _, model := range models {
		if model.SupportsChat || !model.ChatKnown {
			return strings.TrimSpace(model.ID), nil
		}
	}
	return strings.TrimSpace(models[0].ID), nil
}

// ValidateDraft checks a draft can be probed. A new provider has no ID
// until probing names it.
func ValidateDraft(draft ConnectDraft) error {
	if strings.TrimSpace(draft.BaseURL) == "" {
		return fmt.Errorf("base url is required")
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
		_ = providerID
		_ = modelID
		return false
	default:
		return false
	}
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

// UniqueProviderID returns base, or base with a numeric suffix when a
// provider already uses it.
func UniqueProviderID(base string, existing map[string]config.Provider) string {
	base = strings.TrimSpace(base)
	if base == "" {
		base = "provider"
	}
	if len(existing) == 0 {
		return base
	}
	if _, ok := existing[base]; !ok {
		return base
	}
	for idx := 2; ; idx++ {
		candidate := fmt.Sprintf("%s-%d", base, idx)
		if _, ok := existing[candidate]; !ok {
			return candidate
		}
	}
}
