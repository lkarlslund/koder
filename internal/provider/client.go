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
	Role         domain.MessageRole `json:"role"`
	Content      string             `json:"content,omitempty"`
	ContentParts []ContentPart      `json:"-"`
	ToolCallID   string             `json:"tool_call_id,omitempty"`
	ToolCalls    []ToolCall         `json:"tool_calls,omitempty"`
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
		Role       domain.MessageRole `json:"role"`
		Content    any                `json:"content,omitempty"`
		ToolCallID string             `json:"tool_call_id,omitempty"`
		ToolCalls  []ToolCall         `json:"tool_calls,omitempty"`
	}
	var content any
	trimmed := strings.TrimSpace(m.Content)
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
					"text": part.Text,
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
		Role:       m.Role,
		Content:    content,
		ToolCallID: m.ToolCallID,
		ToolCalls:  m.ToolCalls,
	})
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

type ChatRequest struct {
	Model      string           `json:"model"`
	Messages   []Message        `json:"messages"`
	Tools      []ToolDefinition `json:"tools,omitempty"`
	ToolChoice string           `json:"tool_choice,omitempty"`
	Stream     bool             `json:"stream"`
	ExtraBody  map[string]any   `json:"-"`
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

type modelsResponse struct {
	Data []struct {
		ID          string `json:"id"`
		OwnedBy     string `json:"owned_by"`
		MaxModelLen int    `json:"max_model_len"`
	} `json:"data"`
}

type propsResponse struct {
	DefaultGenerationSettings struct {
		NCtx int `json:"n_ctx"`
	} `json:"default_generation_settings"`
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
	apiKey   string
	headers  map[string]string
	provider string
	recorder *debugsrv.Recorder
}

type ChatResponse struct {
	Text      string
	Reasoning string
	Usage     domain.Usage
	ToolCalls []ToolCall
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
		apiKey:   cfg.APIKey,
		headers:  cfg.Headers,
		provider: id,
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
		models = append(models, domain.Model{ID: item.ID, OwnedBy: item.OwnedBy})
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
		if strings.TrimSpace(item.ID) == modelID && item.MaxModelLen > 0 {
			return item.MaxModelLen, nil
		}
	}
	return 0, nil
}

func (c *Client) Props(ctx context.Context, modelID string) (propsResponse, error) {
	path := "/props"
	if trimmed := strings.TrimSpace(modelID); trimmed != "" {
		path += "?model=" + url.QueryEscape(trimmed)
	}
	return c.propsRequest(ctx, path)
}

func DetectContextWindow(ctx context.Context, providerID string, cfg config.Provider, modelID string, recorder *debugsrv.Recorder) (int, error) {
	if !SupportsContextWindowDetection(cfg) {
		return cfg.ContextWindow, nil
	}
	for _, baseURL := range contextWindowProbeBaseURLs(cfg.BaseURL) {
		probeCfg := cfg
		probeCfg.BaseURL = baseURL
		client, err := New(providerID, probeCfg, recorder)
		if err != nil {
			return 0, err
		}
		maxModelLen, err := client.DetectModelContextWindow(ctx, modelID)
		if err == nil {
			if maxModelLen > 0 {
				return maxModelLen, nil
			}
		} else if !isOptionalContextWindowProbeError(err) {
			return 0, err
		}
		props, err := client.Props(ctx, modelID)
		if err == nil {
			if props.DefaultGenerationSettings.NCtx > 0 {
				return props.DefaultGenerationSettings.NCtx, nil
			}
			return cfg.ContextWindow, nil
		}
		if !isOptionalContextWindowProbeError(err) {
			return 0, err
		}
	}
	return cfg.ContextWindow, nil
}

func SupportsContextWindowDetection(cfg config.Provider) bool {
	return strings.TrimSpace(cfg.Kind) == ProviderKindCompatible &&
		strings.TrimSpace(cfg.BaseURL) != ""
}

func contextWindowProbeBaseURLs(baseURL string) []string {
	trimmed := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if trimmed == "" {
		return nil
	}
	withoutV1 := strings.TrimSuffix(trimmed, "/v1")
	if withoutV1 == trimmed {
		return []string{trimmed}
	}
	return []string{withoutV1, trimmed}
}

func isOptionalContextWindowProbeError(err error) bool {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return apiErr.StatusCode == http.StatusNotFound || apiErr.StatusCode == http.StatusMethodNotAllowed
	}
	return false
}

func (c *Client) propsRequest(ctx context.Context, path string) (propsResponse, error) {
	req, err := c.newRequest(ctx, http.MethodGet, path, nil)
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
	var body bytes.Buffer
	if err := json.NewEncoder(&body).Encode(input); err != nil {
		return ChatResponse{}, fmt.Errorf("encode chat request: %w", err)
	}
	requestBody := body.String()
	req, err := c.newRequest(ctx, http.MethodPost, c.apiPath("/chat/completions"), &body)
	if err != nil {
		return ChatResponse{}, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return ChatResponse{}, fmt.Errorf("complete chat: %w", err)
	}
	defer resp.Body.Close()
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
		Reasoning: firstNonEmptyString(choice.Message.ReasoningContent, choice.Message.Reasoning),
		Usage:     usage,
		ToolCalls: convertToolCalls(choice.Message.ToolCalls),
	}, nil
}

