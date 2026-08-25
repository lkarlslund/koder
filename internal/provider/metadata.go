package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/lkarlslund/koder/internal/domain"
)

type nativeV1ModelsResponse struct {
	Models []nativeV1Model `json:"models"`
}

type nativeV1Model struct {
	Key              string `json:"key"`
	Type             string `json:"type"`
	Publisher        string `json:"publisher"`
	MaxContextLength int    `json:"max_context_length"`
	LoadedInstances  []struct {
		ID     string `json:"id"`
		Config struct {
			ContextLength int `json:"context_length"`
		} `json:"config"`
	} `json:"loaded_instances"`
	Capabilities struct {
		Vision            *bool           `json:"vision"`
		TrainedForToolUse *bool           `json:"trained_for_tool_use"`
		Reasoning         json.RawMessage `json:"reasoning"`
	} `json:"capabilities"`
}

type nativeV0ModelsResponse struct {
	Data []nativeV0Model `json:"data"`
}

type nativeV0Model struct {
	ID               string `json:"id"`
	Type             string `json:"type"`
	Publisher        string `json:"publisher"`
	MaxContextLength int    `json:"max_context_length"`
}

type ollamaShowResponse struct {
	Parameters   string         `json:"parameters"`
	Capabilities []string       `json:"capabilities"`
	ModelInfo    map[string]any `json:"model_info"`
}

type endpointSupport uint8

const (
	endpointUnknown endpointSupport = iota
	endpointSupported
	endpointUnsupported
)

var errEndpointUnsupported = errors.New("metadata endpoint is unsupported by this provider connection")

func (c *Client) endpointMayBeSupported(key string) bool {
	c.probeMu.Lock()
	defer c.probeMu.Unlock()
	return c.probes[key] != endpointUnsupported
}

func (c *Client) rememberEndpointSupport(key string, support endpointSupport) {
	c.probeMu.Lock()
	defer c.probeMu.Unlock()
	if c.probes == nil {
		c.probes = make(map[string]endpointSupport)
	}
	c.probes[key] = support
}

func endpointUnsupportedByResponse(err error) bool {
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		return false
	}
	switch apiErr.StatusCode {
	case http.StatusNotFound, http.StatusMethodNotAllowed, http.StatusNotImplemented:
		return true
	default:
		return false
	}
}

// DetectModelMetadata discovers operational metadata using compatible and
// backend-native read-only APIs. A loaded runtime context wins over a model's
// advertised maximum because it is the usable limit for request budgeting.
func (c *Client) DetectModelMetadata(ctx context.Context, modelID string) (domain.Model, error) {
	modelID = strings.TrimSpace(modelID)
	model := domain.Model{ID: modelID}
	runtimeStatus := ""
	listedContext := 0
	listedMetadataSource := ""
	statusContext := 0
	items, listErr := c.listModelItems(ctx)
	if listErr == nil {
		if item, ok := modelResponseItemByID(items, modelID); ok {
			model = modelFromResponseItem(item)
			listedContext = model.ContextWindow
			listedMetadataSource = model.MetadataSource
			statusContext = contextWindowFromModelStatus(item.Status.Args, item.Status.Preset)
			runtimeStatus = strings.ToLower(strings.TrimSpace(item.Status.Value))
		}
	}
	// Prefer native v1 loaded-instance metadata over advertised maxima. This is
	// a read-only endpoint and reports the context actually allocated to a
	// loaded model. Probe it opportunistically: compatible servers that do not
	// implement it are expected to reject it.
	nativeV1Context := 0
	if detected, err := c.nativeV1ModelMetadata(ctx, modelID); err == nil {
		mergeDetectedModel(&model, detected)
		if detected.MetadataSource == "native-v1-loaded-instance" && model.ContextWindow > 0 {
			return model, nil
		}
		nativeV1Context = firstPositive(detected.ContextWindow, detected.MaxContextWindow)
		model.ContextWindow = listedContext
	}

	// llama.cpp's router loads an unloaded model to serve /props. Its /models
	// response already exposes the configured context through status args, so
	// only ask /props when the model is loaded or no router status is available.
	if runtimeStatus != "unloaded" {
		props, propsErr := c.Props(ctx, modelID)
		if propsErr == nil {
			mergeDetectedModel(&model, modelFromProps(modelID, props))
			if model.ContextWindow > 0 {
				return model, nil
			}
		}
		if propsErr != nil && !isOptionalContextWindowProbeError(propsErr) {
			return domain.Model{}, propsErr
		}
	}

	if statusContext > 0 {
		model.ContextWindow = statusContext
		model.MetadataSource = listedMetadataSource
		return model, nil
	}
	if nativeV1Context > 0 {
		model.ContextWindow = nativeV1Context
		return model, nil
	}
	if listedContext > 0 {
		model.ContextWindow = listedContext
		model.MetadataSource = listedMetadataSource
		return model, nil
	}

	// Older compatible servers may expose a richer v0 model catalog. It only
	// reports a model maximum, so consult it after effective-runtime probes.
	if detected, err := c.nativeV0ModelMetadata(ctx, modelID); err == nil {
		mergeDetectedModel(&model, detected)
		if model.ContextWindow > 0 {
			return model, nil
		}
	}

	// Ollama's show operation is read-only despite using POST. Keep it last so
	// generic GET metadata endpoints win and unrelated compatible servers see
	// the fewest unnecessary requests.
	if detected, err := c.ollamaModelMetadata(ctx, modelID); err == nil {
		mergeDetectedModel(&model, detected)
		if model.ContextWindow > 0 {
			return model, nil
		}
	}
	if listErr != nil && !isOptionalMetadataProbeError(listErr) {
		return domain.Model{}, listErr
	}
	return model, nil
}

