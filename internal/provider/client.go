package provider

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/lkarlslund/koder/internal/config"
	"github.com/lkarlslund/koder/internal/debugsrv"
	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/id"
)

type APIError struct {
	Operation  string
	StatusCode int
	Body       string
	RetryAfter time.Duration
}

func (e *APIError) Error() string {
	if e == nil {
		return ""
	}
	body := strings.TrimSpace(e.Body)
	if body == "" {
		return fmt.Sprintf("%s status %d", e.Operation, e.StatusCode)
	}
	return fmt.Sprintf("%s status %d: %s", e.Operation, e.StatusCode, body)
}

type SpeechResponse struct {
	ContentType string
	Audio       []byte
}

type SpeechRequest struct {
	Model          string
	Input          string
	Voice          string
	ResponseFormat string
	Speed          float64
}

func parseRetryAfter(value string, now time.Time) time.Duration {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return 0
	}
	if secs, err := strconv.Atoi(trimmed); err == nil {
		if secs <= 0 {
			return 0
		}
		return time.Duration(secs) * time.Second
	}
	when, err := http.ParseTime(trimmed)
	if err != nil {
		return 0
	}
	if now.IsZero() {
		now = time.Now()
	}
	if when.Before(now) {
		return 0
	}
	return when.Sub(now)
}

type Message struct {
	Role         Role          `json:"role"`
	Content      string        `json:"content,omitempty"`
	ContentParts []ContentPart `json:"-"`
	ToolCallID   string        `json:"tool_call_id,omitempty"`
	ToolCalls    []ToolCall    `json:"tool_calls,omitempty"`
}

type ContentPart struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	MIMEType string `json:"-"`
	Data     []byte `json:"-"`
}

func TextPart(text string) ContentPart {
	return ContentPart{Type: "text", Text: text}
}

func ImagePart(mimeType string, data []byte) ContentPart {
	return ContentPart{Type: "image_url", MIMEType: mimeType, Data: data}
}

var probePNG = []byte{
	0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a,
	0x00, 0x00, 0x00, 0x0d, 0x49, 0x48, 0x44, 0x52,
	0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
	0x08, 0x06, 0x00, 0x00, 0x00, 0x1f, 0x15, 0xc4,
	0x89, 0x00, 0x00, 0x00, 0x0d, 0x49, 0x44, 0x41,
	0x54, 0x78, 0x9c, 0x63, 0xf8, 0xcf, 0xc0, 0xf0,
	0x1f, 0x00, 0x05, 0x00, 0x01, 0xff, 0x89, 0x99,
	0x3d, 0x1d, 0x00, 0x00, 0x00, 0x00, 0x49, 0x45,
	0x4e, 0x44, 0xae, 0x42, 0x60, 0x82,
}

func (m Message) MarshalJSON() ([]byte, error) {
	type wireMessage struct {
		Role       string     `json:"role"`
		Content    any        `json:"content,omitempty"`
		ToolCallID string     `json:"tool_call_id,omitempty"`
		ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	}
	var content any
	trimmed := strings.TrimSpace(sanitizePromptText(m.Content))
	if trimmed != "" {
		content = trimmed
	}
	if len(m.ContentParts) > 0 {
		items := make([]any, 0, len(m.ContentParts))
		for _, part := range m.ContentParts {
			switch part.Type {
			case "text":
				items = append(items, map[string]any{
					"type": "text",
					"text": sanitizePromptText(part.Text),
				})
			case "image_url":
				items = append(items, map[string]any{
					"type": "image_url",
					"image_url": map[string]any{
						"url": imageDataURL(part.MIMEType, part.Data),
					},
				})
			default:
				return nil, fmt.Errorf("unsupported content part type %q", part.Type)
			}
		}
		content = items
	}
	return json.Marshal(wireMessage{
		Role:       providerRole(m.Role),
		Content:    content,
		ToolCallID: m.ToolCallID,
		ToolCalls:  sanitizeToolCalls(m.ToolCalls),
	})
}

func sanitizeToolCalls(calls []ToolCall) []ToolCall {
	if len(calls) == 0 {
		return nil
	}
	out := make([]ToolCall, len(calls))
	copy(out, calls)
	for i := range out {
		out[i].Function.Arguments = sanitizePromptText(out[i].Function.Arguments)
	}
	return out
}

func sanitizePromptText(text string) string {
	if text == "" {
		return ""
	}
	var b strings.Builder
	changed := false
	for _, r := range text {
		switch {
		case r == '\n' || r == '\r' || r == '\t':
			b.WriteRune(r)
		case r < 0x20 || r == 0x7f:
			changed = true
			fmt.Fprintf(&b, "\\x%02x", r)
		default:
			b.WriteRune(r)
		}
	}
	if !changed {
		return text
	}
	return b.String()
}

func providerRole(role Role) string {
	switch role {
	case RoleSystem:
		return "system"
	case RoleUser:
		return "user"
	case RoleAssistant:
		return "assistant"
	case RoleTool:
		return "tool"
	default:
		return strings.ToLower(role.String())
	}
}

type ToolDefinition struct {
	Type     string             `json:"type"`
	Function FunctionDefinition `json:"function"`
}

type FunctionDefinition struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

type ToolCall struct {
	ID       string       `json:"id,omitempty"`
	Index    *int         `json:"index,omitempty"`
	Type     string       `json:"type,omitempty"`
	Function FunctionCall `json:"function"`
}

type FunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type ToolCallError struct {
	ToolCall ToolCall
	Message  string
}

type ChatRequest struct {
	SessionID          id.ID            `json:"-"`
	ChatID             id.ID            `json:"-"`
	Model              string           `json:"model"`
	Messages           []Message        `json:"messages"`
	Tools              []ToolDefinition `json:"tools,omitempty"`
	ToolChoice         string           `json:"tool_choice,omitempty"`
	Stream             bool             `json:"stream"`
	ExtraBody          map[string]any   `json:"-"`
	ToolArgumentLimits map[string]int   `json:"-"`
}

type requestDebugContextKey struct{}

type requestDebugContext struct {
	SessionID id.ID
	ChatID    id.ID
}

