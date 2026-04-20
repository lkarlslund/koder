package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lkarlslund/koder/internal/config"
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
