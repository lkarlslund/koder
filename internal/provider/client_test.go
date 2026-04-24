package provider

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lkarlslund/koder/internal/attachment"
	"github.com/lkarlslund/koder/internal/config"
	"github.com/lkarlslund/koder/internal/domain"
)

func TestListModels(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"model-a","owned_by":"test"}]}`))
	}))
	defer server.Close()

	client, err := New("test", config.Provider{
		BaseURL: server.URL + "/v1",
		Timeout: time.Second,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}

	models, err := client.ListModels(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 1 || models[0].ID != "model-a" {
		t.Fatalf("unexpected models: %#v", models)
	}
}

func TestPropsUsesModelQueryAndParsesContextWindow(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/props" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if got := r.URL.Query().Get("model"); got != "model-a" {
			t.Fatalf("unexpected model query: %q", got)
		}
		_, _ = w.Write([]byte(`{"default_generation_settings":{"n_ctx":8192}}`))
	}))
	defer server.Close()

	client, err := New("llamacpp", config.Provider{
		BaseURL: server.URL,
		Timeout: time.Second,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}

	props, err := client.Props(context.Background(), "model-a")
	if err != nil {
		t.Fatal(err)
	}
	if props.DefaultGenerationSettings.NCtx != 8192 {
		t.Fatalf("unexpected props payload: %#v", props)
	}
}

func TestDetectContextWindowUsesLlamaCPPProps(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/props" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if got := r.URL.Query().Get("model"); got != "model-a" {
			t.Fatalf("unexpected model query: %q", got)
		}
		_, _ = w.Write([]byte(`{"default_generation_settings":{"n_ctx":16384}}`))
	}))
	defer server.Close()

	got, err := DetectContextWindow(context.Background(), "llamacpp", config.Provider{
		BaseURL: server.URL,
		Timeout: time.Second,
	}, "model-a", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != 16384 {
		t.Fatalf("unexpected detected context window: %d", got)
	}
}

func TestListModelsUsesV1ForLlamaCPP(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"model-a","owned_by":"llamacpp"}]}`))
	}))
	defer server.Close()

	client, err := New("llamacpp", config.Provider{
		BaseURL: server.URL,
		Timeout: time.Second,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	models, err := client.ListModels(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 1 || models[0].ID != "model-a" {
		t.Fatalf("unexpected models: %#v", models)
	}
}

func TestNewNormalizesLegacyLlamaCPPV1BaseURL(t *testing.T) {
	client, err := New("llamacpp", config.Provider{
		BaseURL: "http://127.0.0.1:8888/v1",
		Timeout: time.Second,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if client.baseURL != "http://127.0.0.1:8888" {
		t.Fatalf("expected normalized llama.cpp base url, got %q", client.baseURL)
	}
}

func TestCompleteChatReasoning(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"hello","reasoning_content":"trace"}}],"usage":{"prompt_tokens":1,"completion_tokens":2,"total_tokens":3}}`))
	}))
	defer server.Close()

	client, err := New("test", config.Provider{
		BaseURL: server.URL,
		Timeout: time.Second,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.CompleteChat(context.Background(), ChatRequest{
		Model:  "test",
		Stream: false,
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Text != "hello" || resp.Reasoning != "trace" || resp.Usage.TotalTokens != 3 {
		t.Fatalf("unexpected response: %#v", resp)
	}
}

func TestCompleteChatToolCalls(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[{"message":{"tool_calls":[{"id":"call_1","type":"function","function":{"name":"bash","arguments":"{\"command\":\"pwd\"}"}}]}}],"usage":{"total_tokens":3}}`))
	}))
	defer server.Close()

	client, err := New("test", config.Provider{
		BaseURL: server.URL,
		Timeout: time.Second,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.CompleteChat(context.Background(), ChatRequest{
		Model:  "test",
		Stream: false,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.ToolCalls) != 1 {
		t.Fatalf("expected tool call, got %#v", resp)
	}
	if resp.ToolCalls[0].ID != "call_1" || resp.ToolCalls[0].Function.Name != "bash" {
		t.Fatalf("unexpected tool call: %#v", resp.ToolCalls[0])
	}
}

func TestCompleteChatReturnsAPIErrorWithRetryAfter(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "7")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"message":"rate limit"}}`))
	}))
	defer server.Close()

	client, err := New("test", config.Provider{
		BaseURL: server.URL,
		Timeout: time.Second,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}

	_, err = client.CompleteChat(context.Background(), ChatRequest{
		Model:  "test",
		Stream: false,
	})
	if err == nil {
		t.Fatal("expected API error")
	}

	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected APIError, got %T", err)
	}
	if apiErr.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("expected 429 status, got %d", apiErr.StatusCode)
	}
	if apiErr.RetryAfter != 7*time.Second {
		t.Fatalf("expected retry-after 7s, got %s", apiErr.RetryAfter)
	}
	if !strings.Contains(apiErr.Error(), "chat status 429") {
		t.Fatalf("expected formatted error message, got %q", apiErr.Error())
	}
}

func TestMessageMarshalJSONWithContentParts(t *testing.T) {
	buf, err := json.Marshal(Message{
		Role: domain.MessageRoleUser,
		ContentParts: []ContentPart{
			TextPart("hello"),
			ImagePart("image/png", []byte("pngbytes")),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	raw := string(buf)
	if !strings.Contains(raw, `"content":[`) {
		t.Fatalf("expected multipart content, got %s", raw)
	}
	if !strings.Contains(raw, `"type":"text"`) || !strings.Contains(raw, `"type":"image_url"`) {
		t.Fatalf("expected text and image content parts, got %s", raw)
	}
	if !strings.Contains(raw, `"url":"data:image/png;base64,`) {
		t.Fatalf("expected image data URL, got %s", raw)
	}
}

func TestCapabilityStoreEnrichesAndCachesModels(t *testing.T) {
	store := NewCapabilityStore(t.TempDir())
	cfg := config.Provider{BaseURL: "https://api.openai.com/v1"}

	model, err := store.EnrichModel("openai", cfg, domain.Model{ID: "gpt-5.4"})
	if err != nil {
		t.Fatal(err)
	}
	if !model.SupportsImages || !model.CapabilitiesKnown {
		t.Fatalf("expected enriched image capability, got %#v", model)
	}

	models, err := store.EnrichModels("openai", cfg, []domain.Model{{ID: "gpt-5.4"}, {ID: "gpt-4.1-mini"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 2 || !models[0].SupportsImages || !models[1].SupportsImages {
		t.Fatalf("expected cached enriched models, got %#v", models)
	}
}

func TestCapabilityStoreSupportsAttachment(t *testing.T) {
	store := NewCapabilityStore(t.TempDir())
	cfg := config.Provider{BaseURL: "https://api.openai.com/v1"}

	ok, err := store.SupportsAttachment("openai", cfg, "gpt-5.4", attachment.KindImage)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected gpt-5.4 image support")
	}

	ok, err = store.SupportsAttachment("openai", cfg, "gpt-5.4", attachment.KindPDF)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("expected pdf support to remain disabled")
	}
}