func (r ChatRequest) MarshalJSON() ([]byte, error) {
	body := map[string]any{
		"model":    r.Model,
		"messages": r.Messages,
		"stream":   r.Stream,
	}
	if r.Stream {
		body["stream_options"] = map[string]any{"include_usage": true}
	}
	if len(r.Tools) > 0 {
		body["tools"] = r.Tools
	}
	if strings.TrimSpace(r.ToolChoice) != "" {
		body["tool_choice"] = r.ToolChoice
	}
	for key, value := range r.ExtraBody {
		if strings.TrimSpace(key) == "" {
			continue
		}
		body[key] = value
	}
	return json.Marshal(body)
}

type modelResponseItem struct {
	ID                  string   `json:"id"`
	OwnedBy             string   `json:"owned_by"`
	ContextLength       int      `json:"context_length"`
	ContextWindow       int      `json:"context_window"`
	MaxContextLength    int      `json:"max_context_length"`
	MaxModelLen         int      `json:"max_model_len"`
	MaxCompletionTokens int      `json:"max_completion_tokens"`
	Capabilities        []string `json:"capabilities"`
	SupportedParameters []string `json:"supported_parameters"`
	Architecture        struct {
		InputModalities  []string `json:"input_modalities"`
		OutputModalities []string `json:"output_modalities"`
	} `json:"architecture"`
	TopProvider struct {
		ContextLength       int `json:"context_length"`
		MaxCompletionTokens int `json:"max_completion_tokens"`
	} `json:"top_provider"`
	Status struct {
		Args   []string `json:"args"`
		Preset string   `json:"preset"`
	} `json:"status"`
}

type modelsResponse struct {
	Data []modelResponseItem `json:"data"`
}

type propsResponse struct {
	MaxInstances              int `json:"max_instances"`
	DefaultGenerationSettings struct {
		NCtx int `json:"n_ctx"`
	} `json:"default_generation_settings"`
}

type slotResponse struct {
	ID int `json:"id"`
}

