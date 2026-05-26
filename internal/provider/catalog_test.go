package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lkarlslund/koder/internal/config"
)

func TestBuildDraftUsesDescriptorDefaults(t *testing.T) {
	draft, err := BuildDraft("openai", nil)
	if err != nil {
		t.Fatal(err)
	}
	if draft.ProviderID != "openai" {
		t.Fatalf("unexpected provider id: %q", draft.ProviderID)
	}
	if draft.BaseURL != "https://api.openai.com/v1" {
		t.Fatalf("unexpected base url: %q", draft.BaseURL)
	}
}

func TestBuildDraftGeneratesUniqueProviderID(t *testing.T) {
	draft, err := BuildDraft("openrouter", map[string]config.Provider{
		"openrouter": {},
	})
	if err != nil {
		t.Fatal(err)
	}
	if draft.ProviderID != "openrouter-2" {
		t.Fatalf("unexpected provider id: %q", draft.ProviderID)
	}
}

func TestBuildDraftForExistingProvider(t *testing.T) {
	draft, err := BuildDraftForExisting("openrouter-work", config.Provider{
		TemplateID: "openrouter",
		Kind:       "openai-compatible",
		Name:       "OpenRouter Work",
		BaseURL:    "https://example.com/v1",
		APIKey:     "secret",
	})
	if err != nil {
		t.Fatal(err)
	}
	if draft.ProviderID != "openrouter-work" || draft.TemplateID != "openrouter" || draft.BaseURL != "https://example.com/v1" || draft.APIKey != "secret" || draft.Model == "" {
		t.Fatalf("expected existing provider values, got %#v", draft)
	}
}

func TestProbeReturnsSortedModels(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"z-model","owned_by":"test"},{"id":"a-model","owned_by":"test"}]}`))
	}))
	defer server.Close()

	result, err := Probe(context.Background(), ConnectDraft{
		ProviderID: "test",
		Kind:       ProviderKindCompatible,
		BaseURL:    server.URL + "/v1",
		Model:      "a-model",
		Headers:    map[string]string{},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Models) != 2 || result.Models[0].ID != "a-model" {
		t.Fatalf("unexpected models: %#v", result.Models)
	}
	if result.SelectedModel != "a-model" {
		t.Fatalf("expected selected model a-model, got %q", result.SelectedModel)
	}
}

func TestProbeAutoSelectsDetectedModelWhenDraftModelIsUnavailable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":[{"id":"z-model"},{"id":"a-model"}]}`))
	}))
	defer server.Close()

	result, err := Probe(context.Background(), ConnectDraft{
		ProviderID: "test",
		Kind:       ProviderKindCompatible,
		BaseURL:    server.URL + "/v1",
		Model:      "missing-model",
		Headers:    map[string]string{},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.SelectedModel != "a-model" {
		t.Fatalf("expected first detected model, got %q", result.SelectedModel)
	}
}

func TestProbeRejectsProvidersWithoutModels(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	defer server.Close()

	_, err := Probe(context.Background(), ConnectDraft{
		ProviderID: "test",
		Kind:       ProviderKindCompatible,
		BaseURL:    server.URL + "/v1",
		Model:      "missing-model",
		Headers:    map[string]string{},
	}, nil)
	if err == nil || !strings.Contains(err.Error(), "no models") {
		t.Fatalf("expected no models error, got %v", err)
	}
}

func TestConnectDraftToConfigPersistsAPIKeyWhenSet(t *testing.T) {
	cfg := ConnectDraft{
		ProviderID: "ollama",
		Kind:       ProviderKindCompatible,
		BaseURL:    "http://127.0.0.1:11434/v1",
		APIKey:     "persist-me",
		Model:      "qwen",
	}.ToConfig()
	if cfg.APIKey != "persist-me" {
		t.Fatalf("expected API key to be preserved, got %q", cfg.APIKey)
	}
}

func TestValidateDraftAllowsEmptyAPIKeyForRemoteProviders(t *testing.T) {
	err := ValidateDraft(ConnectDraft{
		ProviderID: "openai",
		Kind:       ProviderKindCompatible,
		BaseURL:    "https://api.openai.com/v1",
		Model:      "gpt-5.4",
	})
	if err != nil {
		t.Fatalf("expected draft validation to allow empty api key, got %v", err)
	}
}

func TestValidateDraftAllowsEmptyModelBeforeProbe(t *testing.T) {
	err := ValidateDraft(ConnectDraft{
		ProviderID: "openai",
		Kind:       ProviderKindCompatible,
		BaseURL:    "https://api.openai.com/v1",
	})
	if err != nil {
		t.Fatalf("expected draft validation to allow empty model before probing, got %v", err)
	}
}

func TestProbeUsesValidProviderConfig(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer secret" {
			t.Fatalf("unexpected auth header: %q", got)
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"model-a","owned_by":"test"}]}`))
	}))
	defer server.Close()

	_, err := Probe(context.Background(), ConnectDraft{
		ProviderID: "openai",
		Kind:       ProviderKindCompatible,
		BaseURL:    server.URL,
		APIKey:     "secret",
		Model:      "model-a",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
}

func TestProbeSurfacesUnauthorizedWhenAPIKeyMissing(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "" {
			t.Fatalf("expected no auth header, got %q", got)
		}
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"missing api key"}}`))
	}))
	defer server.Close()

	_, err := Probe(context.Background(), ConnectDraft{
		ProviderID: "openai",
		Kind:       ProviderKindCompatible,
		BaseURL:    server.URL,
		Model:      "model-a",
	}, nil)
	if err == nil {
		t.Fatal("expected unauthorized probe error")
	}
	if got := err.Error(); got == "" || !strings.Contains(got, "401") {
		t.Fatalf("expected unauthorized error, got %v", err)
	}
}

func TestClientStillSupportsConfigFromDraft(t *testing.T) {
	client, err := New("test", ConnectDraft{
		ProviderID: "test",
		Kind:       ProviderKindCompatible,
		BaseURL:    "https://example.com/v1",
		APIKey:     "secret",
		Model:      "model-a",
	}.ToConfig(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if client.http.Timeout != 0 {
		t.Fatalf("unexpected timeout: %v", client.http.Timeout)
	}
	transport, ok := client.http.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("expected *http.Transport, got %T", client.http.Transport)
	}
	if transport.ResponseHeaderTimeout != 10*time.Minute {
		t.Fatalf("unexpected response header timeout: %v", transport.ResponseHeaderTimeout)
	}
}
