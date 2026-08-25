package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/lkarlslund/koder/internal/config"
	"github.com/lkarlslund/koder/internal/domain"
)

func TestMetadataProbeRemembersUnsupportedEndpointsPerClient(t *testing.T) {
	var mu sync.Mutex
	requests := map[string]int{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests[r.Method+" "+r.URL.Path]++
		mu.Unlock()
		if r.URL.Path == "/v1/models" {
			_, _ = w.Write([]byte(`{"data":[{"id":"model-a"}]}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	newClient := func() *Client {
		client, err := New("local", config.Provider{Kind: ProviderKindCompatible, BaseURL: server.URL + "/v1", Timeout: time.Second}, nil)
		if err != nil {
			t.Fatal(err)
		}
		return client
	}
	client := newClient()
	for range 2 {
		if _, err := client.DetectModelMetadata(context.Background(), "model-a"); err != nil {
			t.Fatal(err)
		}
	}

	mu.Lock()
	firstRequests := make(map[string]int, len(requests))
	for key, count := range requests {
		firstRequests[key] = count
	}
	mu.Unlock()
	for _, key := range []string{"GET /api/v1/models", "GET /props", "GET /api/v0/models", "POST /api/show"} {
		if firstRequests[key] != 1 {
			t.Fatalf("%s called %d times on one client, want 1", key, firstRequests[key])
		}
	}
	if firstRequests["GET /v1/models"] != 2 {
		t.Fatalf("required model list called %d times, want 2", firstRequests["GET /v1/models"])
	}

	if _, err := newClient().DetectModelMetadata(context.Background(), "model-a"); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	for _, key := range []string{"GET /api/v1/models", "GET /props", "GET /api/v0/models", "POST /api/show"} {
		if requests[key] != 2 {
			t.Fatalf("%s called %d times after reconnect, want 2", key, requests[key])
		}
	}
}

func TestListModelsParsesCompatibleMetadataSuperset(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"model-a","owned_by":"vendor","context_length":131072,"top_provider":{"context_length":65536,"max_completion_tokens":8192},"architecture":{"input_modalities":["text","image"],"output_modalities":["text"]},"supported_parameters":["tools","structured_outputs","reasoning"]}]}`))
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
	if !model.SupportsChat || !model.SupportsImages || !model.SupportsTools || !model.SupportsJSON || !model.SupportsReasoning || !model.CapabilitiesKnown {
		t.Fatalf("unexpected capabilities: %#v", model)
	}
}

func TestListModelsEnrichesSparseCatalogFromNativeMetadata(t *testing.T) {
	var nativeCalls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			_, _ = w.Write([]byte(`{"data":[{"id":"model-a"},{"id":"embed-a"}]}`))
		case "/api/v1/models":
			nativeCalls++
			_, _ = w.Write([]byte(`{"models":[{"key":"model-a","type":"llm","publisher":"vendor","max_context_length":131072,"loaded_instances":[{"id":"model-a","config":{"context_length":32768}}],"capabilities":{"vision":true,"trained_for_tool_use":true,"reasoning":{"allowed_options":["low","high"],"default":"high"}}},{"key":"embed-a","type":"embedding","publisher":"vendor","max_context_length":8192,"capabilities":{"vision":false,"trained_for_tool_use":false,"reasoning":null}}]}`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
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
	if nativeCalls != 1 || len(models) != 2 {
		t.Fatalf("native calls = %d, models = %#v", nativeCalls, models)
	}
	chat := models[0]
	if chat.OwnedBy != "vendor" || chat.ContextWindow != 32768 || chat.MaxContextWindow != 131072 || !chat.SupportsChat || !chat.ChatKnown || !chat.SupportsImages || !chat.SupportsTools || !chat.SupportsReasoning {
		t.Fatalf("chat metadata = %#v", chat)
	}
	if len(chat.ReasoningEfforts) != 2 || chat.ReasoningEfforts[0] != "low" || chat.ReasoningEfforts[1] != "high" || chat.DefaultReasoningEffort != "high" {
		t.Fatalf("reasoning metadata = %#v", chat)
	}
	embedding := models[1]
	if embedding.SupportsChat || !embedding.ChatKnown || embedding.ContextWindow != 8192 || embedding.OwnedBy != "vendor" {
		t.Fatalf("embedding metadata = %#v", embedding)
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

	client, err := New("local", config.Provider{Kind: ProviderKindCompatible, BaseURL: server.URL + "/v1", Timeout: time.Second}, nil)
	if err != nil {
		t.Fatal(err)
	}
	model, err := client.DetectModelMetadata(context.Background(), "model-a")
	if err != nil {
		t.Fatal(err)
	}
	if model.ContextWindow != 32768 || model.MaxContextWindow != 131072 || model.MetadataSource != "native-v1-loaded-instance" {
		t.Fatalf("unexpected LM Studio metadata: %#v", model)
	}
	if !model.SupportsImages || !model.SupportsTools || !model.SupportsReasoning || !model.CapabilitiesKnown {
		t.Fatalf("unexpected LM Studio capabilities: %#v", model)
	}
}

func TestDetectModelMetadataUsesSingleLoadedInstanceForModelKey(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			_, _ = w.Write([]byte(`{"data":[{"id":"publisher/model","max_context_length":131072}]}`))
		case "/api/v1/models":
			_, _ = w.Write([]byte(`{"models":[{"key":"publisher/model","max_context_length":131072,"loaded_instances":[{"id":"custom-instance","config":{"context_length":49152}}]}]}`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	client, err := New("local", config.Provider{Kind: ProviderKindCompatible, BaseURL: server.URL + "/v1", Timeout: time.Second}, nil)
	if err != nil {
		t.Fatal(err)
	}
	model, err := client.DetectModelMetadata(context.Background(), "publisher/model")
	if err != nil {
		t.Fatal(err)
	}
	if model.ContextWindow != 49152 || model.MaxContextWindow != 131072 || model.MetadataSource != "native-v1-loaded-instance" {
		t.Fatalf("unexpected native v1 metadata: %#v", model)
	}
}

func TestDetectModelMetadataUsesNativeV0Maximum(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			_, _ = w.Write([]byte(`{"data":[{"id":"model-a"}]}`))
		case "/api/v1/models", "/props":
			http.NotFound(w, r)
		case "/api/v0/models":
			_, _ = w.Write([]byte(`{"data":[{"id":"model-a","type":"embedding","publisher":"vendor","max_context_length":98304}]}`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	client, err := New("local", config.Provider{Kind: ProviderKindCompatible, BaseURL: server.URL + "/v1", Timeout: time.Second}, nil)
	if err != nil {
		t.Fatal(err)
	}
	model, err := client.DetectModelMetadata(context.Background(), "model-a")
	if err != nil {
		t.Fatal(err)
	}
	if model.ContextWindow != 98304 || model.MaxContextWindow != 98304 || model.MetadataSource != "compatible-v0-models" {
		t.Fatalf("unexpected native v0 metadata: %#v", model)
	}
	if model.SupportsChat || !model.ChatKnown || model.OwnedBy != "vendor" {
		t.Fatalf("unexpected native v0 identity: %#v", model)
	}
}

func TestDetectModelMetadataUsesOllamaShow(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			_, _ = w.Write([]byte(`{"data":[{"id":"gemma3"}]}`))
		case "/props", "/api/v1/models", "/api/v0/models":
			http.NotFound(w, r)
		case "/api/show":
			_, _ = w.Write([]byte(`{"parameters":"temperature 0.7\nnum_ctx 8192","capabilities":["completion","vision","tools"],"model_info":{"gemma3.context_length":131072}}`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	client, err := New("local", config.Provider{Kind: ProviderKindCompatible, BaseURL: server.URL + "/v1", Timeout: time.Second}, nil)
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

func TestModelFromPropsUsesTopLevelRuntimeFacts(t *testing.T) {
	vision := true
	props := propsResponse{NCtx: 49152, ChatTemplateToolUse: json.RawMessage(`"tool template"`)}
	props.Modalities.Vision = &vision
	model := modelFromProps("model-a", props)
	if model.ContextWindow != 49152 || model.MetadataSource != "llama.cpp-props" || !model.SupportsImages || !model.ImagesKnown || !model.SupportsTools || !model.ToolsKnown || !model.CapabilitiesKnown {
		t.Fatalf("props metadata = %#v", model)
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

func TestCapabilityStorePrefersLiveNativeMetadataOverCachedProbe(t *testing.T) {
	store := NewCapabilityStore(t.TempDir())
	cfg := config.Provider{BaseURL: "http://local.example/v1"}
	key := capabilityKey("local", cfg.BaseURL, "model-a")
	if err := store.save(capabilityFile{Entries: map[string]capabilityEntry{
		key: {
			ProviderID: "local", BaseURL: cfg.BaseURL, ModelID: "model-a",
			SupportsChat: true, ChatKnown: true, SupportsTools: false, ToolsKnown: true,
			CapabilitySource: "probe", CapabilitiesKnown: true, DetectedAt: time.Now(),
		},
	}}); err != nil {
		t.Fatal(err)
	}
	model, err := store.EnrichModel("local", cfg, domain.Model{
		ID: "model-a", SupportsChat: true, ChatKnown: true, SupportsTools: true, ToolsKnown: true,
		CapabilitySource: "native-v1-models", CapabilitiesKnown: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !model.SupportsTools || model.CapabilitySource != "native-v1-models" {
		t.Fatalf("live metadata was overwritten: %#v", model)
	}
}