type chatChunk struct {
	Choices []struct {
		Delta struct {
			Content          string        `json:"content"`
			Reasoning        string        `json:"reasoning"`
			ReasoningContent string        `json:"reasoning_content"`
			ToolCalls        []rawToolCall `json:"tool_calls"`
		} `json:"delta"`
		Message struct {
			Content          string        `json:"content"`
			Reasoning        string        `json:"reasoning"`
			ReasoningContent string        `json:"reasoning_content"`
			ToolCalls        []rawToolCall `json:"tool_calls"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens        int `json:"prompt_tokens"`
		CompletionTokens    int `json:"completion_tokens"`
		PromptTokensDetails struct {
			CachedTokens int `json:"cached_tokens"`
		} `json:"prompt_tokens_details"`
		InputTokensDetails struct {
			CachedTokens int `json:"cached_tokens"`
		} `json:"input_tokens_details"`
		TotalTokens int `json:"total_tokens"`
	} `json:"usage"`
	PromptProgress struct {
		Total     int   `json:"total"`
		Cache     int   `json:"cache"`
		Processed int   `json:"processed"`
		TimeMS    int64 `json:"time_ms"`
	} `json:"prompt_progress"`
}

type rawToolCall struct {
	ID       string `json:"id"`
	Index    *int   `json:"index,omitempty"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type Client struct {
	http     *http.Client
	baseURL  string
	llamaURL string
	apiKey   string
	headers  map[string]string
	provider string
	backend  string
	recorder *debugsrv.Recorder
}

type ChatResponse struct {
	Text               string
	Reasoning          string
	Usage              domain.Usage
	ToolCalls          []ToolCall
	ToolCallErrors     []ToolCallError
	PromptProgressSeen bool
}

func New(id string, cfg config.Provider, recorder *debugsrv.Recorder) (*Client, error) {
	if cfg.BaseURL == "" {
		return nil, errors.New("provider base url is empty")
	}
	baseURL := strings.TrimRight(cfg.BaseURL, "/")
	if _, err := url.Parse(baseURL); err != nil {
		return nil, fmt.Errorf("parse provider base url: %w", err)
	}
	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = 10 * time.Minute
	}
	transport := defaultTransportWithHeaderTimeout(timeout)
	if recorder != nil {
		transport = &tracingTransport{
			base:       transport,
			recorder:   recorder,
			providerID: id,
		}
	}
	return &Client{
		http: &http.Client{
			Timeout:   0,
			Transport: transport,
		},
		baseURL:  baseURL,
		llamaURL: llamaServerBaseURL(baseURL),
		apiKey:   cfg.APIKey,
		headers:  cfg.Headers,
		provider: id,
		backend:  strings.ToLower(strings.Join([]string{id, cfg.TemplateID, cfg.Name, cfg.BaseURL}, " ")),
		recorder: recorder,
	}, nil
}

func defaultTransportWithHeaderTimeout(timeout time.Duration) http.RoundTripper {
	base, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return http.DefaultTransport
	}
	clone := base.Clone()
	clone.ResponseHeaderTimeout = timeout
	return clone
}

func (c *Client) ListModels(ctx context.Context) ([]domain.Model, error) {
	req, err := c.newRequest(ctx, http.MethodGet, c.apiPath("/models"), nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("list models: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
		return nil, &APIError{
			Operation:  "list models",
			StatusCode: resp.StatusCode,
			Body:       strings.TrimSpace(string(body)),
			RetryAfter: parseRetryAfter(resp.Header.Get("Retry-After"), time.Now()),
		}
	}

	var payload modelsResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode model list: %w", err)
	}

	models := make([]domain.Model, 0, len(payload.Data))
	for _, item := range payload.Data {
		model := domain.Model{
			ID:               item.ID,
			OwnedBy:          item.OwnedBy,
			ContextWindow:    listedModelContextWindow(item, true),
			MaxContextWindow: listedModelContextWindow(item, false),
			MaxOutputTokens:  firstPositive(item.TopProvider.MaxCompletionTokens, item.MaxCompletionTokens),
			MetadataSource:   "openai-models",
		}
		applyListedCapabilities(&model, item)
		models = append(models, model)
	}
	return models, nil
}

func (c *Client) DetectModelContextWindow(ctx context.Context, modelID string) (int, error) {
	req, err := c.newRequest(ctx, http.MethodGet, c.apiPath("/models"), nil)
	if err != nil {
		return 0, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, fmt.Errorf("list models: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
		return 0, &APIError{
			Operation:  "list models",
			StatusCode: resp.StatusCode,
			Body:       strings.TrimSpace(string(body)),
			RetryAfter: parseRetryAfter(resp.Header.Get("Retry-After"), time.Now()),
		}
	}
	var payload modelsResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return 0, fmt.Errorf("decode model list: %w", err)
	}
	modelID = strings.TrimSpace(modelID)
	for _, item := range payload.Data {
		if strings.TrimSpace(item.ID) != modelID {
			continue
		}
		if n := listedModelContextWindow(item, true); n > 0 {
			return n, nil
		}
	}
	return 0, nil
}

func listedModelContextWindow(item modelResponseItem, effective bool) int {
	if effective {
		if n := contextWindowFromModelStatus(item.Status.Args, item.Status.Preset); n > 0 {
			return n
		}
	}
	return firstPositive(item.TopProvider.ContextLength, item.ContextWindow, item.ContextLength, item.MaxContextLength, item.MaxModelLen)
}

func applyListedCapabilities(model *domain.Model, item modelResponseItem) {
	if model == nil {
		return
	}
	for _, modality := range item.Architecture.InputModalities {
		model.ImagesKnown = true
		switch strings.ToLower(strings.TrimSpace(modality)) {
		case "image":
			model.SupportsImages = true
		case "file", "pdf":
			model.SupportsPDFs = true
		}
	}
	for _, capability := range append(slices.Clone(item.Capabilities), item.SupportedParameters...) {
		switch strings.ToLower(strings.TrimSpace(capability)) {
		case "vision", "image", "images":
			model.SupportsImages = true
		case "tools", "tool_use", "tool_choice", "function_calling":
			model.SupportsTools = true
		case "structured_outputs", "response_format", "json_schema":
			model.SupportsJSON = true
		case "reasoning", "include_reasoning":
			model.SupportsReasoning = true
		}
	}
	if len(item.Capabilities) > 0 || len(item.SupportedParameters) > 0 {
		model.ToolsKnown = true
	}
	if len(item.Capabilities) > 0 || len(item.SupportedParameters) > 0 || len(item.Architecture.InputModalities) > 0 {
		model.CapabilitiesKnown = true
		model.CapabilitySource = "openai-models"
	}
}

func firstPositive(values ...int) int {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}

func contextWindowFromModelStatus(args []string, preset string) int {
	for idx, arg := range args {
		if arg == "--ctx-size" || arg == "-c" {
			if idx+1 < len(args) {
				if n := parsePositiveInt(args[idx+1]); n > 0 {
					return n
				}
			}
			continue
		}
		if value, ok := strings.CutPrefix(arg, "--ctx-size="); ok {
			if n := parsePositiveInt(value); n > 0 {
				return n
			}
		}
	}
	for _, line := range strings.Split(preset, "\n") {
		key, value, ok := strings.Cut(line, "=")
		if !ok || strings.TrimSpace(key) != "ctx-size" {
			continue
		}
		if n := parsePositiveInt(value); n > 0 {
			return n
		}
	}
	return 0
}

func parsePositiveInt(value string) int {
	n, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || n <= 0 {
		return 0
	}
	return n
}

func (c *Client) Props(ctx context.Context, modelID string) (propsResponse, error) {
	path := "/props"
	if trimmed := strings.TrimSpace(modelID); trimmed != "" {
		path += "?model=" + url.QueryEscape(trimmed)
	}
	return c.propsRequest(ctx, c.llamaURL, path)
}

func (c *Client) Slots(ctx context.Context, modelID string) ([]slotResponse, error) {
	path := "/slots"
	if trimmed := strings.TrimSpace(modelID); trimmed != "" {
		path += "?model=" + url.QueryEscape(trimmed)
	}
	req, err := c.newRequestAt(ctx, http.MethodGet, c.llamaURL, path, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("get slots: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
		return nil, &APIError{
			Operation:  "slots",
			StatusCode: resp.StatusCode,
			Body:       strings.TrimSpace(string(body)),
			RetryAfter: parseRetryAfter(resp.Header.Get("Retry-After"), time.Now()),
		}
	}
	var payload []slotResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode slots: %w", err)
	}
	return payload, nil
}

func DetectLlamaSlots(ctx context.Context, providerID string, cfg config.Provider, modelID string, recorder *debugsrv.Recorder) (int, error) {
	if !SupportsContextWindowDetection(cfg) {
		return 0, nil
	}
	client, err := New(providerID, cfg, recorder)
	if err != nil {
		return 0, err
	}
	slots, err := client.Slots(ctx, modelID)
	if err == nil {
		if len(slots) > 0 {
			return len(slots), nil
		}
	} else if !isOptionalLlamaProbeError(err) {
		return 0, err
	}
	props, err := client.Props(ctx, modelID)
	if err == nil {
		if props.MaxInstances > 0 {
			return props.MaxInstances, nil
		}
	} else if !isOptionalLlamaProbeError(err) {
		return 0, err
	}
	return 0, nil
}

func DetectContextWindow(ctx context.Context, providerID string, cfg config.Provider, modelID string, recorder *debugsrv.Recorder) (int, error) {
	if !SupportsContextWindowDetection(cfg) {
		return 32768, nil
	}
	client, err := New(providerID, cfg, recorder)
	if err != nil {
		return 0, err
	}
	model, err := client.DetectModelMetadata(ctx, modelID)
	if err != nil {
		return 0, err
	}
	if model.ContextWindow > 0 {
		return model.ContextWindow, nil
	}
	return 32768, nil
}

func SupportsContextWindowDetection(cfg config.Provider) bool {
	return strings.TrimSpace(cfg.Kind) == ProviderKindCompatible &&
		strings.TrimSpace(cfg.BaseURL) != ""
}

func llamaServerBaseURL(baseURL string) string {
	trimmed := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	return strings.TrimSuffix(trimmed, "/v1")
}

func isOptionalContextWindowProbeError(err error) bool {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return apiErr.StatusCode == http.StatusNotFound || apiErr.StatusCode == http.StatusMethodNotAllowed
	}
	return false
}

func isOptionalLlamaProbeError(err error) bool {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		switch apiErr.StatusCode {
		case http.StatusBadRequest, http.StatusNotFound, http.StatusMethodNotAllowed:
			return true
		default:
			return false
		}
	}
	return false
}

func (c *Client) propsRequest(ctx context.Context, baseURL, path string) (propsResponse, error) {
	req, err := c.newRequestAt(ctx, http.MethodGet, baseURL, path, nil)
	if err != nil {
		return propsResponse{}, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return propsResponse{}, fmt.Errorf("get props: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
		return propsResponse{}, &APIError{
			Operation:  "props",
			StatusCode: resp.StatusCode,
			Body:       strings.TrimSpace(string(body)),
			RetryAfter: parseRetryAfter(resp.Header.Get("Retry-After"), time.Now()),
		}
	}
	var payload propsResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return propsResponse{}, fmt.Errorf("decode props: %w", err)
	}
	return payload, nil
}

