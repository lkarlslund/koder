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

func (m Message) MarshalJSON() ([]byte, error) {
	type wireMessage struct {
		Role       domain.MessageRole `json:"role"`
		Content    any                `json:"content,omitempty"`
		ToolCallID string             `json:"tool_call_id,omitempty"`
		ToolCalls  []ToolCall         `json:"tool_calls,omitempty"`
	}
	content := any(strings.TrimSpace(m.Content))
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
			Content          string `json:"content"`
			Reasoning        string `json:"reasoning"`
			ReasoningContent string `json:"reasoning_content"`
			ToolCalls        []struct {
				ID       string `json:"id"`
				Type     string `json:"type"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"delta"`
		Message struct {
			Content          string `json:"content"`
			Reasoning        string `json:"reasoning"`
			ReasoningContent string `json:"reasoning_content"`
			ToolCalls        []struct {
				ID       string `json:"id"`
				Type     string `json:"type"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage"`
}

type Client struct {
	http     *http.Client
	baseURL  string
	apiKey   string
	headers  map[string]string
	provider string
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
		timeout = 2 * time.Minute
	}
	transport := http.DefaultTransport
	if recorder != nil {
		transport = &tracingTransport{
			base:       http.DefaultTransport,
			recorder:   recorder,
			providerID: id,
		}
	}
	return &Client{
		http: &http.Client{
			Timeout:   timeout,
			Transport: transport,
		},
		baseURL:  baseURL,
		apiKey:   cfg.APIKey,
		headers:  cfg.Headers,
		provider: id,
	}, nil
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
		return ChatResponse{}, fmt.Errorf("decode chat response: %w", err)
	}
	if len(payload.Choices) == 0 {
		return ChatResponse{}, nil
	}
	choice := payload.Choices[0]
	usage := domain.Usage{
		PromptTokens:     payload.Usage.PromptTokens,
		CompletionTokens: payload.Usage.CompletionTokens,
		TotalTokens:      payload.Usage.TotalTokens,
	}
	return ChatResponse{
		Text:      choice.Message.Content,
		Reasoning: firstNonEmptyString(choice.Message.ReasoningContent, choice.Message.Reasoning),
		Usage:     usage,
		ToolCalls: convertToolCalls(choice.Message.ToolCalls),
	}, nil
}

func (c *Client) StreamChat(ctx context.Context, input ChatRequest) (<-chan domain.Event, error) {
	input.Stream = true
	var body bytes.Buffer
	if err := json.NewEncoder(&body).Encode(input); err != nil {
		return nil, fmt.Errorf("encode chat request: %w", err)
	}
	req, err := c.newRequest(ctx, http.MethodPost, c.apiPath("/chat/completions"), &body)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("stream chat: %w", err)
	}
	if resp.StatusCode >= 300 {
		defer resp.Body.Close()
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
		return nil, &APIError{
			Operation:  "chat",
			StatusCode: resp.StatusCode,
			Body:       strings.TrimSpace(string(body)),
			RetryAfter: parseRetryAfter(resp.Header.Get("Retry-After"), time.Now()),
		}
	}

	events := make(chan domain.Event)
	go func() {
		defer close(events)
		defer resp.Body.Close()

		reader := bufio.NewReader(resp.Body)
		for {
			line, err := reader.ReadString('\n')
			if err != nil && !errors.Is(err, io.EOF) {
				events <- domain.Event{Kind: domain.EventKindError, Err: err}
				return
			}

			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "data:") {
				payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
				if payload == "[DONE]" {
					events <- domain.Event{Kind: domain.EventKindMessageDone}
					return
				}
				var chunk chatChunk
				if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
					events <- domain.Event{Kind: domain.EventKindError, Err: fmt.Errorf("decode sse chunk: %w", err), RawJSON: payload}
					return
				}
				c.emitChunk(events, chunk, payload)
			}

			if errors.Is(err, io.EOF) {
				return
			}
		}
	}()
	return events, nil
}

func (c *Client) emitChunk(events chan<- domain.Event, chunk chatChunk, raw string) {
	if len(chunk.Choices) > 0 {
		choice := chunk.Choices[0]
		if choice.Delta.Content != "" {
			events <- domain.Event{Kind: domain.EventKindMessageDelta, Text: choice.Delta.Content, RawJSON: raw}
		}
		if reasoning := firstNonEmptyString(choice.Delta.ReasoningContent, choice.Delta.Reasoning); reasoning != "" {
			events <- domain.Event{Kind: domain.EventKindReasoning, Text: reasoning, RawJSON: raw}
		}
		if choice.FinishReason != "" {
			events <- domain.Event{Kind: domain.EventKindStatus, Text: choice.FinishReason}
		}
	}
	if chunk.Usage.TotalTokens > 0 {
		events <- domain.Event{
			Kind: domain.EventKindUsage,
			Usage: domain.Usage{
				PromptTokens:     chunk.Usage.PromptTokens,
				CompletionTokens: chunk.Usage.CompletionTokens,
				TotalTokens:      chunk.Usage.TotalTokens,
			},
		}
	}
}

func convertToolCalls(raw []struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}) []ToolCall {
	if len(raw) == 0 {
		return nil
	}
	calls := make([]ToolCall, 0, len(raw))
	for _, item := range raw {
		calls = append(calls, ToolCall{
			ID:   item.ID,
			Type: firstNonEmptyString(item.Type, "function"),
			Function: FunctionCall{
				Name:      item.Function.Name,
				Arguments: item.Function.Arguments,
			},
		})
	}
	return calls
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
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
		trace.ResponseBody = redactBody(string(body))
		resp.Body = io.NopCloser(io.MultiReader(bytes.NewReader(body), resp.Body))
	}
	t.recorder.RecordHTTP(trace)
	return resp, nil
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