func (c *Client) ProbeImageSupport(ctx context.Context, modelID string) (bool, error) {
	_, err := c.CompleteChat(ctx, ChatRequest{
		Model: modelID,
		Messages: []Message{{
			Role: domain.MessageRoleUser,
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
	start := time.Now()
	var body bytes.Buffer
	if err := json.NewEncoder(&body).Encode(input); err != nil {
		return ChatResponse{}, fmt.Errorf("encode chat request: %w", err)
	}
	requestBody := body.String()
	req, err := c.newRequest(ctx, http.MethodPost, c.apiPath("/chat/completions"), &body)
	if err != nil {
		return ChatResponse{}, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return ChatResponse{}, fmt.Errorf("stream chat: %w", err)
	}
	defer resp.Body.Close()
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
		c.recorder.RecordHTTP(trace)
	}
	for {
		line, err := reader.ReadString('\n')
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
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "data:") {
			payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			lastPayload = payload
			if payload == "[DONE]" {
				if onEvent != nil {
					onEvent(domain.Event{Kind: domain.EventKindMessageDone})
				}
				recordTrace("", map[string]string{
					"phase":       "stream_complete",
					"chunk_count": strconv.Itoa(chunkCount),
				})
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
			if onEvent != nil {
				c.emitChunk(onEvent, chunk, payload)
			}
		}

		if errors.Is(err, io.EOF) {
			recordTrace("", map[string]string{
				"phase":       "stream_eof",
				"chunk_count": strconv.Itoa(chunkCount),
			})
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
	if resp.Request != nil {
		requestHeaders = redactHeaders(resp.Request.Header)
	}
	trace := debugsrv.HTTPTrace{
		ProviderID:   c.provider,
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

func debugTruncate(value string, max int) string {
	value = strings.TrimSpace(value)
	if len(value) <= max {
		return value
	}
	return value[:max-1] + "…"
}

func (c *Client) emitChunk(emit func(domain.Event), chunk chatChunk, raw string) {
	if len(chunk.Choices) > 0 {
		choice := chunk.Choices[0]
		if choice.Delta.Content != "" {
			emit(domain.Event{Kind: domain.EventKindMessageDelta, Text: choice.Delta.Content, RawJSON: raw})
		}
		if reasoning := firstNonEmptyString(choice.Delta.ReasoningContent, choice.Delta.Reasoning); reasoning != "" {
			emit(domain.Event{Kind: domain.EventKindReasoning, Text: reasoning, RawJSON: raw})
		}
		if len(choice.Delta.ToolCalls) > 0 || len(choice.Message.ToolCalls) > 0 {
			emit(domain.Event{Kind: domain.EventKindToolCallDelta, Text: "provider tool call delta", RawJSON: raw})
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
			Type:  firstNonEmptyString(item.Type, "function"),
			Function: FunctionCall{
				Name:      item.Function.Name,
				Arguments: item.Function.Arguments,
			},
		})
	}
	return calls
}

type streamedChatResponse struct {
	text      strings.Builder
	reasoning strings.Builder
	usage     domain.Usage
	toolCalls []ToolCall
}

func (r *streamedChatResponse) Apply(chunk chatChunk) {
	if len(chunk.Choices) > 0 {
		choice := chunk.Choices[0]
		if choice.Delta.Content != "" {
			r.text.WriteString(choice.Delta.Content)
		} else if choice.Message.Content != "" {
			r.text.WriteString(choice.Message.Content)
		}
		if reasoning := firstNonEmptyString(choice.Delta.ReasoningContent, choice.Delta.Reasoning); reasoning != "" {
			r.reasoning.WriteString(reasoning)
		} else if reasoning := firstNonEmptyString(choice.Message.ReasoningContent, choice.Message.Reasoning); reasoning != "" {
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
		Text:      r.text.String(),
		Reasoning: r.reasoning.String(),
		Usage:     r.usage,
		ToolCalls: r.toolCalls,
	}
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
		if strings.TrimSpace(next.ID) != "" {
			merged[index].ID = next.ID
		}
		if next.Index != nil {
			merged[index].Index = next.Index
		}
		if strings.TrimSpace(next.Type) != "" {
			merged[index].Type = next.Type
		}
		if strings.TrimSpace(next.Function.Name) != "" {
			merged[index].Function.Name = next.Function.Name
		}
		if strings.TrimSpace(next.Function.Arguments) != "" {
			merged[index].Function.Arguments += next.Function.Arguments
		}
	}
	return merged
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
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
	var requestBody string
	if req.Body != nil {
		body, _ := io.ReadAll(req.Body)
		requestBody = redactBody(string(body))
		req.Body = io.NopCloser(bytes.NewReader(body))
	}
	resp, err := t.base.RoundTrip(req)
	trace := debugsrv.HTTPTrace{
		ProviderID:  t.providerID,
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
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
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