func (c *Client) Health(ctx context.Context) error {
	healthURL := strings.TrimSuffix(c.baseURL, "/v1")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, healthURL+"/health", nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("health check: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("health status %d", resp.StatusCode)
	}
	return nil
}

func (c *Client) CompleteChat(ctx context.Context, input ChatRequest) (ChatResponse, error) {
	ctx = context.WithValue(ctx, requestDebugContextKey{}, requestDebugContext{SessionID: input.SessionID, ChatID: input.ChatID})
	var body bytes.Buffer
	if err := json.NewEncoder(&body).Encode(input); err != nil {
		return ChatResponse{}, fmt.Errorf("encode chat request: %w", err)
	}
	requestBody := body.String()
	ctx, activeRequestID, finishActive := c.startActiveHTTP(ctx, input, http.MethodPost, c.apiPath("/chat/completions"), requestBody, nil)
	defer finishActive()
	req, err := c.newRequest(ctx, http.MethodPost, c.apiPath("/chat/completions"), &body)
	if err != nil {
		return ChatResponse{}, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return ChatResponse{}, fmt.Errorf("complete chat: %w", err)
	}
	defer resp.Body.Close()
	c.updateActiveHTTP(activeRequestID, debugsrv.HTTPTrace{
		Status:       resp.StatusCode,
		ResponseHdrs: redactHeaders(resp.Header),
	})
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
		return ChatResponse{}, &APIError{
			Operation:  "chat",
			StatusCode: resp.StatusCode,
			Body:       strings.TrimSpace(string(body)),
			RetryAfter: parseRetryAfter(resp.Header.Get("Retry-After"), time.Now()),
		}
	}

	var payload chatChunk
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		c.recordBodyFailure(http.MethodPost, req.URL.Path, requestBody, resp, "decode_chat_response", err, nil, "")
		return ChatResponse{}, fmt.Errorf("decode chat response: %w", err)
	}
	if data, err := json.Marshal(payload); err == nil {
		c.updateActiveHTTP(activeRequestID, debugsrv.HTTPTrace{ResponseBody: redactBody(string(data))})
	}
	if len(payload.Choices) == 0 {
		return ChatResponse{}, nil
	}
	choice := payload.Choices[0]
	usage := domain.Usage{
		PromptTokens:     payload.Usage.PromptTokens,
		CompletionTokens: payload.Usage.CompletionTokens,
		CachedTokens:     cachedTokensFromUsage(payload.Usage.PromptTokensDetails.CachedTokens, payload.Usage.InputTokensDetails.CachedTokens),
		TotalTokens:      payload.Usage.TotalTokens,
	}.Normalized()
	return ChatResponse{
		Text:      choice.Message.Content,
		Reasoning: firstPresentString(choice.Message.ReasoningContent, choice.Message.Reasoning),
		Usage:     usage,
		ToolCalls: convertToolCalls(choice.Message.ToolCalls),
	}, nil
}

func (c *Client) ProbeImageSupport(ctx context.Context, modelID string) (bool, error) {
	_, err := c.CompleteChat(ctx, ChatRequest{
		Model: modelID,
		Messages: []Message{{
			Role: RoleUser,
			ContentParts: []ContentPart{
				ImagePart("image/png", probePNG),
				TextPart("Reply with OK."),
			},
		}},
		Stream: false,
		ExtraBody: map[string]any{
			"max_tokens": 1,
		},
	})
	if err == nil {
		return true, nil
	}
	if isImageSupportProbeUnsupported(err) {
		return false, nil
	}
	return false, err
}

func (c *Client) ProbeChatSupport(ctx context.Context, modelID string) (bool, error) {
	_, err := c.CompleteChat(ctx, ChatRequest{
		Model: modelID,
		Messages: []Message{{
			Role:    RoleUser,
			Content: "Reply with OK.",
		}},
		Stream: false,
		ExtraBody: map[string]any{
			"max_tokens": 1,
		},
	})
	if err == nil {
		return true, nil
	}
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		switch apiErr.StatusCode {
		case http.StatusNotFound, http.StatusMethodNotAllowed:
			return false, nil
		}
	}
	return true, err
}

