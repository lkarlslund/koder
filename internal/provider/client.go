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
	"strings"
	"time"

	"github.com/lkarlslund/koder/internal/config"
	"github.com/lkarlslund/koder/internal/debugsrv"
	"github.com/lkarlslund/koder/internal/domain"
)

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
}

type modelsResponse struct {
	Data []struct {
		ID      string `json:"id"`
		OwnedBy string `json:"owned_by"`
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
	if _, err := url.Parse(cfg.BaseURL); err != nil {
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
		baseURL:  strings.TrimRight(cfg.BaseURL, "/"),
		apiKey:   cfg.APIKey,
		headers:  cfg.Headers,
		provider: id,
	}, nil
}

func (c *Client) ListModels(ctx context.Context) ([]domain.Model, error) {
	req, err := c.newRequest(ctx, http.MethodGet, "/models", nil)
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
		return nil, fmt.Errorf("list models status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
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

func (c *Client) Props(ctx context.Context, modelID string) (propsResponse, error) {
	path := "/props"
	if trimmed := strings.TrimSpace(modelID); trimmed != "" {
		path += "?model=" + url.QueryEscape(trimmed)
	}
	props, err := c.propsRequest(ctx, path)
	if err == nil || strings.TrimSpace(modelID) == "" {
		return props, err
	}
	return c.propsRequest(ctx, "/props")
}

func DetectContextWindow(ctx context.Context, providerID string, cfg config.Provider, modelID string, recorder *debugsrv.Recorder) (int, error) {
	if strings.TrimSpace(providerID) != "llamacpp" {
		return cfg.ContextWindow, nil
	}
	client, err := New(providerID, cfg, recorder)
	if err != nil {
		return 0, err
	}
	props, err := client.Props(ctx, modelID)
	if err != nil {
		return 0, err
	}
	return props.DefaultGenerationSettings.NCtx, nil
}

func (c *Client) propsRequest(ctx context.Context, path string) (propsResponse, error) {
	rootURL := strings.TrimSuffix(c.baseURL, "/v1") + path
	payload, status, err := c.propsRequestURL(ctx, rootURL)
	if err == nil {
		return payload, nil
	}
	if status != http.StatusNotFound || rootURL == c.baseURL+path {
		return propsResponse{}, err
	}
	payload, _, err = c.propsRequestURL(ctx, c.baseURL+path)
	return payload, err
}

func (c *Client) propsRequestURL(ctx context.Context, endpoint string) (propsResponse, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return propsResponse{}, 0, fmt.Errorf("new request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	for key, value := range c.headers {
		req.Header.Set(key, value)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return propsResponse{}, 0, fmt.Errorf("get props: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
		return propsResponse{}, resp.StatusCode, fmt.Errorf("props status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var payload propsResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return propsResponse{}, resp.StatusCode, fmt.Errorf("decode props: %w", err)
	}
	return payload, resp.StatusCode, nil
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
	req, err := c.newRequest(ctx, http.MethodPost, "/chat/completions", &body)
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
		return ChatResponse{}, fmt.Errorf("chat status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
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
		Reasoning: choice.Message.ReasoningContent,
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
	req, err := c.newRequest(ctx, http.MethodPost, "/chat/completions", &body)
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
		return nil, fmt.Errorf("chat status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
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
		if choice.Delta.ReasoningContent != "" {
			events <- domain.Event{Kind: domain.EventKindReasoning, Text: choice.Delta.ReasoningContent, RawJSON: raw}
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
