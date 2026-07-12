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

type lmStudioModelsResponse struct {
	Models []struct {
		Key              string `json:"key"`
		MaxContextLength int    `json:"max_context_length"`
		LoadedInstances  []struct {
			ID     string `json:"id"`
			Config struct {
				ContextLength int `json:"context_length"`
			} `json:"config"`
		} `json:"loaded_instances"`
		Capabilities struct {
			Vision            bool            `json:"vision"`
			TrainedForToolUse bool            `json:"trained_for_tool_use"`
			Reasoning         json.RawMessage `json:"reasoning"`
		} `json:"capabilities"`
	} `json:"models"`
}

type ollamaShowResponse struct {
	Parameters   string         `json:"parameters"`
	Capabilities []string       `json:"capabilities"`
	ModelInfo    map[string]any `json:"model_info"`
}

// DetectModelMetadata discovers operational metadata using compatible and
// backend-native read-only APIs. A loaded runtime context wins over a model's
// advertised maximum because it is the usable limit for request budgeting.
func (c *Client) DetectModelMetadata(ctx context.Context, modelID string) (domain.Model, error) {
	modelID = strings.TrimSpace(modelID)
	model := domain.Model{ID: modelID}

	props, propsErr := c.Props(ctx, modelID)
	if propsErr == nil && props.DefaultGenerationSettings.NCtx > 0 {
		model.ContextWindow = props.DefaultGenerationSettings.NCtx
		model.MetadataSource = "llama.cpp-props"
		return model, nil
	}
	if propsErr != nil && !isOptionalContextWindowProbeError(propsErr) {
		return domain.Model{}, propsErr
	}

	models, listErr := c.ListModels(ctx)
	if listErr == nil {
		for _, listed := range models {
			if strings.TrimSpace(listed.ID) == modelID {
				model = listed
				break
			}
		}
	}
	if model.ContextWindow > 0 {
		return model, nil
	}

	if looksLikeLMStudio(c.backend) {
		if detected, err := c.lmStudioModelMetadata(ctx, modelID); err == nil {
			mergeDetectedModel(&model, detected)
			if model.ContextWindow > 0 {
				return model, nil
			}
		} else if !isOptionalMetadataProbeError(err) {
			return domain.Model{}, err
		}
	}

	if looksLikeOllama(c.provider, c.baseURL) {
		if detected, err := c.ollamaModelMetadata(ctx, modelID); err == nil {
			mergeDetectedModel(&model, detected)
			if model.ContextWindow > 0 {
				return model, nil
			}
		} else if !isOptionalMetadataProbeError(err) {
			return domain.Model{}, err
		}
	}
	if listErr != nil && !isOptionalMetadataProbeError(listErr) {
		return domain.Model{}, listErr
	}
	return model, nil
}

func (c *Client) lmStudioModelMetadata(ctx context.Context, modelID string) (domain.Model, error) {
	var payload lmStudioModelsResponse
	if err := c.decodeJSONAt(ctx, http.MethodGet, c.serverRootPath("/api/v1/models"), nil, &payload, "get LM Studio models"); err != nil {
		return domain.Model{}, err
	}
	for _, item := range payload.Models {
		matched := strings.TrimSpace(item.Key) == modelID
		for _, instance := range item.LoadedInstances {
			if strings.TrimSpace(instance.ID) == modelID {
				matched = true
				if instance.Config.ContextLength > 0 {
					return domain.Model{ID: modelID, ContextWindow: instance.Config.ContextLength, MaxContextWindow: item.MaxContextLength, MetadataSource: "lmstudio-loaded-instance", SupportsImages: item.Capabilities.Vision, ImagesKnown: true, SupportsTools: item.Capabilities.TrainedForToolUse, ToolsKnown: true, SupportsReasoning: len(item.Capabilities.Reasoning) > 0 && string(item.Capabilities.Reasoning) != "null", CapabilitiesKnown: true, CapabilitySource: "lmstudio-models"}, nil
				}
			}
		}
		if matched {
			return domain.Model{ID: modelID, ContextWindow: item.MaxContextLength, MaxContextWindow: item.MaxContextLength, MetadataSource: "lmstudio-models", SupportsImages: item.Capabilities.Vision, ImagesKnown: true, SupportsTools: item.Capabilities.TrainedForToolUse, ToolsKnown: true, SupportsReasoning: len(item.Capabilities.Reasoning) > 0 && string(item.Capabilities.Reasoning) != "null", CapabilitiesKnown: true, CapabilitySource: "lmstudio-models"}, nil
		}
	}
	return domain.Model{ID: modelID}, nil
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
		case "vision":
			model.SupportsImages = true
		case "tools", "tool_use":
			model.SupportsTools = true
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
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
		return &APIError{Operation: operation, StatusCode: resp.StatusCode, Body: strings.TrimSpace(string(raw))}
	}
	if err := json.NewDecoder(resp.Body).Decode(dst); err != nil {
		return fmt.Errorf("decode %s: %w", operation, err)
	}
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
	if src.ContextWindow > 0 {
		dst.ContextWindow = src.ContextWindow
	}
	if src.MaxContextWindow > 0 {
		dst.MaxContextWindow = src.MaxContextWindow
	}
	if src.MaxOutputTokens > 0 {
		dst.MaxOutputTokens = src.MaxOutputTokens
	}
	if src.MetadataSource != "" {
		dst.MetadataSource = src.MetadataSource
	}
	dst.SupportsImages = dst.SupportsImages || src.SupportsImages
	dst.ImagesKnown = dst.ImagesKnown || src.ImagesKnown
	dst.SupportsPDFs = dst.SupportsPDFs || src.SupportsPDFs
	dst.SupportsTools = dst.SupportsTools || src.SupportsTools
	dst.ToolsKnown = dst.ToolsKnown || src.ToolsKnown
	dst.SupportsJSON = dst.SupportsJSON || src.SupportsJSON
	dst.SupportsReasoning = dst.SupportsReasoning || src.SupportsReasoning
	if src.CapabilitiesKnown {
		dst.CapabilitiesKnown = true
		dst.CapabilitySource = src.CapabilitySource
	}
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

func looksLikeOllama(providerID, baseURL string) bool {
	haystack := strings.ToLower(providerID + " " + baseURL)
	return strings.Contains(haystack, "ollama") || strings.Contains(haystack, ":11434")
}

func looksLikeLMStudio(hint string) bool {
	hint = strings.ToLower(hint)
	return strings.Contains(hint, "lm studio") || strings.Contains(hint, "lmstudio") || strings.Contains(hint, ":1234")
}

func isOptionalMetadataProbeError(err error) bool {
	if isOptionalContextWindowProbeError(err) {
		return true
	}
	var apiErr *APIError
	return errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusBadRequest
}