func (c *Client) ProbeTTSSupport(ctx context.Context, modelID string) (bool, error) {
	payload := map[string]any{
		"model": modelID,
		"input": "OK",
		"voice": "alloy",
	}
	var body bytes.Buffer
	if err := json.NewEncoder(&body).Encode(payload); err != nil {
		return false, fmt.Errorf("encode tts probe: %w", err)
	}
	req, err := c.newRequest(ctx, http.MethodPost, c.apiPath("/audio/speech"), &body)
	if err != nil {
		return false, err
	}
	req.Header.Set("Accept", "audio/*, application/octet-stream")
	resp, err := c.http.Do(req)
	if err != nil {
		return false, fmt.Errorf("tts probe: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1024))
		contentType := strings.ToLower(resp.Header.Get("Content-Type"))
		return strings.Contains(contentType, "audio/") || strings.Contains(contentType, "application/octet-stream"), nil
	}
	bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
	switch resp.StatusCode {
	case http.StatusNotFound, http.StatusMethodNotAllowed, http.StatusBadRequest, http.StatusUnsupportedMediaType, http.StatusUnprocessableEntity:
		return false, nil
	}
	return false, &APIError{
		Operation:  "tts probe",
		StatusCode: resp.StatusCode,
		Body:       strings.TrimSpace(string(bodyBytes)),
		RetryAfter: parseRetryAfter(resp.Header.Get("Retry-After"), time.Now()),
	}
}

func (c *Client) CreateSpeech(ctx context.Context, input SpeechRequest) (SpeechResponse, error) {
	input.Model = strings.TrimSpace(input.Model)
	input.Input = strings.TrimSpace(input.Input)
	input.Voice = strings.TrimSpace(input.Voice)
	input.ResponseFormat = strings.TrimSpace(input.ResponseFormat)
	if input.Model == "" {
		return SpeechResponse{}, fmt.Errorf("tts model id is required")
	}
	if input.Input == "" {
		return SpeechResponse{}, fmt.Errorf("tts input is required")
	}
	if input.Voice == "" {
		input.Voice = "alloy"
	}
	payload := map[string]any{
		"model": input.Model,
		"input": input.Input,
		"voice": input.Voice,
	}
	if input.ResponseFormat != "" {
		payload["response_format"] = input.ResponseFormat
	}
	if input.Speed > 0 {
		payload["speed"] = input.Speed
	}
	var body bytes.Buffer
	if err := json.NewEncoder(&body).Encode(payload); err != nil {
		return SpeechResponse{}, fmt.Errorf("encode tts request: %w", err)
	}
	req, err := c.newRequest(ctx, http.MethodPost, c.apiPath("/audio/speech"), &body)
	if err != nil {
		return SpeechResponse{}, err
	}
	req.Header.Set("Accept", "audio/*, application/octet-stream")
	resp, err := c.http.Do(req)
	if err != nil {
		return SpeechResponse{}, fmt.Errorf("tts request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
		return SpeechResponse{}, &APIError{
			Operation:  "tts",
			StatusCode: resp.StatusCode,
			Body:       strings.TrimSpace(string(bodyBytes)),
			RetryAfter: parseRetryAfter(resp.Header.Get("Retry-After"), time.Now()),
		}
	}
	audio, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return SpeechResponse{}, fmt.Errorf("read tts response: %w", err)
	}
	if len(audio) == 0 {
		return SpeechResponse{}, fmt.Errorf("tts response was empty")
	}
	contentType := strings.TrimSpace(resp.Header.Get("Content-Type"))
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	return SpeechResponse{ContentType: contentType, Audio: audio}, nil
}

func (c *Client) StreamChat(ctx context.Context, input ChatRequest) (<-chan domain.Event, error) {
	events := make(chan domain.Event)
	go func() {
		defer close(events)
		_, err := c.StreamChatResponse(ctx, input, func(evt domain.Event) {
			events <- evt
		})
		if err != nil {
			events <- domain.Event{Kind: domain.EventKindError, Err: err}
		}
	}()
	return events, nil
}

func isImageSupportProbeUnsupported(err error) bool {
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		return false
	}
	switch apiErr.StatusCode {
	case http.StatusBadRequest, http.StatusUnsupportedMediaType, http.StatusUnprocessableEntity:
	default:
		return false
	}
	body := strings.ToLower(apiErr.Body)
	needles := []string{
		"image",
		"vision",
		"multimodal",
		"content part",
		"input modality",
		"unsupported",
	}
	matches := 0
	for _, needle := range needles {
		if strings.Contains(body, needle) {
			matches++
		}
	}
	return matches >= 2
}

func (c *Client) StreamChatResponse(ctx context.Context, input ChatRequest, onEvent func(domain.Event)) (ChatResponse, error) {
	input.Stream = true
	ctx = context.WithValue(ctx, requestDebugContextKey{}, requestDebugContext{SessionID: input.SessionID, ChatID: input.ChatID})
	start := time.Now()
	var body bytes.Buffer
	if err := json.NewEncoder(&body).Encode(input); err != nil {
		return ChatResponse{}, fmt.Errorf("encode chat request: %w", err)
	}
	requestBody := body.String()
	ctx, activeRequestID, finishActive := c.startActiveHTTP(ctx, input, http.MethodPost, c.apiPath("/chat/completions"), requestBody, nil)
	defer finishActive()
	req, err := c.newRequest(ctx, http.MethodPost, c.apiPath("/chat/completions"), &body)
	if err != nil {
		return ChatResponse{}, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return ChatResponse{}, fmt.Errorf("stream chat: %w", err)
	}
	defer resp.Body.Close()
	c.updateActiveHTTP(activeRequestID, debugsrv.HTTPTrace{
		Status:       resp.StatusCode,
		ResponseHdrs: redactHeaders(resp.Header),
	})
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
		return ChatResponse{}, &APIError{
			Operation:  "chat",
			StatusCode: resp.StatusCode,
			Body:       strings.TrimSpace(string(body)),
			RetryAfter: parseRetryAfter(resp.Header.Get("Retry-After"), time.Now()),
		}
	}
	if !strings.Contains(strings.ToLower(resp.Header.Get("Content-Type")), "text/event-stream") {
		var payload chatChunk
		if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
			c.recordBodyFailure(http.MethodPost, req.URL.Path, requestBody, resp, "decode_chat_response", err, nil, "")
			return ChatResponse{}, fmt.Errorf("decode chat response: %w", err)
		}
		result := streamedChatResponse{}
		result.Apply(payload)
		if onEvent != nil {
			if text := result.text.String(); strings.TrimSpace(text) != "" {
				onEvent(domain.Event{Kind: domain.EventKindMessageDelta, Text: text})
			}
			if reasoning := result.reasoning.String(); strings.TrimSpace(reasoning) != "" {
				onEvent(domain.Event{Kind: domain.EventKindReasoning, Text: reasoning})
			}
			if result.usage.HasAnyTokens() {
				onEvent(domain.Event{Kind: domain.EventKindUsage, Usage: result.usage})
			}
			onEvent(domain.Event{Kind: domain.EventKindMessageDone})
		}
		return result.Response(), nil
	}

	var aggregated streamedChatResponse
	reader := bufio.NewReader(resp.Body)
	chunkCount := 0
	lastPayload := ""
	capture := newDebugStreamCapture(8192)
	updateActiveStream := func(meta map[string]string) {
		c.updateActiveHTTP(activeRequestID, debugsrv.HTTPTrace{
			Status:       resp.StatusCode,
			ResponseHdrs: redactHeaders(resp.Header),
			ResponseBody: capture.String(),
			Meta:         meta,
		})
	}
	recordTrace := func(errText string, meta map[string]string) {
		if c == nil || c.recorder == nil {
			return
		}
		var requestHeaders map[string]string
		if resp.Request != nil {
			requestHeaders = redactHeaders(resp.Request.Header)
		}
		trace := debugsrv.HTTPTrace{
			ProviderID:   c.provider,
			SessionID:    input.SessionID,
			ChatID:       input.ChatID,
			Method:       http.MethodPost,
			Path:         req.URL.Path,
			Status:       resp.StatusCode,
			DurationMS:   time.Since(start).Milliseconds(),
			RequestBody:  redactBody(requestBody),
			RequestHdrs:  requestHeaders,
			ResponseHdrs: redactHeaders(resp.Header),
			ResponseBody: capture.String(),
			Meta:         meta,
			Error:        errText,
		}
		c.updateActiveHTTP(activeRequestID, trace)
		c.recorder.RecordHTTP(trace)
	}
	for {
		if err := ctx.Err(); errors.Is(err, context.Canceled) {
			recordTrace(err.Error(), map[string]string{
				"phase":       "stream_canceled",
				"chunk_count": strconv.Itoa(chunkCount),
			})
			return aggregated.Response(), err
		}
		line, err := reader.ReadString('\n')
		if ctxErr := ctx.Err(); errors.Is(ctxErr, context.Canceled) {
			recordTrace(ctxErr.Error(), map[string]string{
				"phase":       "stream_canceled",
				"chunk_count": strconv.Itoa(chunkCount),
			})
			return aggregated.Response(), ctxErr
		}
		if err != nil && !errors.Is(err, io.EOF) {
			meta := map[string]string{
				"phase":       "read_stream",
				"chunk_count": strconv.Itoa(chunkCount),
			}
			if strings.TrimSpace(lastPayload) != "" {
				meta["last_payload"] = debugTruncate(strings.TrimSpace(lastPayload), 4096)
			}
			c.recordBodyFailure(http.MethodPost, req.URL.Path, requestBody, resp, "read_stream", err, meta, capture.String())
			return ChatResponse{}, err
		}

		capture.Append(line)
		updateActiveStream(map[string]string{
			"phase":       "streaming",
			"chunk_count": strconv.Itoa(chunkCount),
		})
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "data:") {
			payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			lastPayload = payload
			if payload == "[DONE]" {
				if onEvent != nil {
					onEvent(domain.Event{Kind: domain.EventKindMessageDone})
				}
				meta := map[string]string{
					"phase":       "stream_complete",
					"chunk_count": strconv.Itoa(chunkCount),
				}
				aggregated.addPromptProgressMeta(meta)
				recordTrace("", meta)
				return aggregated.Response(), nil
			}
			var chunk chatChunk
			if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
				meta := map[string]string{
					"phase":       "decode_sse_chunk",
					"chunk_count": strconv.Itoa(chunkCount),
				}
				if strings.TrimSpace(payload) != "" {
					meta["last_payload"] = debugTruncate(payload, 4096)
				}
				c.recordBodyFailure(http.MethodPost, req.URL.Path, requestBody, resp, "decode_sse_chunk", err, meta, capture.String())
				return ChatResponse{}, fmt.Errorf("decode sse chunk: %w", err)
			}
			chunkCount++
			aggregated.Apply(chunk)
			if toolCallErr, ok := aggregated.TakeOversizedToolCall(input.ToolArgumentLimits); ok {
				if onEvent != nil {
					onEvent(domain.Event{Kind: domain.EventKindStatus, Text: toolCallErr.Message})
					onEvent(domain.Event{Kind: domain.EventKindMessageDone})
				}
				recordTrace(toolCallErr.Message, map[string]string{
					"phase":        "stream_tool_arguments_exceeded",
					"chunk_count":  strconv.Itoa(chunkCount),
					"tool":         strings.TrimSpace(toolCallErr.ToolCall.Function.Name),
					"tool_call_id": strings.TrimSpace(toolCallErr.ToolCall.ID),
				})
				response := aggregated.Response()
				response.ToolCallErrors = append(response.ToolCallErrors, toolCallErr)
				return response, nil
			}
			if onEvent != nil {
				c.emitChunk(onEvent, chunk, payload, aggregated.toolCalls)
			}
		}

		if errors.Is(err, io.EOF) {
			meta := map[string]string{
				"phase":       "stream_eof",
				"chunk_count": strconv.Itoa(chunkCount),
			}
			aggregated.addPromptProgressMeta(meta)
			recordTrace("", meta)
			return aggregated.Response(), nil
		}
	}
}

