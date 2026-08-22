package embedding

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
)

func TestOpenAICompatibleIsLazyAndRestoresResponseOrder(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		calls.Add(1)
		if request.URL.Path != "/v1/embeddings" || request.Method != http.MethodPost {
			t.Errorf("request = %s %s", request.Method, request.URL.Path)
		}
		if request.Header.Get("Authorization") != "Bearer test-token" {
			t.Errorf("Authorization = %q", request.Header.Get("Authorization"))
		}
		var body embeddingRequest
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
		}
		if body.Model != "embed-v1" || body.Dimensions != 2 || !slices.Equal(body.Input, []string{"first", "second"}) {
			t.Errorf("request body = %#v", body)
		}
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{"data":[{"index":1,"embedding":[0,1]},{"index":0,"embedding":[1,0]}]}`))
	}))
	defer server.Close()

	backend, err := NewOpenAICompatible(OpenAICompatibleConfig{
		ProviderID: "test", BaseURL: server.URL + "/v1/", APIKey: "test-token",
		ModelID: "embed-v1", Dimensions: 2, SendDimensions: true,
	})
	if err != nil {
		t.Fatalf("NewOpenAICompatible() error = %v", err)
	}
	if calls.Load() != 0 {
		t.Fatalf("constructor made %d HTTP requests", calls.Load())
	}
	vectors, err := backend.Embed(context.Background(), []string{"first", "second"})
	if err != nil {
		t.Fatalf("Embed() error = %v", err)
	}
	if calls.Load() != 1 || !slices.Equal(vectors[0], []float32{1, 0}) || !slices.Equal(vectors[1], []float32{0, 1}) {
		t.Fatalf("Embed() = %#v; calls = %d", vectors, calls.Load())
	}
}

func TestOpenAICompatibleRejectsBadEndpointResponsesWithoutEchoingInput(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.WriteHeader(http.StatusBadGateway)
		_, _ = response.Write([]byte(`{"error":"request contained private-query-marker"}`))
	}))
	defer server.Close()
	backend, err := NewOpenAICompatible(OpenAICompatibleConfig{
		ProviderID: "test", BaseURL: server.URL + "/v1", ModelID: "embed-v1", Dimensions: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = backend.Embed(context.Background(), []string{"private-query-marker"})
	if err == nil {
		t.Fatal("Embed() unexpectedly succeeded")
	}
	if contains := strings.Contains(err.Error(), "private-query-marker"); contains {
		t.Fatalf("Embed() error leaked input: %v", err)
	}
}
