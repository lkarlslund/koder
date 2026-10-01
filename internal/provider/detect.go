package provider

import (
	"context"
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

// ErrAuthRequired reports that a provider rejected detection for missing or
// invalid credentials, so the caller should ask for an API key.
var ErrAuthRequired = errors.New("provider requires authentication")

// Detection is what Detect learned about a provider's server.
type Detection struct {
	// BaseURL is the API base that answered the model list, which may add
	// /v1 to the URL the user entered.
	BaseURL   string
	Transport string
	Features  config.ProviderFeatures
	Models    []domain.Model
}

// Detect finds the provider's API base, lists its models and checks which
// endpoints it serves. The features it returns decide the kinds of the
// listed models, so every provider is handled by the same client.
func Detect(ctx context.Context, providerID string, cfg config.Provider, recorder *debugsrv.Recorder) (Detection, error) {
	var lastErr error
	for _, baseURL := range candidateBaseURLs(cfg.BaseURL) {
		cfg.BaseURL = baseURL
		client, err := New(providerID, cfg, recorder)
		if err != nil {
			return Detection{}, err
		}
		items, err := client.listModelItems(ctx)
		if err != nil {
			if apiErr, ok := errors.AsType[*APIError](err); ok && (apiErr.StatusCode == http.StatusUnauthorized || apiErr.StatusCode == http.StatusForbidden) {
				return Detection{}, fmt.Errorf("%w: %w", ErrAuthRequired, err)
			}
			lastErr = err
			continue
		}
		features := client.detectFeatures(ctx)
		client.features = &features
		return Detection{
			BaseURL:   baseURL,
			Transport: detectedTransport(baseURL, features, items),
			Features:  features,
			Models:    client.modelsFromItems(ctx, items),
		}, nil
	}
	return Detection{}, lastErr
}

// candidateBaseURLs returns the entered URL and, when it names no path, the
// conventional /v1 API base behind it.
func candidateBaseURLs(raw string) []string {
	baseURL := strings.TrimRight(strings.TrimSpace(raw), "/")
	candidates := []string{baseURL}
	if parsed, err := url.Parse(baseURL); err == nil && parsed.Path == "" {
		candidates = append(candidates, baseURL+"/v1")
	}
	return candidates
}

func (c *Client) detectFeatures(ctx context.Context) config.ProviderFeatures {
	features := config.ProviderFeatures{
		CheckedAt:     time.Now().UTC(),
		Chat:          c.endpointExists(ctx, "/chat/completions"),
		Decisions:     c.endpointExists(ctx, "/systemone"),
		Speech:        c.endpointExists(ctx, "/audio/speech"),
		Transcription: c.endpointExists(ctx, "/audio/transcriptions"),
	}
	_, err := c.propsRequest(ctx, c.llamaURL, "/props")
	features.LlamaProps = err == nil
	return features
}

// endpointExists posts an empty object to path. A server rejects that body
// as invalid on a route it serves, and answers 404 or 405 on one it lacks.
func (c *Client) endpointExists(ctx context.Context, path string) bool {
	req, err := c.newRequest(ctx, http.MethodPost, c.apiPath(path), strings.NewReader("{}"))
	if err != nil {
		return false
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return false
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	return resp.StatusCode != http.StatusNotFound && resp.StatusCode != http.StatusMethodNotAllowed
}

// detectedTransport names the request dialect the server speaks. Only
// DashScope cannot be recognized from responses, so its host decides.
func detectedTransport(baseURL string, features config.ProviderFeatures, items []modelResponseItem) string {
	if parsed, err := url.Parse(baseURL); err == nil && config.IsDashScopeHost(parsed.Hostname()) {
		return config.TransportDashScope
	}
	for _, item := range items {
		if strings.EqualFold(strings.TrimSpace(item.OwnedBy), "ninfer") {
			return config.TransportNinfer
		}
	}
	if features.LlamaProps {
		return config.TransportLlama
	}
	return config.TransportOpenAI
}