func (c *Client) recordBodyFailure(method, path, requestBody string, resp *http.Response, phase string, err error, meta map[string]string, responseBody string) {
	if c == nil || c.recorder == nil || resp == nil || err == nil {
		return
	}
	if meta == nil {
		meta = map[string]string{}
	}
	meta["phase"] = phase
	var requestHeaders map[string]string
	var debugMeta requestDebugContext
	if resp.Request != nil {
		requestHeaders = redactHeaders(resp.Request.Header)
		debugMeta, _ = resp.Request.Context().Value(requestDebugContextKey{}).(requestDebugContext)
	}
	trace := debugsrv.HTTPTrace{
		ProviderID:   c.provider,
		SessionID:    debugMeta.SessionID,
		ChatID:       debugMeta.ChatID,
		Method:       method,
		Path:         path,
		Status:       resp.StatusCode,
		RequestBody:  redactBody(requestBody),
		RequestHdrs:  requestHeaders,
		ResponseHdrs: redactHeaders(resp.Header),
		ResponseBody: strings.TrimSpace(responseBody),
		Meta:         meta,
		Error:        err.Error(),
	}
	if trace.ResponseBody == "" {
		trace.ResponseBody = "[stream/body read failed after headers]"
	}
	if !strings.Contains(strings.ToLower(resp.Header.Get("Content-Type")), "text/event-stream") {
		trace.ResponseBody = "[body read failed after headers]"
	}
	c.recorder.RecordHTTP(trace)
}

func (c *Client) startActiveHTTP(ctx context.Context, input ChatRequest, method, path, requestBody string, requestHeaders map[string]string) (context.Context, id.ID, func()) {
	if c == nil || c.recorder == nil {
		return ctx, "", func() {}
	}
	return c.recorder.StartHTTP(ctx, debugsrv.HTTPTrace{
		ProviderID:  c.provider,
		SessionID:   input.SessionID,
		ChatID:      input.ChatID,
		Method:      method,
		Path:        path,
		RequestBody: redactBody(requestBody),
		RequestHdrs: requestHeaders,
	})
}

func (c *Client) updateActiveHTTP(requestID id.ID, trace debugsrv.HTTPTrace) {
	if c == nil || c.recorder == nil || requestID == "" {
		return
	}
	c.recorder.UpdateActiveHTTP(requestID, trace)
}

func debugTruncate(value string, max int) string {
	value = strings.TrimSpace(value)
	if len(value) <= max {
		return value
	}
	return value[:max-1] + "…"
}