func modelFromProps(modelID string, props propsResponse) domain.Model {
	model := domain.Model{ID: modelID, CapabilitySource: "llama.cpp-props"}
	model.ContextWindow = firstPositive(props.NCtx, props.DefaultGenerationSettings.NCtx)
	if model.ContextWindow > 0 {
		model.MetadataSource = "llama.cpp-props"
	}
	if props.Modalities.Vision != nil {
		model.SupportsImages = *props.Modalities.Vision
		model.ImagesKnown = true
		model.CapabilitiesKnown = true
	}
	if props.ChatTemplateToolUse != nil {
		var template string
		model.SupportsTools = json.Unmarshal(props.ChatTemplateToolUse, &template) == nil && strings.TrimSpace(template) != ""
		model.ToolsKnown = true
		model.CapabilitiesKnown = true
	}
	return model
}

func (c *Client) enrichModelCatalog(ctx context.Context, models []domain.Model) {
	if !catalogNeedsEnrichment(models) {
		return
	}
	var v1 nativeV1ModelsResponse
	if err := c.decodeJSONAt(ctx, http.MethodGet, c.serverRootPath("/api/v1/models"), nil, &v1, "get native v1 models"); err == nil {
		for idx := range models {
			if item, ok := nativeV1ModelByID(v1.Models, models[idx].ID); ok {
				mergeDetectedModel(&models[idx], modelFromNativeV1(item, models[idx].ID))
			}
		}
		return
	}
	var v0 nativeV0ModelsResponse
	if err := c.decodeJSONAt(ctx, http.MethodGet, c.serverRootPath("/api/v0/models"), nil, &v0, "get compatible v0 models"); err != nil {
		return
	}
	for idx := range models {
		if item, ok := nativeV0ModelByID(v0.Data, models[idx].ID); ok {
			mergeDetectedModel(&models[idx], modelFromNativeV0(item, models[idx].ID))
		}
	}
}

func catalogNeedsEnrichment(models []domain.Model) bool {
	for _, model := range models {
		if model.ContextWindow <= 0 || strings.TrimSpace(model.OwnedBy) == "" || !model.CapabilitiesKnown || (model.SupportsReasoning && len(model.ReasoningEfforts) == 0) {
			return true
		}
	}
	return false
}

func (c *Client) nativeV1ModelMetadata(ctx context.Context, modelID string) (domain.Model, error) {
	var payload nativeV1ModelsResponse
	if err := c.decodeJSONAt(ctx, http.MethodGet, c.serverRootPath("/api/v1/models"), nil, &payload, "get native v1 models"); err != nil {
		return domain.Model{}, err
	}
	if item, ok := nativeV1ModelByID(payload.Models, modelID); ok {
		return modelFromNativeV1(item, modelID), nil
	}
	return domain.Model{ID: modelID}, nil
}

func nativeV1ModelByID(items []nativeV1Model, modelID string) (nativeV1Model, bool) {
	modelID = strings.TrimSpace(modelID)
	for _, item := range items {
		if strings.TrimSpace(item.Key) == modelID {
			return item, true
		}
		for _, instance := range item.LoadedInstances {
			if strings.TrimSpace(instance.ID) == modelID {
				return item, true
			}
		}
	}
	return nativeV1Model{}, false
}

