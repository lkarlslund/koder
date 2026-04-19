package provider

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/lkarlslund/koder/internal/config"
	"github.com/lkarlslund/koder/internal/domain"
)

type Message struct {
	Role    domain.MessageRole `json:"role"`
	Content string             `json:"content"`
}

type ChatRequest struct {
	Model    string    `json:"model"`
	Messages []Message `json:"messages"`
	Stream   bool      `json:"stream"`
}

type modelsResponse struct {
	Data []struct {
		ID      string `json:"id"`
		OwnedBy string `json:"owned_by"`
	} `json:"data"`
}

type chatChunk struct {
	Choices []struct {
		Delta struct {
			Content          string `json:"content"`
			ReasoningContent string `json:"reasoning_content"`
		} `json:"delta"`
		Message struct {
			Content          string `json:"content"`
			ReasoningContent string `json:"reasoning_content"`
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

func New(id string, cfg config.Provider) (*Client, error) {
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
	return &Client{
		http: &http.Client{
			Timeout: timeout,
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

func (c *Client) CompleteChat(ctx context.Context, input ChatRequest) (string, string, domain.Usage, error) {
	var body bytes.Buffer
	if err := json.NewEncoder(&body).Encode(input); err != nil {
		return "", "", domain.Usage{}, fmt.Errorf("encode chat request: %w", err)
	}
	req, err := c.newRequest(ctx, http.MethodPost, "/chat/completions", &body)
	if err != nil {
		return "", "", domain.Usage{}, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return "", "", domain.Usage{}, fmt.Errorf("complete chat: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
		return "", "", domain.Usage{}, fmt.Errorf("chat status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var payload chatChunk
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return "", "", domain.Usage{}, fmt.Errorf("decode chat response: %w", err)
	}
	if len(payload.Choices) == 0 {
		return "", "", domain.Usage{}, nil
	}
	choice := payload.Choices[0]
	usage := domain.Usage{
		PromptTokens:     payload.Usage.PromptTokens,
		CompletionTokens: payload.Usage.CompletionTokens,
		TotalTokens:      payload.Usage.TotalTokens,
	}
	return choice.Message.Content, choice.Message.ReasoningContent, usage, nil
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