func (c *Client) emitChunk(emit func(domain.Event), chunk chatChunk, raw string, currentToolCalls []ToolCall) {
	if len(chunk.Choices) > 0 {
		choice := chunk.Choices[0]
		if choice.Delta.Content != "" {
			emit(domain.Event{Kind: domain.EventKindMessageDelta, Text: choice.Delta.Content, RawJSON: raw})
		}
		if reasoning := firstPresentString(choice.Delta.ReasoningContent, choice.Delta.Reasoning); reasoning != "" {
			emit(domain.Event{Kind: domain.EventKindReasoning, Text: reasoning, RawJSON: raw})
		}
		if len(choice.Delta.ToolCalls) > 0 || len(choice.Message.ToolCalls) > 0 {
			emit(providerToolCallDeltaEvent(raw, currentToolCalls))
		}
		if choice.FinishReason != "" {
			emit(domain.Event{Kind: domain.EventKindStatus, Text: choice.FinishReason})
		}
	}
	usage := domain.Usage{
		PromptTokens:     chunk.Usage.PromptTokens,
		CompletionTokens: chunk.Usage.CompletionTokens,
		CachedTokens:     cachedTokensFromUsage(chunk.Usage.PromptTokensDetails.CachedTokens, chunk.Usage.InputTokensDetails.CachedTokens),
		TotalTokens:      chunk.Usage.TotalTokens,
	}.Normalized()
	if usage.HasAnyTokens() {
		emit(domain.Event{
			Kind:  domain.EventKindUsage,
			Usage: usage,
		})
	}
	if chunk.PromptProgress.Total > 0 || chunk.PromptProgress.Processed > 0 || chunk.PromptProgress.Cache > 0 {
		total := chunk.PromptProgress.Total
		processed := chunk.PromptProgress.Processed
		text := "Processing prompt"
		if total > 0 {
			text = fmt.Sprintf("Processing prompt %d%%", processed*100/total)
		}
		meta := map[string]string{
			domain.EventMetaPromptProgress: "true",
			"processed":                    strconv.Itoa(processed),
			"total":                        strconv.Itoa(total),
			"cache":                        strconv.Itoa(chunk.PromptProgress.Cache),
			"time_ms":                      strconv.FormatInt(chunk.PromptProgress.TimeMS, 10),
		}
		emit(domain.Event{Kind: domain.EventKindStatus, Text: text, Meta: meta, RawJSON: raw})
	}
}

func providerToolCallDeltaEvent(raw string, currentToolCalls []ToolCall) domain.Event {
	evt := domain.Event{Kind: domain.EventKindToolCallDelta, Text: "provider tool call delta", RawJSON: raw}
	if len(currentToolCalls) == 0 {
		return evt
	}
	call := currentToolCalls[len(currentToolCalls)-1]
	evt.ToolCallID = call.ID
	if name := call.Function.Name; name != "" {
		evt.Tool = domain.ToolKind(name)
	}
	meta := map[string]string{}
	if args := call.Function.Arguments; args != "" {
		meta["arguments"] = args
	}
	if call.Index != nil {
		meta["index"] = strconv.Itoa(*call.Index)
	}
	if len(meta) > 0 {
		evt.Meta = meta
	}
	return evt
}

func convertToolCalls(raw []rawToolCall) []ToolCall {
	if len(raw) == 0 {
		return nil
	}
	calls := make([]ToolCall, 0, len(raw))
	for _, item := range raw {
		calls = append(calls, ToolCall{
			ID:    item.ID,
			Index: item.Index,
			Type:  firstPresentString(item.Type, "function"),
			Function: FunctionCall{
				Name:      item.Function.Name,
				Arguments: item.Function.Arguments,
			},
		})
	}
	return calls
}

type streamedChatResponse struct {
	text               strings.Builder
	reasoning          strings.Builder
	usage              domain.Usage
	toolCalls          []ToolCall
	promptProgressSeen bool
	promptProgress     struct {
		total     int
		cache     int
		processed int
		timeMS    int64
	}
}

func (r *streamedChatResponse) Apply(chunk chatChunk) {
	if len(chunk.Choices) > 0 {
		choice := chunk.Choices[0]
		if choice.Delta.Content != "" {
			r.text.WriteString(choice.Delta.Content)
		} else if choice.Message.Content != "" {
			r.text.WriteString(choice.Message.Content)
		}
		if reasoning := firstPresentString(choice.Delta.ReasoningContent, choice.Delta.Reasoning); reasoning != "" {
			r.reasoning.WriteString(reasoning)
		} else if reasoning := firstPresentString(choice.Message.ReasoningContent, choice.Message.Reasoning); reasoning != "" {
			r.reasoning.WriteString(reasoning)
		}
		r.toolCalls = mergeToolCalls(r.toolCalls, convertToolCalls(choice.Delta.ToolCalls))
		r.toolCalls = mergeToolCalls(r.toolCalls, convertToolCalls(choice.Message.ToolCalls))
	}
	usage := domain.Usage{
		PromptTokens:     chunk.Usage.PromptTokens,
		CompletionTokens: chunk.Usage.CompletionTokens,
		CachedTokens:     cachedTokensFromUsage(chunk.Usage.PromptTokensDetails.CachedTokens, chunk.Usage.InputTokensDetails.CachedTokens),
		TotalTokens:      chunk.Usage.TotalTokens,
	}.Normalized()
	if usage.HasAnyTokens() {
		r.usage = usage
	}
	if chunk.PromptProgress.Total > 0 || chunk.PromptProgress.Processed > 0 || chunk.PromptProgress.Cache > 0 {
		r.promptProgressSeen = true
		r.promptProgress.total = chunk.PromptProgress.Total
		r.promptProgress.cache = chunk.PromptProgress.Cache
		r.promptProgress.processed = chunk.PromptProgress.Processed
		r.promptProgress.timeMS = chunk.PromptProgress.TimeMS
	}
}

func (r streamedChatResponse) addPromptProgressMeta(meta map[string]string) {
	if !r.promptProgressSeen || meta == nil {
		return
	}
	meta["prompt_progress_total"] = strconv.Itoa(r.promptProgress.total)
	meta["prompt_progress_cache"] = strconv.Itoa(r.promptProgress.cache)
	meta["prompt_progress_processed"] = strconv.Itoa(r.promptProgress.processed)
	meta["prompt_progress_time_ms"] = strconv.FormatInt(r.promptProgress.timeMS, 10)
}

func cachedTokensFromUsage(values ...int) int {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}