func modelFromNativeV1(item nativeV1Model, modelID string) domain.Model {
	model := domain.Model{
		ID: modelID, OwnedBy: strings.TrimSpace(item.Publisher),
		MaxContextWindow: item.MaxContextLength,
		MetadataSource:   "native-v1-models", CapabilitySource: "native-v1-models",
	}
	if modelType := strings.ToLower(strings.TrimSpace(item.Type)); modelType != "" {
		model.ChatKnown = true
		model.SupportsChat = modelType == "llm" || modelType == "chat" || modelType == "vlm"
		model.CapabilitiesKnown = true
	}
	if item.Capabilities.Vision != nil {
		model.SupportsImages = *item.Capabilities.Vision
		model.ImagesKnown = true
		model.CapabilitiesKnown = true
	}
	if item.Capabilities.TrainedForToolUse != nil {
		model.SupportsTools = *item.Capabilities.TrainedForToolUse
		model.ToolsKnown = true
		model.CapabilitiesKnown = true
	}
	model.SupportsReasoning, model.ReasoningEfforts, model.DefaultReasoningEffort = reasoningMetadata(item.Capabilities.Reasoning)
	if len(item.Capabilities.Reasoning) > 0 {
		model.CapabilitiesKnown = true
	}
	for _, instance := range item.LoadedInstances {
		if strings.TrimSpace(instance.ID) == strings.TrimSpace(modelID) && instance.Config.ContextLength > 0 {
			model.ContextWindow = instance.Config.ContextLength
			model.MetadataSource = "native-v1-loaded-instance"
			return model
		}
	}
	if strings.TrimSpace(item.Key) == strings.TrimSpace(modelID) && len(item.LoadedInstances) == 1 && item.LoadedInstances[0].Config.ContextLength > 0 {
		model.ContextWindow = item.LoadedInstances[0].Config.ContextLength
		model.MetadataSource = "native-v1-loaded-instance"
	}
	return model
}

func (c *Client) nativeV0ModelMetadata(ctx context.Context, modelID string) (domain.Model, error) {
	var payload nativeV0ModelsResponse
	if err := c.decodeJSONAt(ctx, http.MethodGet, c.serverRootPath("/api/v0/models"), nil, &payload, "get compatible v0 models"); err != nil {
		return domain.Model{}, err
	}
	if item, ok := nativeV0ModelByID(payload.Data, modelID); ok {
		return modelFromNativeV0(item, modelID), nil
	}
	return domain.Model{ID: modelID}, nil
}

func nativeV0ModelByID(items []nativeV0Model, modelID string) (nativeV0Model, bool) {
	for _, item := range items {
		if strings.TrimSpace(item.ID) == strings.TrimSpace(modelID) {
			return item, true
		}
	}
	return nativeV0Model{}, false
}

func modelFromNativeV0(item nativeV0Model, modelID string) domain.Model {
	model := domain.Model{
		ID: modelID, OwnedBy: strings.TrimSpace(item.Publisher),
		ContextWindow: item.MaxContextLength, MaxContextWindow: item.MaxContextLength,
		MetadataSource: "compatible-v0-models", CapabilitySource: "compatible-v0-models",
	}
	if modelType := strings.ToLower(strings.TrimSpace(item.Type)); modelType != "" {
		model.ChatKnown = true
		model.SupportsChat = modelType == "llm" || modelType == "chat" || modelType == "vlm"
		model.CapabilitiesKnown = true
	}
	return model
}

func (c *Client) ollamaModelMetadata(ctx context.Context, modelID string) (domain.Model, error) {
	body, err := json.Marshal(map[string]string{"model": modelID})
	if err != nil {
		return domain.Model{}, err
	}
	var payload ollamaShowResponse
	if err := c.decodeJSONAt(ctx, http.MethodPost, c.serverRootPath("/api/show"), bytes.NewReader(body), &payload, "show Ollama model"); err != nil {
		return domain.Model{}, err
	}
	model := domain.Model{ID: modelID, MetadataSource: "ollama-show"}
	for _, line := range strings.Split(payload.Parameters, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[0] == "num_ctx" {
			model.ContextWindow = parsePositiveInt(fields[1])
		}
	}
	for key, raw := range payload.ModelInfo {
		if !strings.HasSuffix(strings.ToLower(strings.TrimSpace(key)), ".context_length") {
			continue
		}
		if value, ok := numericInt(raw); ok && value > model.MaxContextWindow {
			model.MaxContextWindow = value
		}
	}
	if model.ContextWindow <= 0 {
		model.ContextWindow = model.MaxContextWindow
	}
	for _, capability := range payload.Capabilities {
		switch strings.ToLower(strings.TrimSpace(capability)) {
		case "completion", "chat":
			model.SupportsChat = true
			model.ChatKnown = true
		case "embedding", "embeddings":
			if !model.SupportsChat {
				model.ChatKnown = true
			}
		case "vision":
			model.SupportsImages = true
		case "tools", "tool_use":
			model.SupportsTools = true
		case "thinking", "reasoning":
			model.SupportsReasoning = true
		}
	}
	if len(payload.Capabilities) > 0 {
		model.ImagesKnown = true
		model.ToolsKnown = true
		model.CapabilitiesKnown = true
		model.CapabilitySource = "ollama-show"
	}
	return model, nil
}

