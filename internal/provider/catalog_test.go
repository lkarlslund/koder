package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lkarlslund/koder/internal/config"
)

func TestBuildDraftForExistingProvider(t *testing.T) {
	draft := BuildDraftForExisting("work", config.Provider{
		Name:    "Work",
		BaseURL: "https://example.com/v1",
		APIKey:  "secret",
	})
	if draft.ProviderID != "work" || draft.OriginalProviderID != "work" || draft.BaseURL != "https://example.com/v1" || draft.APIKey != "secret" {
		t.Fatalf("expected existing provider values, got %#v", draft)
	}
}

// Probing decides the transport from what the server takes: a server that
// accepts chat template arguments is sent them, one that rejects them as
// unknown (as hosted APIs do) is not.
func TestProbeFindsChatTemplateArguments(t *testing.T) {
	for _, accepts := range []bool{true, false} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/v1/models":
				_, _ = w.Write([]byte(`{"data":[{"id":"model-a"}]}`))
			case "/v1/chat/completions":
				var body map[string]any
				_ = json.NewDecoder(r.Body).Decode(&body)
				if _, ok := body["chat_template_kwargs"]; ok && !accepts {
					http.Error(w, `{"error":{"message":"Unrecognized request argument supplied: chat_template_kwargs"}}`, http.StatusBadRequest)
					return
				}
				if body["stream"] == true {
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"p\"}}]}\n\ndata: [DONE]\n\n"))
					return
				}
				_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"pong"}}]}`))
			default:
				http.NotFound(w, r)
			}
		}))
		result, err := Probe(context.Background(), ConnectDraft{ProviderID: "test", BaseURL: server.URL + "/v1", Headers: map[string]string{}}, nil)
		server.Close()
		if err != nil {
			t.Fatal(err)
		}
		want := config.TransportOpenAI
		if accepts {
			want = config.TransportLlama
		}
		if result.Features.TemplateKwargs != accepts || result.Transport != want {
			t.Fatalf("accepts=%v: kwargs=%v transport=%q", accepts, result.Features.TemplateKwargs, result.Transport)
		}
		// A stream without progress chunks does not count as prompt progress.
		if result.PromptProgressSupported {
			t.Fatalf("accepts=%v: prompt progress reported without progress chunks", accepts)
		}
	}
}

func TestProbeReturnsSortedModels(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/chat/completions" {
			http.Error(w, "return_progress is not supported", http.StatusBadRequest)
			return
		}
		if r.URL.Path == "/slots" || r.URL.Path == "/props" {
			http.NotFound(w, r)
			return
		}
		if r.URL.Path != "/v1/models" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"z-model","owned_by":"test"},{"id":"a-model","owned_by":"test"}]}`))
	}))
	defer server.Close()

	result, err := Probe(context.Background(), ConnectDraft{
		ProviderID: "test",
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
	if !result.PromptProgressProbed || result.PromptProgressSupported {
		t.Fatalf("expected rejected prompt progress probe, got %#v", result)
	}
	if result.PromptProgressCheckedAt.IsZero() || result.PromptProgressTarget == "" {
		t.Fatalf("expected timestamped prompt progress observation, got %#v", result)
	}
}

func TestProbeAutoSelectsDetectedModelWhenDraftModelIsUnavailable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		if r.URL.Path == "/slots" || r.URL.Path == "/props" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"z-model"},{"id":"a-model"}]}`))
	}))
	defer server.Close()

	result, err := Probe(context.Background(), ConnectDraft{
		ProviderID: "test",
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

func TestProbeDetectsPromptProgressSupport(t *testing.T) {
	var sawReturnProgress bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			_, _ = w.Write([]byte(`{"data":[{"id":"model-a"}]}`))
		case "/v1/chat/completions":
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode chat probe body: %v", err)
			}
			sawReturnProgress, _ = body["return_progress"].(bool)
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("data: {\"prompt_progress\":{\"total\":10,\"processed\":5,\"cache\":2,\"time_ms\":3}}\n\n"))
			_, _ = w.Write([]byte("data: [DONE]\n\n"))
		case "/slots", "/props", "/api/v1/models", "/api/v0/models", "/v1/systemone", "/v1/audio/speech", "/v1/audio/transcriptions":
			http.NotFound(w, r)
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	result, err := Probe(context.Background(), ConnectDraft{
		ProviderID: "test",
		BaseURL:    server.URL + "/v1",
		Model:      "model-a",
		Headers:    map[string]string{},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !sawReturnProgress {
		t.Fatal("expected probe request to include return_progress")
	}
	if !result.PromptProgressProbed || !result.PromptProgressSupported {
		t.Fatalf("expected supported prompt progress probe, got %#v", result)
	}
	if result.PromptProgressCheckedAt.IsZero() || result.PromptProgressTarget == "" {
		t.Fatalf("expected timestamped prompt progress observation, got %#v", result)
	}
}

func TestProbeRejectsProvidersWithoutModels(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	defer server.Close()

	_, err := Probe(context.Background(), ConnectDraft{
		ProviderID: "test",
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
		BaseURL:    "https://api.openai.com/v1",
	})
	if err != nil {
		t.Fatalf("expected draft validation to allow empty model before probing, got %v", err)
	}
}

func TestProbeUsesValidProviderConfig(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/chat/completions" {
			http.NotFound(w, r)
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer secret" {
			t.Fatalf("unexpected auth header: %q", got)
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"model-a","owned_by":"test"}]}`))
	}))
	defer server.Close()

	_, err := Probe(context.Background(), ConnectDraft{
		ProviderID: "openai",
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