func (r streamedChatResponse) Response() ChatResponse {
	return ChatResponse{
		Text:               r.text.String(),
		Reasoning:          r.reasoning.String(),
		Usage:              r.usage,
		ToolCalls:          r.toolCalls,
		PromptProgressSeen: r.promptProgressSeen,
	}
}

func (r *streamedChatResponse) TakeOversizedToolCall(limits map[string]int) (ToolCallError, bool) {
	if len(limits) == 0 {
		return ToolCallError{}, false
	}
	for i, call := range r.toolCalls {
		name := strings.TrimSpace(call.Function.Name)
		limit := limits[name]
		if limit <= 0 || len(call.Function.Arguments) <= limit {
			continue
		}
		r.toolCalls = slices.Delete(r.toolCalls, i, len(r.toolCalls))
		return ToolCallError{
			ToolCall: call,
			Message:  fmt.Sprintf("%s tool arguments exceeded %s while streaming. Use smaller tool calls.", name, formatByteLimit(limit)),
		}, true
	}
	return ToolCallError{}, false
}

func formatByteLimit(limit int) string {
	if limit > 0 && limit%1024 == 0 {
		return fmt.Sprintf("%d KiB", limit/1024)
	}
	return fmt.Sprintf("%d bytes", limit)
}

func mergeToolCalls(existing, incoming []ToolCall) []ToolCall {
	if len(incoming) == 0 {
		return existing
	}
	if len(existing) == 0 {
		return slices.Clone(incoming)
	}
	merged := slices.Clone(existing)
	for _, next := range incoming {
		index := -1
		if next.Index != nil {
			for i, current := range merged {
				if current.Index != nil && *current.Index == *next.Index {
					index = i
					break
				}
			}
		}
		if index >= 0 {
			goto merge
		}
		for i, current := range merged {
			if next.ID != "" && current.ID == next.ID {
				index = i
				break
			}
		}
		if index < 0 {
			for i, current := range merged {
				if current.ID == "" && current.Function.Name == next.Function.Name {
					index = i
					break
				}
			}
		}
		if index < 0 {
			merged = append(merged, next)
			continue
		}
	merge:
		if next.ID != "" {
			merged[index].ID = next.ID
		}
		if next.Index != nil {
			merged[index].Index = next.Index
		}
		if next.Type != "" {
			merged[index].Type = next.Type
		}
		if next.Function.Name != "" {
			merged[index].Function.Name = next.Function.Name
		}
		if next.Function.Arguments != "" {
			merged[index].Function.Arguments += next.Function.Arguments
		}
	}
	return merged
}

func firstPresentString(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func imageDataURL(mimeType string, data []byte) string {
	mimeType = strings.TrimSpace(mimeType)
	if mimeType == "" {
		mimeType = "image/png"
	}
	return "data:" + mimeType + ";base64," + base64.StdEncoding.EncodeToString(data)
}

type tracingTransport struct {
	base       http.RoundTripper
	recorder   *debugsrv.Recorder
	providerID string
}

func (t *tracingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	start := time.Now()
	meta, _ := req.Context().Value(requestDebugContextKey{}).(requestDebugContext)
	var requestBody string
	if req.Body != nil {
		body, _ := io.ReadAll(req.Body)
		requestBody = redactBody(string(body))
		req.Body = io.NopCloser(bytes.NewReader(body))
	}
	resp, err := t.base.RoundTrip(req)
	trace := debugsrv.HTTPTrace{
		ProviderID:  t.providerID,
		SessionID:   meta.SessionID,
		ChatID:      meta.ChatID,
		Method:      req.Method,
		Path:        req.URL.Path,
		DurationMS:  time.Since(start).Milliseconds(),
		RequestBody: requestBody,
		RequestHdrs: redactHeaders(req.Header),
	}
	if err != nil {
		trace.Error = err.Error()
		t.recorder.RecordHTTP(trace)
		return nil, err
	}
	trace.Status = resp.StatusCode
	trace.ResponseHdrs = redactHeaders(resp.Header)
	if resp.Body != nil {
		if !strings.Contains(strings.ToLower(resp.Header.Get("Content-Type")), "text/event-stream") {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
			trace.ResponseBody = redactBody(string(body))
			resp.Body = io.NopCloser(io.MultiReader(bytes.NewReader(body), resp.Body))
		}
	}
	if !strings.Contains(strings.ToLower(resp.Header.Get("Content-Type")), "text/event-stream") {
		t.recorder.RecordHTTP(trace)
	}
	return resp, nil
}

type debugStreamCapture struct {
	builder   strings.Builder
	limit     int
	truncated bool
}

func newDebugStreamCapture(limit int) *debugStreamCapture {
	if limit <= 0 {
		limit = 8192
	}
	return &debugStreamCapture{limit: limit}
}

func (c *debugStreamCapture) Append(text string) {
	if c == nil || text == "" || c.limit <= 0 {
		return
	}
	if c.truncated {
		return
	}
	remaining := c.limit - c.builder.Len()
	if remaining <= 0 {
		c.truncated = true
		return
	}
	if len(text) <= remaining {
		c.builder.WriteString(text)
		return
	}
	c.builder.WriteString(text[:remaining])
	c.truncated = true
}

func (c *debugStreamCapture) String() string {
	if c == nil {
		return ""
	}
	out := c.builder.String()
	if c.truncated {
		out += "…"
	}
	return redactBody(out)
}

func redactHeaders(headers http.Header) map[string]string {
	if len(headers) == 0 {
		return nil
	}
	out := make(map[string]string, len(headers))
	for key, values := range headers {
		lower := strings.ToLower(key)
		if lower == "authorization" || strings.Contains(lower, "api-key") || strings.Contains(lower, "token") {
			out[key] = "[redacted]"
			continue
		}
		out[key] = strings.Join(values, ", ")
	}
	return out
}

func redactBody(body string) string {
	body = strings.TrimSpace(body)
	body = strings.ReplaceAll(body, "Bearer ", "Bearer [redacted]")
	return body
}

func (c *Client) apiPath(path string) string {
	return path
}

func (c *Client) newRequest(ctx context.Context, method, path string, body io.Reader) (*http.Request, error) {
	return c.newRequestAt(ctx, method, c.baseURL, path, body)
}

func (c *Client) newRequestAt(ctx context.Context, method, baseURL, path string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, baseURL+path, body)
	if err != nil {
		return nil, fmt.Errorf("new request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	for key, value := range c.headers {
		req.Header.Set(key, value)
	}
	return req, nil
}