func (c *Client) decodeJSONAt(ctx context.Context, method, endpoint string, body io.Reader, dst any, operation string) error {
	probeKey := method + " " + endpoint
	if !c.endpointMayBeSupported(probeKey) {
		return errEndpointUnsupported
	}
	req, err := c.newRequestAt(ctx, method, endpoint, "", body)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("%s: %w", operation, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
		err := &APIError{Operation: operation, StatusCode: resp.StatusCode, Body: strings.TrimSpace(string(raw))}
		if endpointUnsupportedByResponse(err) {
			c.rememberEndpointSupport(probeKey, endpointUnsupported)
		}
		return err
	}
	if err := json.NewDecoder(resp.Body).Decode(dst); err != nil {
		return fmt.Errorf("decode %s: %w", operation, err)
	}
	c.rememberEndpointSupport(probeKey, endpointSupported)
	return nil
}

func (c *Client) serverRootPath(path string) string {
	parsed, err := url.Parse(c.baseURL)
	if err != nil {
		return strings.TrimRight(c.llamaURL, "/") + path
	}
	parsed.Path = path
	parsed.RawPath = ""
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String()
}

func mergeDetectedModel(dst *domain.Model, src domain.Model) {
	if strings.TrimSpace(src.OwnedBy) != "" {
		dst.OwnedBy = strings.TrimSpace(src.OwnedBy)
	}
	if src.ContextWindow > 0 {
		dst.ContextWindow = src.ContextWindow
	}
	if src.MaxContextWindow > 0 {
		dst.MaxContextWindow = src.MaxContextWindow
		if dst.ContextWindow <= 0 {
			dst.ContextWindow = src.MaxContextWindow
		}
	}
	if src.MaxOutputTokens > 0 {
		dst.MaxOutputTokens = src.MaxOutputTokens
	}
	if src.MetadataSource != "" {
		dst.MetadataSource = src.MetadataSource
	}
	if src.ChatKnown {
		dst.SupportsChat = src.SupportsChat
		dst.ChatKnown = true
	}
	if src.ImagesKnown {
		dst.SupportsImages = src.SupportsImages
		dst.ImagesKnown = true
	}
	dst.SupportsPDFs = dst.SupportsPDFs || src.SupportsPDFs
	if src.ToolsKnown {
		dst.SupportsTools = src.SupportsTools
		dst.ToolsKnown = true
	}
	dst.SupportsJSON = dst.SupportsJSON || src.SupportsJSON
	dst.SupportsReasoning = dst.SupportsReasoning || src.SupportsReasoning
	if len(src.ReasoningEfforts) > 0 {
		dst.ReasoningEfforts = append([]string(nil), src.ReasoningEfforts...)
	}
	if strings.TrimSpace(src.DefaultReasoningEffort) != "" {
		dst.DefaultReasoningEffort = src.DefaultReasoningEffort
	}
	if src.CapabilitiesKnown {
		dst.CapabilitiesKnown = true
		dst.CapabilitySource = src.CapabilitySource
	}
}

func reasoningMetadata(raw json.RawMessage) (bool, []string, string) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) || bytes.Equal(trimmed, []byte("false")) {
		return false, nil, ""
	}
	if bytes.Equal(trimmed, []byte("true")) {
		return true, nil, ""
	}
	var detail struct {
		AllowedOptions []string `json:"allowed_options"`
		Default        string   `json:"default"`
		DefaultOption  string   `json:"default_option"`
		DefaultEffort  string   `json:"default_effort"`
	}
	if json.Unmarshal(trimmed, &detail) != nil {
		return true, nil, ""
	}
	return true, normalizedStrings(detail.AllowedOptions), firstNonEmptyLower(detail.Default, detail.DefaultOption, detail.DefaultEffort)
}

func normalizedStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func numericInt(value any) (int, bool) {
	switch value := value.(type) {
	case float64:
		return int(value), value > 0
	case json.Number:
		n, err := strconv.Atoi(value.String())
		return n, err == nil && n > 0
	default:
		return 0, false
	}
}

func isOptionalMetadataProbeError(err error) bool {
	if errors.Is(err, errEndpointUnsupported) {
		return true
	}
	if isOptionalContextWindowProbeError(err) {
		return true
	}
	var apiErr *APIError
	return errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusBadRequest
}
