// Package embedding provides optional adapters for deriving Knowledge vectors.
package embedding

import (
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

	knowledgeService "github.com/lkarlslund/koder/internal/knowledge/service"
)

const maxEmbeddingResponseBytes = 64 << 20

type OpenAICompatibleConfig struct {
	ProviderID     string
	BaseURL        string
	APIKey         string
	ModelID        string
	Dimensions     int
	SendDimensions bool
	HTTPClient     *http.Client
}

// OpenAICompatible uses the OpenAI /embeddings wire shape. Construction only validates
// configuration; the first Embed call is the first network operation.
type OpenAICompatible struct {
	identity       knowledgeService.EmbeddingIdentity
	endpoint       string
	apiKey         string
	sendDimensions bool
	client         *http.Client
}

var _ knowledgeService.EmbeddingBackend = (*OpenAICompatible)(nil)

func NewOpenAICompatible(cfg OpenAICompatibleConfig) (*OpenAICompatible, error) {
	identity := knowledgeService.EmbeddingIdentity{
		ProviderID: strings.TrimSpace(cfg.ProviderID), ModelID: strings.TrimSpace(cfg.ModelID),
		Dimensions: cfg.Dimensions, Metric: knowledgeService.SemanticMetricCosine,
	}
	if err := identity.Validate(); err != nil {
		return nil, err
	}
	endpoint, err := embeddingEndpoint(cfg.BaseURL)
	if err != nil {
		return nil, err
	}
	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 2 * time.Minute}
	}
	return &OpenAICompatible{
		identity: identity, endpoint: endpoint, apiKey: strings.TrimSpace(cfg.APIKey),
		sendDimensions: cfg.SendDimensions, client: client,
	}, nil
}

func (b *OpenAICompatible) Identity() knowledgeService.EmbeddingIdentity {
	return b.identity
}

func (b *OpenAICompatible) Embed(ctx context.Context, inputs []string) ([][]float32, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(inputs) == 0 {
		return [][]float32{}, nil
	}
	requestBody := embeddingRequest{Model: b.identity.ModelID, Input: inputs, EncodingFormat: "float"}
	if b.sendDimensions {
		requestBody.Dimensions = b.identity.Dimensions
	}
	encoded, err := json.Marshal(requestBody)
	if err != nil {
		return nil, fmt.Errorf("encode embedding request: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, b.endpoint, bytes.NewReader(encoded))
	if err != nil {
		return nil, fmt.Errorf("create embedding request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	if b.apiKey != "" {
		request.Header.Set("Authorization", "Bearer "+b.apiKey)
	}
	response, err := b.client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("embedding request failed: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))
		return nil, fmt.Errorf("embedding endpoint returned HTTP %d", response.StatusCode)
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, maxEmbeddingResponseBytes))
	var payload embeddingResponse
	if err := decoder.Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode embedding response: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("decode embedding response: trailing data")
	}
	if len(payload.Data) != len(inputs) {
		return nil, fmt.Errorf("embedding endpoint returned %d vectors for %d inputs", len(payload.Data), len(inputs))
	}
	vectors := make([][]float32, len(inputs))
	for _, item := range payload.Data {
		if item.Index < 0 || item.Index >= len(vectors) || vectors[item.Index] != nil {
			return nil, fmt.Errorf("embedding endpoint returned an invalid or duplicate input index")
		}
		if len(item.Embedding) != b.identity.Dimensions {
			return nil, fmt.Errorf("embedding endpoint returned %d dimensions, want %d", len(item.Embedding), b.identity.Dimensions)
		}
		vectors[item.Index] = item.Embedding
	}
	return vectors, nil
}

type embeddingRequest struct {
	Model          string   `json:"model"`
	Input          []string `json:"input"`
	EncodingFormat string   `json:"encoding_format,omitempty"`
	Dimensions     int      `json:"dimensions,omitempty"`
}

type embeddingResponse struct {
	Data []struct {
		Embedding []float32 `json:"embedding"`
		Index     int       `json:"index"`
	} `json:"data"`
}

func embeddingEndpoint(rawBaseURL string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(rawBaseURL))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", fmt.Errorf("embedding base URL must be an absolute HTTP(S) URL without query or fragment")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", fmt.Errorf("embedding base URL must use HTTP or HTTPS")
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	if !strings.HasSuffix(parsed.Path, "/embeddings") {
		parsed.Path += "/embeddings"
	}
	return parsed.String(), nil
}
