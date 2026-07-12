package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lkarlslund/koder/internal/config"
	"github.com/lkarlslund/koder/internal/domain"
)

func TestListModelsParsesCompatibleMetadataSuperset(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"model-a","owned_by":"vendor","context_length":131072,"top_provider":{"context_length":65536,"max_completion_tokens":8192},"architecture":{"input_modalities":["text","image"]},"supported_parameters":["tools","structured_outputs","reasoning"]}]}`))
	}))
	defer server.Close()

	client, err := New("compatible", config.Provider{Kind: ProviderKindCompatible, BaseURL: server.URL + "/v1", Timeout: time.Second}, nil)
	if err != nil {
		t.Fatal(err)
	}
	models, err := client.ListModels(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 1 {
		t.Fatalf("expected one model, got %#v", models)
	}
	model := models[0]
	if model.ContextWindow != 65536 || model.MaxContextWindow != 65536 || model.MaxOutputTokens != 8192 {
		t.Fatalf("unexpected limits: %#v", model)
	}
	if !model.SupportsImages || !model.SupportsTools || !model.SupportsJSON || !model.SupportsReasoning || !model.CapabilitiesKnown {
		t.Fatalf("unexpected capabilities: %#v", model)
	}
}

func TestDetectModelMetadataUsesLMStudioLoadedContext(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			_, _ = w.Write([]byte(`{"data":[{"id":"model-a"}]}`))
		case "/props":
			http.NotFound(w, r)
		case "/api/v1/models":
			_, _ = w.Write([]byte(`{"models":[{"key":"model-a","max_context_length":131072,"loaded_instances":[{"id":"model-a","config":{"context_length":32768}}],"capabilities":{"vision":true,"trained_for_tool_use":true,"reasoning":{"allowed_options":["on"]}}}]}`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	client, err := New("lmstudio", config.Provider{Kind: ProviderKindCompatible, BaseURL: server.URL + "/v1", Timeout: time.Second}, nil)
	if err != nil {
		t.Fatal(err)
	}
	model, err := client.DetectModelMetadata(context.Background(), "model-a")
	if err != nil {
		t.Fatal(err)
	}
	if model.ContextWindow != 32768 || model.MaxContextWindow != 131072 || model.MetadataSource != "lmstudio-loaded-instance" {
		t.Fatalf("unexpected LM Studio metadata: %#v", model)
	}
	if !model.SupportsImages || !model.SupportsTools || !model.SupportsReasoning || !model.CapabilitiesKnown {
		t.Fatalf("unexpected LM Studio capabilities: %#v", model)
	}
}

func TestDetectModelMetadataUsesOllamaShow(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			_, _ = w.Write([]byte(`{"data":[{"id":"gemma3"}]}`))
		case "/props", "/api/v1/models":
			http.NotFound(w, r)
		case "/api/show":
			_, _ = w.Write([]byte(`{"parameters":"temperature 0.7\nnum_ctx 8192","capabilities":["completion","vision","tools"],"model_info":{"gemma3.context_length":131072}}`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	client, err := New("ollama", config.Provider{Kind: ProviderKindCompatible, BaseURL: server.URL + "/v1", Timeout: time.Second}, nil)
	if err != nil {
		t.Fatal(err)
	}
	model, err := client.DetectModelMetadata(context.Background(), "gemma3")
	if err != nil {
		t.Fatal(err)
	}
	if model.ContextWindow != 8192 || model.MaxContextWindow != 131072 || model.MetadataSource != "ollama-show" {
		t.Fatalf("unexpected Ollama metadata: %#v", model)
	}
	if !model.SupportsImages || !model.SupportsTools || !model.CapabilitiesKnown {
		t.Fatalf("unexpected Ollama capabilities: %#v", model)
	}
}

func TestCapabilityStorePreservesExplicitModelMetadata(t *testing.T) {
	store := NewCapabilityStore(t.TempDir())
	model, err := store.EnrichModel("compatible", config.Provider{}, domain.Model{
		ID:                "model-a",
		SupportsImages:    true,
		SupportsTools:     true,
		CapabilitiesKnown: true,
		CapabilitySource:  "openai-models",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !model.SupportsImages || !model.SupportsTools || !model.CapabilitiesKnown || model.CapabilitySource != "openai-models" {
		t.Fatalf("explicit metadata was discarded: %#v", model)
	}
}
