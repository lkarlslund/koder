package provider

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lkarlslund/koder/internal/attachment"
	"github.com/lkarlslund/koder/internal/config"
	"github.com/lkarlslund/koder/internal/debugsrv"
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

func TestSerializePromptEnvelopeUsesSingleLeadingSystemMessage(t *testing.T) {
	env := PromptEnvelope{
		Instructions: []InstructionBlock{
			{Kind: InstructionKindBaseSystem, Text: "Base prompt"},
			{Kind: InstructionKindProjectInstructions, Text: "Project instructions"},
			{Kind: InstructionKindSkills, Text: "Skills"},
		},
		Items: []Message{
			{Role: RoleUser, Content: "hello"},
		},
	}

	got := SerializePromptEnvelope(env)
	if len(got) != 2 {
		t.Fatalf("expected system message and user item, got %#v", got)
	}
	if got[0].Role != RoleSystem {
		t.Fatalf("expected leading system message, got %#v", got)
	}
	if strings.Contains(got[0].Content, "\n\n\n") {
		t.Fatalf("expected normalized system join, got %q", got[0].Content)
	}
	for _, want := range []string{"Base prompt", "Project instructions", "Skills"} {
		if !strings.Contains(got[0].Content, want) {
			t.Fatalf("expected %q in joined system prompt, got %q", want, got[0].Content)
		}
	}
	if got[1].Role != RoleUser || got[1].Content != "hello" {
		t.Fatalf("unexpected trailing item: %#v", got[1])
	}
}

func TestMessageMarshalJSONOmitsEmptyAssistantToolCallContent(t *testing.T) {
	data, err := json.Marshal(Message{
		Role: RoleAssistant,
		ToolCalls: []ToolCall{
			{
				ID:   "call_1",
				Type: "function",
				Function: FunctionCall{
					Name:      "read",
					Arguments: "{\"path\":\".\"}",
				},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	if strings.Contains(got, `"content":""`) {
		t.Fatalf("expected empty content to be omitted, got %s", got)
	}
	if !strings.Contains(got, `"tool_calls"`) {
		t.Fatalf("expected tool calls to be preserved, got %s", got)
	}
}

func TestChatRequestMarshalJSONIncludesStreamUsageOptions(t *testing.T) {
	data, err := json.Marshal(ChatRequest{
		Model:  "test-model",
		Stream: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	if !strings.Contains(got, `"stream_options":{"include_usage":true}`) {
		t.Fatalf("expected streamed requests to ask for usage, got %s", got)
	}
}

func TestChatRequestMarshalJSONUsesProviderRoleNames(t *testing.T) {
	data, err := json.Marshal(ChatRequest{
		Model: "test-model",
		Messages: []Message{
			{Role: RoleSystem, Content: "system"},
			{Role: RoleUser, Content: "user"},
			{Role: RoleAssistant, Content: "assistant"},
			{Role: RoleTool, ToolCallID: "call_1", Content: "tool"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	for _, role := range []string{"system", "user", "assistant", "tool"} {
		if !strings.Contains(got, `"role":"`+role+`"`) {
			t.Fatalf("expected lowercase provider role %q, got %s", role, got)
		}
	}
	for _, role := range []string{"System", "User", "Assistant", "Tool"} {
		if strings.Contains(got, `"role":"`+role+`"`) {
			t.Fatalf("unexpected domain enum role %q in provider request: %s", role, got)
		}
	}
}

func TestChatRequestMarshalJSONIncludesExtraBody(t *testing.T) {
	data, err := json.Marshal(ChatRequest{
		Model:  "test-model",
		Stream: true,
		ExtraBody: map[string]any{
			"return_progress": true,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	if !strings.Contains(got, `"return_progress":true`) {
		t.Fatalf("expected extra body fields in request, got %s", got)
	}
}

func TestChatRequestMarshalJSONOmitsInternalIDs(t *testing.T) {
	data, err := json.Marshal(ChatRequest{
		SessionID: "session-a",
		ChatID:    "chat-a",
		Model:     "test-model",
		Messages:  []Message{{Role: RoleUser, Content: "hello"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	if strings.Contains(got, "session-a") || strings.Contains(got, "chat-a") {
		t.Fatalf("internal ids leaked into provider request: %s", got)
	}
}

func TestChatRequestMarshalJSONStable(t *testing.T) {
	req := ChatRequest{
		Model:    "test-model",
		Messages: []Message{{Role: RoleUser, Content: "hello"}},
		ExtraBody: map[string]any{
			"return_progress": true,
			"id_slot":         2,
			"cache_prompt":    true,
		},
	}
	first, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10; i++ {
		next, err := json.Marshal(req)
		if err != nil {
			t.Fatal(err)
		}
		if string(next) != string(first) {
			t.Fatalf("request JSON changed between marshals:\nfirst=%s\nnext=%s", first, next)
		}
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

	client, err := New("openai-compatible", config.Provider{
		Kind:    ProviderKindCompatible,
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

func TestDetectContextWindowUsesCompatibleLocalProps(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/models":
			_, _ = w.Write([]byte(`{"data":[{"id":"model-a","max_model_len":16384}]}`))
		case "/props":
			if got := r.URL.Query().Get("model"); got != "model-a" {
				t.Fatalf("unexpected model query: %q", got)
			}
			_, _ = w.Write([]byte(`{"default_generation_settings":{"n_ctx":8192}}`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	got, err := DetectContextWindow(context.Background(), "openai-compatible", config.Provider{
		Kind:    ProviderKindCompatible,
		BaseURL: server.URL,
		Timeout: time.Second,
	}, "model-a", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != 8192 {
		t.Fatalf("unexpected detected context window: %d", got)
	}
}

func TestDetectLlamaSlotsUsesSlotsEndpoint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/slots":
			if got := r.URL.Query().Get("model"); got != "model-a" {
				t.Fatalf("unexpected model query: %q", got)
			}
			_, _ = w.Write([]byte(`[{"id":0},{"id":1},{"id":2}]`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	got, err := DetectLlamaSlots(context.Background(), "openai-compatible", config.Provider{
		Kind:    ProviderKindCompatible,
		BaseURL: server.URL + "/v1",
		Timeout: time.Second,
	}, "model-a", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != 3 {
		t.Fatalf("unexpected detected slot count: %d", got)
	}
}

func TestDetectLlamaSlotsFallsBackToPropsMaxInstances(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/slots":
			http.NotFound(w, r)
		case "/props":
			if got := r.URL.Query().Get("model"); got != "model-a" {
				t.Fatalf("unexpected model query: %q", got)
			}
			_, _ = w.Write([]byte(`{"max_instances":2,"default_generation_settings":{"n_ctx":8192}}`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	got, err := DetectLlamaSlots(context.Background(), "openai-compatible", config.Provider{
		Kind:    ProviderKindCompatible,
		BaseURL: server.URL + "/v1",
		Timeout: time.Second,
	}, "model-a", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != 2 {
		t.Fatalf("unexpected detected slot count: %d", got)
	}
}

func TestDetectContextWindowUsesCompatibleModelStatusArgs(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/props":
			http.NotFound(w, r)
		case "/models":
			_, _ = w.Write([]byte(`{"data":[{"id":"model-a","status":{"args":["llama-server","--ctx-size","262144"]}}]}`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	got, err := DetectContextWindow(context.Background(), "openai-compatible", config.Provider{
		Kind:    ProviderKindCompatible,
		BaseURL: server.URL,
		Timeout: time.Second,
	}, "model-a", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != 262144 {
		t.Fatalf("unexpected detected context window: %d", got)
	}
}

func TestDetectContextWindowUsesCompatibleModelStatusPreset(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/props":
			http.NotFound(w, r)
		case "/models":
			_, _ = w.Write([]byte(`{"data":[{"id":"model-a","status":{"preset":"ctx-size = 131072\n"}}]}`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	got, err := DetectContextWindow(context.Background(), "openai-compatible", config.Provider{
		Kind:    ProviderKindCompatible,
		BaseURL: server.URL,
		Timeout: time.Second,
	}, "model-a", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != 131072 {
		t.Fatalf("unexpected detected context window: %d", got)
	}
}

func TestDetectContextWindowUsesNativePropsFromConfiguredV1Base(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/models":
			_, _ = w.Write([]byte(`{"data":[{"id":"model-a","max_model_len":65536}]}`))
		case "/props":
			if got := r.URL.Query().Get("model"); got != "model-a" {
				t.Fatalf("unexpected model query: %q", got)
			}
			_, _ = w.Write([]byte(`{"default_generation_settings":{"n_ctx":2048}}`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	got, err := DetectContextWindow(context.Background(), "openai-compatible", config.Provider{
		Kind:    ProviderKindCompatible,
		BaseURL: server.URL + "/v1",
		Timeout: time.Second,
	}, "model-a", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != 2048 {
		t.Fatalf("unexpected detected context window: %d", got)
	}
}

func TestDetectContextWindowPrefersEffectivePropsOverModelArgs(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/props":
			if got := r.URL.Query().Get("model"); got != "model-a" {
				t.Fatalf("unexpected model query: %q", got)
			}
			_, _ = w.Write([]byte(`{"default_generation_settings":{"n_ctx":65536}}`))
		case "/models":
			_, _ = w.Write([]byte(`{"data":[{"id":"model-a","status":{"args":["llama-server","--ctx-size","131072"]}}]}`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	got, err := DetectContextWindow(context.Background(), "openai-compatible", config.Provider{
		Kind:    ProviderKindCompatible,
		BaseURL: server.URL + "/v1",
		Timeout: time.Second,
	}, "model-a", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != 65536 {
		t.Fatalf("unexpected detected context window: %d", got)
	}
}

func TestDetectContextWindowFallsBackToPropsWhenModelListLacksContext(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/models":
			_, _ = w.Write([]byte(`{"data":[{"id":"model-a"}]}`))
		case "/props":
			if got := r.URL.Query().Get("model"); got != "model-a" {
				t.Fatalf("unexpected model query: %q", got)
			}
			_, _ = w.Write([]byte(`{"default_generation_settings":{"n_ctx":49152}}`))
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	got, err := DetectContextWindow(context.Background(), "openai-compatible", config.Provider{
		Kind:    ProviderKindCompatible,
		BaseURL: server.URL + "/v1",
		Timeout: time.Second,
	}, "model-a", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != 49152 {
		t.Fatalf("unexpected detected context window: %d", got)
	}
}

func TestDetectContextWindowFallsBackWhenCompatibleEndpointHasNoProps(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer server.Close()

	got, err := DetectContextWindow(context.Background(), "openai-compatible", config.Provider{
		Kind:    ProviderKindCompatible,
		BaseURL: server.URL + "/v1",
		Timeout: time.Second,
	}, "model-a", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != 32768 {
		t.Fatalf("unexpected fallback context window: %d", got)
	}
}

func TestSupportsContextWindowDetectionUsesCompatibleAPIKeyProvider(t *testing.T) {
	cfg := config.Provider{
		Kind:    ProviderKindCompatible,
		BaseURL: "https://api.example.com/v1",
	}

	if !SupportsContextWindowDetection(cfg) {
		t.Fatal("expected compatible api-key provider to support context window detection")
	}
}

func TestListModelsUsesConfiguredV1BaseURL(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"model-a","owned_by":"compatible"}]}`))
	}))
	defer server.Close()

	client, err := New("openai-compatible", config.Provider{
		Kind:    ProviderKindCompatible,
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

func TestCompleteChatReasoningFallbackField(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"hello","reasoning":"trace"}}],"usage":{"prompt_tokens":1,"completion_tokens":2,"total_tokens":3}}`))
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

func TestCompleteChatParsesCachedTokens(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"hello"}}],"usage":{"prompt_tokens":10,"completion_tokens":2,"total_tokens":12,"prompt_tokens_details":{"cached_tokens":6}}}`))
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
	if resp.Usage.CachedTokens != 6 {
		t.Fatalf("expected cached tokens, got %#v", resp.Usage)
	}
}

func TestCompleteChatSynthesizesTotalTokensWhenMissing(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"hello"}}],"usage":{"prompt_tokens":10,"completion_tokens":2}}`))
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
	if resp.Usage.TotalTokens != 12 {
		t.Fatalf("expected synthesized total tokens, got %#v", resp.Usage)
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

func TestStreamChatReasoningFallbackField(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"reasoning\":\"trace\"}}]}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer server.Close()

	client, err := New("test", config.Provider{
		BaseURL: server.URL,
		Timeout: time.Second,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	events, err := client.StreamChat(context.Background(), ChatRequest{Model: "test"})
	if err != nil {
		t.Fatal(err)
	}
	var gotReasoning string
	for event := range events {
		if event.Kind == domain.EventKindReasoning {
			gotReasoning = event.Text
		}
		if event.Err != nil {
			t.Fatal(event.Err)
		}
	}
	if gotReasoning != "trace" {
		t.Fatalf("expected reasoning event, got %q", gotReasoning)
	}
}

func TestStreamChatResponseAggregatesToolCallsAndDeltas(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"hel\",\"reasoning\":\"trace-\",\"tool_calls\":[{\"id\":\"call_1\",\"type\":\"function\",\"function\":{\"name\":\"bash\",\"arguments\":\"{\\\"command\\\":\\\"pri\"}}]}}]}\n\n"))
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"lo\",\"reasoning\":\"ace\",\"tool_calls\":[{\"id\":\"call_1\",\"type\":\"function\",\"function\":{\"arguments\":\"ntf hello\\\"}\"}}]}}],\"usage\":{\"total_tokens\":3}}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer server.Close()

	client, err := New("test", config.Provider{
		BaseURL: server.URL,
		Timeout: time.Second,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var deltas []string
	var sawDone bool
	var toolDeltas []domain.Event
	resp, err := client.StreamChatResponse(context.Background(), ChatRequest{Model: "test"}, func(evt domain.Event) {
		switch evt.Kind {
		case domain.EventKindMessageDelta:
			deltas = append(deltas, evt.Text)
		case domain.EventKindToolCallDelta:
			toolDeltas = append(toolDeltas, evt)
			if !strings.Contains(evt.RawJSON, "\"tool_calls\"") {
				t.Fatalf("expected raw tool call payload, got %q", evt.RawJSON)
			}
		case domain.EventKindMessageDone:
			sawDone = true
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(deltas, "") != "hello" {
		t.Fatalf("expected streamed deltas to form hello, got %#v", deltas)
	}
	if resp.Text != "hello" {
		t.Fatalf("expected aggregated text hello, got %#v", resp)
	}
	if resp.Reasoning != "trace-ace" {
		t.Fatalf("expected aggregated reasoning, got %#v", resp)
	}
	if len(resp.ToolCalls) != 1 {
		t.Fatalf("expected one aggregated tool call, got %#v", resp.ToolCalls)
	}
	if resp.ToolCalls[0].Function.Name != "bash" || resp.ToolCalls[0].Function.Arguments != "{\"command\":\"printf hello\"}" {
		t.Fatalf("unexpected aggregated tool call: %#v", resp.ToolCalls[0])
	}
	if resp.Usage.TotalTokens != 3 {
		t.Fatalf("expected usage tokens, got %#v", resp.Usage)
	}
	if !sawDone {
		t.Fatal("expected message done event")
	}
	if len(toolDeltas) != 2 {
		t.Fatal("expected streamed tool call delta event")
	}
	if toolDeltas[0].Tool != domain.ToolKindBash || toolDeltas[0].ToolCallID != "call_1" || !strings.Contains(toolDeltas[0].Meta["arguments"], "pri") {
		t.Fatalf("expected first streamed tool call details, got %#v", toolDeltas[0])
	}
	if toolDeltas[1].Tool != domain.ToolKindBash || toolDeltas[1].ToolCallID != "call_1" || !strings.Contains(toolDeltas[1].Meta["arguments"], "printf hello") {
		t.Fatalf("expected accumulated streamed tool call details, got %#v", toolDeltas[1])
	}
}

func TestStreamChatResponseStopsOnToolArgumentLimit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"id\":\"call_read\",\"type\":\"function\",\"index\":0,\"function\":{\"name\":\"file_read\",\"arguments\":\"{\\\"path\\\":\\\"go.mod\\\"}\"}}]}}]}\n\n"))
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"id\":\"call_write\",\"type\":\"function\",\"index\":1,\"function\":{\"name\":\"file_write\",\"arguments\":\"{\\\"path\\\":\\\"big.go\\\",\\\"content\\\":\\\"1234567890\"}}]}}]}\n\n"))
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":1,\"function\":{\"arguments\":\"1234567890\\\"}\"}}]}}]}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer server.Close()

	client, err := New("test", config.Provider{
		BaseURL: server.URL,
		Timeout: time.Second,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var sawStatus, sawDone bool
	resp, err := client.StreamChatResponse(context.Background(), ChatRequest{
		Model: "test",
		ToolArgumentLimits: map[string]int{
			"file_write": 30,
		},
	}, func(evt domain.Event) {
		if evt.Kind == domain.EventKindStatus && strings.Contains(evt.Text, "file_write tool arguments exceeded 30 bytes") {
			sawStatus = true
		}
		if evt.Kind == domain.EventKindMessageDone {
			sawDone = true
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.ToolCalls) != 1 || resp.ToolCalls[0].ID != "call_read" {
		t.Fatalf("expected only completed sibling call to remain, got %#v", resp.ToolCalls)
	}
	if len(resp.ToolCallErrors) != 1 {
		t.Fatalf("expected oversized call error, got %#v", resp.ToolCallErrors)
	}
	if resp.ToolCallErrors[0].ToolCall.ID != "call_write" || !strings.Contains(resp.ToolCallErrors[0].Message, "file_write tool arguments exceeded 30 bytes") {
		t.Fatalf("unexpected tool call error: %#v", resp.ToolCallErrors[0])
	}
	if !sawStatus || !sawDone {
		t.Fatalf("expected status and done events, sawStatus=%v sawDone=%v", sawStatus, sawDone)
	}
}

func TestStreamChatResponseEmitsPromptProgressStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"prompt_progress\":{\"total\":10,\"processed\":5,\"cache\":2,\"time_ms\":3}}\n\n"))
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"done\"}}]}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer server.Close()

	client, err := New("test", config.Provider{
		BaseURL: server.URL,
		Timeout: time.Second,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}

	var status domain.Event
	resp, err := client.StreamChatResponse(context.Background(), ChatRequest{Model: "test"}, func(evt domain.Event) {
		if evt.Kind == domain.EventKindStatus && strings.HasPrefix(evt.Text, "Processing prompt") {
			status = evt
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if !resp.PromptProgressSeen {
		t.Fatalf("expected prompt progress marker, got %#v", resp)
	}
	if status.Text != "Processing prompt 50%" {
		t.Fatalf("expected prompt progress status, got %#v", status)
	}
	if status.Meta[domain.EventMetaPromptProgress] != "true" || status.Meta["processed"] != "5" || status.Meta["total"] != "10" || status.Meta["cache"] != "2" || status.Meta["time_ms"] != "3" {
		t.Fatalf("unexpected prompt progress metadata: %#v", status.Meta)
	}
}

func TestStreamChatResponseStopsEmittingBufferedEventsAfterCancel(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"first\"}}]}\n\n"))
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"second\"}}]}\n\n"))
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"third\"}}]}\n\n"))
	}))
	defer server.Close()

	client, err := New("test", config.Provider{
		BaseURL: server.URL,
		Timeout: time.Second,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var deltas []string
	_, err = client.StreamChatResponse(ctx, ChatRequest{Model: "test"}, func(evt domain.Event) {
		if evt.Kind != domain.EventKindMessageDelta {
			return
		}
		deltas = append(deltas, evt.Text)
		cancel()
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context cancellation, got %v", err)
	}
	if got := strings.Join(deltas, ""); got != "first" {
		t.Fatalf("expected stream to stop after first delta, got %q", got)
	}
}

func TestStreamChatResponseEmitsUsageWhenTotalTokensMissing(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"hello\"}}],\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":2}}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer server.Close()

	client, err := New("test", config.Provider{
		BaseURL: server.URL,
		Timeout: time.Second,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var usageEvents []domain.Usage
	resp, err := client.StreamChatResponse(context.Background(), ChatRequest{Model: "test"}, func(evt domain.Event) {
		if evt.Kind == domain.EventKindUsage {
			usageEvents = append(usageEvents, evt.Usage)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(usageEvents) != 1 {
		t.Fatalf("expected one usage event, got %#v", usageEvents)
	}
	if usageEvents[0].TotalTokens != 12 {
		t.Fatalf("expected synthesized total tokens in event, got %#v", usageEvents[0])
	}
	if resp.Usage.TotalTokens != 12 {
		t.Fatalf("expected synthesized total tokens in response, got %#v", resp.Usage)
	}
}

func TestStreamChatResponseMergesToolCallsByIndexAcrossArgumentChunks(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"id\":\"call_read\",\"type\":\"function\",\"index\":0,\"function\":{\"name\":\"read\",\"arguments\":\"\"}}]}}]}\n\n"))
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"{\"}}]}}]}\n\n"))
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"\\\"path\\\":\\\".\\\"\"}}]}}]}\n\n"))
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer server.Close()

	client, err := New("test", config.Provider{
		BaseURL: server.URL,
		Timeout: time.Second,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}

	resp, err := client.StreamChatResponse(context.Background(), ChatRequest{Model: "test"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.ToolCalls) != 1 {
		t.Fatalf("expected one aggregated tool call, got %#v", resp.ToolCalls)
	}
	got := resp.ToolCalls[0]
	if got.Function.Name != "read" {
		t.Fatalf("expected tool name read, got %#v", got)
	}
	if got.Function.Arguments != "{\"path\":\".\"}" {
		t.Fatalf("expected merged arguments, got %#v", got)
	}
}

func TestStreamChatResponsePreservesWhitespaceOnlyToolArgumentChunks(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"id\":\"call_exec\",\"type\":\"function\",\"index\":0,\"function\":{\"name\":\"exec_command\",\"arguments\":\"\"}}]}}]}\n\n"))
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"{\\\"cmd\\\":\\\"sed -i 's/else\"}}]}}]}\n\n"))
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\" \"}}]}}]}\n\n"))
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"389/else\"}}]}}]}\n\n"))
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\" \"}}]}}]}\n\n"))
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"389/g' file\\\"}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer server.Close()

	client, err := New("test", config.Provider{
		BaseURL: server.URL,
		Timeout: time.Second,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}

	resp, err := client.StreamChatResponse(context.Background(), ChatRequest{Model: "test"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.ToolCalls) != 1 {
		t.Fatalf("expected one aggregated tool call, got %#v", resp.ToolCalls)
	}
	got := resp.ToolCalls[0].Function.Arguments
	want := `{"cmd":"sed -i 's/else 389/else 389/g' file"}`
	if got != want {
		t.Fatalf("expected whitespace-only argument chunks to be preserved, got %q want %q", got, want)
	}
}

func TestStreamChatResponsePreservesWhitespaceOnlyReasoningChunks(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"else\"}}]}\n\n"))
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"reasoning_content\":\" \"}}]}\n\n"))
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"389\"}}]}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer server.Close()

	client, err := New("test", config.Provider{
		BaseURL: server.URL,
		Timeout: time.Second,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}

	var reasoning strings.Builder
	resp, err := client.StreamChatResponse(context.Background(), ChatRequest{Model: "test"}, func(evt domain.Event) {
		if evt.Kind == domain.EventKindReasoning {
			reasoning.WriteString(evt.Text)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Reasoning != "else 389" {
		t.Fatalf("expected response reasoning to preserve whitespace-only chunks, got %q", resp.Reasoning)
	}
	if reasoning.String() != "else 389" {
		t.Fatalf("expected reasoning events to preserve whitespace-only chunks, got %q", reasoning.String())
	}
}

func TestStreamChatWithRecorderDoesNotBufferEventStream(t *testing.T) {
	firstChunkSent := make(chan struct{}, 1)
	releaseDone := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("expected flusher")
		}
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"hel\"}}]}\n\n"))
		flusher.Flush()
		firstChunkSent <- struct{}{}
		<-releaseDone
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"lo\"},\"finish_reason\":\"stop\"}]}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
		flusher.Flush()
	}))
	defer server.Close()

	recorder := debugsrv.NewRecorder()
	client, err := New("test", config.Provider{
		BaseURL: server.URL,
		Timeout: time.Second,
	}, recorder)
	if err != nil {
		t.Fatal(err)
	}

	events, err := client.StreamChat(context.Background(), ChatRequest{Model: "test"})
	if err != nil {
		t.Fatal(err)
	}

	select {
	case <-firstChunkSent:
	case <-time.After(time.Second):
		t.Fatal("expected server to flush first chunk")
	}

	select {
	case evt := <-events:
		if evt.Kind != domain.EventKindMessageDelta || evt.Text != "hel" {
			t.Fatalf("expected first streamed delta before stream completion, got %#v", evt)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("expected first streamed delta without waiting for stream completion")
	}

	close(releaseDone)

	var combined strings.Builder
	for evt := range events {
		if evt.Kind == domain.EventKindMessageDelta {
			combined.WriteString(evt.Text)
		}
		if evt.Err != nil {
			t.Fatal(evt.Err)
		}
	}
	if combined.String() != "lo" {
		t.Fatalf("expected remaining streamed delta after release, got %q", combined.String())
	}

	traces := recorder.HTTPTraces()
	if len(traces) != 1 {
		t.Fatalf("expected one recorded trace, got %d", len(traces))
	}
	if strings.Contains(traces[0].ResponseBody, "[stream omitted]") {
		t.Fatalf("expected real stream body capture, got %q", traces[0].ResponseBody)
	}
	if !strings.Contains(traces[0].ResponseBody, `"content":"hel"`) {
		t.Fatalf("expected first SSE chunk in response body, got %q", traces[0].ResponseBody)
	}
	if !strings.Contains(traces[0].ResponseBody, "[DONE]") {
		t.Fatalf("expected done marker in response body, got %q", traces[0].ResponseBody)
	}
}

func TestStreamChatRecorderExposesAndCancelsActiveStream(t *testing.T) {
	firstChunkSent := make(chan struct{}, 1)
	requestCanceled := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("expected flusher")
		}
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"me think\"}}]}\n\n"))
		flusher.Flush()
		firstChunkSent <- struct{}{}
		<-r.Context().Done()
		requestCanceled <- struct{}{}
	}))
	defer server.Close()

	recorder := debugsrv.NewRecorder()
	client, err := New("test", config.Provider{
		BaseURL: server.URL,
		Timeout: time.Second,
	}, recorder)
	if err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)
	go func() {
		_, err := client.StreamChatResponse(context.Background(), ChatRequest{
			SessionID: "session-a",
			ChatID:    "chat-a",
			Model:     "test",
			Messages:  []Message{{Role: RoleUser, Content: "hello"}},
		}, nil)
		done <- err
	}()

	select {
	case <-firstChunkSent:
	case <-time.After(time.Second):
		t.Fatal("expected server to flush first chunk")
	}
	var active []debugsrv.ActiveHTTPTrace
	for deadline := time.Now().Add(time.Second); time.Now().Before(deadline); {
		active = recorder.ActiveHTTPTraces(debugsrv.HTTPTraceFilter{SessionID: "session-a", ChatID: "chat-a"})
		if len(active) == 1 && strings.Contains(active[0].ResponseBody, "me think") {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(active) != 1 || !strings.Contains(active[0].ResponseBody, "me think") {
		t.Fatalf("expected active stream partial data, got %#v", active)
	}
	if !recorder.CancelActiveHTTP(active[0].ID) {
		t.Fatalf("expected active stream cancel to succeed")
	}

	select {
	case <-requestCanceled:
	case <-time.After(time.Second):
		t.Fatal("expected server request context to be canceled")
	}
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context canceled stream error, got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("expected stream call to return after debug cancel")
	}
}

func TestStreamChatTraceIncludesPromptProgressMeta(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}],\"prompt_progress\":{\"total\":100,\"cache\":80,\"processed\":90,\"time_ms\":7}}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer server.Close()

	recorder := debugsrv.NewRecorder()
	client, err := New("test", config.Provider{
		BaseURL: server.URL,
		Timeout: time.Second,
	}, recorder)
	if err != nil {
		t.Fatal(err)
	}

	resp, err := client.StreamChatResponse(context.Background(), ChatRequest{
		SessionID: "session-a",
		ChatID:    "chat-a",
		Model:     "test",
		Messages:  []Message{{Role: RoleUser, Content: "hello"}},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !resp.PromptProgressSeen {
		t.Fatalf("expected prompt progress in response, got %#v", resp)
	}
	traces := recorder.HTTPTraces()
	if len(traces) != 1 {
		t.Fatalf("expected one trace, got %#v", traces)
	}
	meta := traces[0].Meta
	if meta["prompt_progress_total"] != "100" || meta["prompt_progress_cache"] != "80" || meta["prompt_progress_processed"] != "90" || meta["prompt_progress_time_ms"] != "7" {
		t.Fatalf("expected prompt progress trace meta, got %#v", meta)
	}
	if traces[0].SessionID != "session-a" || traces[0].ChatID != "chat-a" {
		t.Fatalf("expected request ids in trace, got %#v", traces[0])
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

func TestStreamChatResponseDoesNotTimeoutActiveBodyReadAfterHeaders(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		_ = r.Body.Close()
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("expected flusher")
		}
		_, _ = w.Write([]byte("data: {\"choices\":[{\"index\":0,\"delta\":{\"reasoning\":\"Thinking\"}}]}\n\n"))
		flusher.Flush()
		time.Sleep(150 * time.Millisecond)
		_, _ = w.Write([]byte("data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"done\"}}]}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
		flusher.Flush()
	}))
	defer server.Close()

	client, err := New("test", config.Provider{
		BaseURL: server.URL,
		Timeout: 100 * time.Millisecond,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}

	resp, err := client.StreamChatResponse(context.Background(), ChatRequest{Model: "test"}, nil)
	if err != nil {
		t.Fatalf("expected active stream to survive past header timeout, got %v", err)
	}
	if resp.Text != "done" {
		t.Fatalf("unexpected response text: %#v", resp)
	}
	if resp.Reasoning != "Thinking" {
		t.Fatalf("unexpected response reasoning: %#v", resp)
	}
}

func TestStreamChatResponseRecordsReadFailureAfterHeaders(t *testing.T) {
	recorder := debugsrv.NewRecorder()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		_ = r.Body.Close()
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("expected flusher")
		}
		_, _ = w.Write([]byte("data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hel\"}}]}\n\n"))
		flusher.Flush()
		time.Sleep(250 * time.Millisecond)
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	client, err := New("test", config.Provider{
		BaseURL: server.URL,
		Timeout: time.Second,
	}, recorder)
	if err != nil {
		t.Fatal(err)
	}

	_, err = client.StreamChatResponse(ctx, ChatRequest{Model: "test"}, nil)
	if err == nil {
		t.Fatal("expected stream read failure")
	}

	traces := recorder.HTTPTraces()
	if len(traces) != 1 {
		t.Fatalf("expected one stream failure trace, got %#v", traces)
	}
	failure := traces[0]
	if failure.Status != http.StatusOK {
		t.Fatalf("expected failure trace to preserve response status, got %#v", failure)
	}
	if failure.Meta["phase"] != "read_stream" {
		t.Fatalf("expected read_stream phase meta, got %#v", failure)
	}
	if failure.Meta["chunk_count"] != "1" {
		t.Fatalf("expected chunk_count=1, got %#v", failure)
	}
	if !strings.Contains(failure.Meta["last_payload"], "\"hel\"") {
		t.Fatalf("expected last payload in failure trace, got %#v", failure)
	}
	if failure.Error == "" {
		t.Fatalf("expected failure error in trace, got %#v", failure)
	}
	if !strings.Contains(failure.ResponseBody, `"content":"hel"`) {
		t.Fatalf("expected captured partial stream body, got %#v", failure)
	}
}

func TestMessageMarshalJSONWithContentParts(t *testing.T) {
	buf, err := json.Marshal(Message{
		Role: RoleUser,
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

func TestMessageMarshalJSONSanitizesControlCharacters(t *testing.T) {
	buf, err := json.Marshal(ChatRequest{
		Model:  "test",
		Stream: false,
		Messages: []Message{
			{
				Role:    RoleTool,
				Content: "ldap diag v65f4\x00\nnext\x1b",
			},
			{
				Role: RoleUser,
				ContentParts: []ContentPart{
					TextPart("part\x00text"),
				},
			},
			{
				Role: RoleAssistant,
				ToolCalls: []ToolCall{{
					ID:   "call_1",
					Type: "function",
					Function: FunctionCall{
						Name:      "exec_command",
						Arguments: "{\"cmd\":\"printf \x00\"}",
					},
				}},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	raw := string(buf)
	if strings.Contains(raw, `\u0000`) || strings.Contains(raw, `\u001b`) {
		t.Fatalf("expected control characters to be visible hex escapes, got %s", raw)
	}
	for _, want := range []string{`v65f4\\x00`, `next\\x1b`, `part\\x00text`} {
		if !strings.Contains(raw, want) {
			t.Fatalf("expected %s in sanitized JSON, got %s", want, raw)
		}
	}
}

func TestChatRequestMarshalJSONMergesExtraBody(t *testing.T) {
	buf, err := json.Marshal(ChatRequest{
		Model:  "Qwen/Qwen3.6-35B-A3B",
		Stream: false,
		ExtraBody: map[string]any{
			"chat_template_kwargs": map[string]any{
				"preserve_thinking": true,
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	raw := string(buf)
	if !strings.Contains(raw, `"chat_template_kwargs":{"preserve_thinking":true}`) {
		t.Fatalf("expected merged extra body, got %s", raw)
	}
	if strings.Contains(raw, `"extra_body"`) {
		t.Fatalf("expected extra body to be flattened into the request, got %s", raw)
	}
}

func TestCapabilityStoreEnrichesAndCachesModels(t *testing.T) {
	store := NewCapabilityStore(t.TempDir())
	cfg := config.Provider{BaseURL: "https://api.openai.com/v1"}

	model, err := store.EnrichModel("openai", cfg, domain.Model{ID: "gpt-5.4"})
	if err != nil {
		t.Fatal(err)
	}
	if model.SupportsImages || model.CapabilitiesKnown {
		t.Fatalf("expected unprobed capabilities to remain unknown, got %#v", model)
	}

	models, err := store.EnrichModels("openai", cfg, []domain.Model{{ID: "gpt-5.4"}, {ID: "gpt-4.1-mini"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 2 || models[0].CapabilitiesKnown || models[1].CapabilitiesKnown {
		t.Fatalf("expected unprobed models to remain unknown, got %#v", models)
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
		t.Fatal("expected unknown image support to stay permissive")
	}

	ok, err = store.SupportsAttachment("openai", cfg, "gpt-5.4", attachment.KindPDF)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("expected pdf support to remain disabled")
	}
}

func TestProbeImageSupportSuccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		raw := string(body)
		if !strings.Contains(raw, `"type":"image_url"`) {
			t.Fatalf("expected image part in probe request, got %s", raw)
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"OK"}}]}`))
	}))
	defer server.Close()

	client, err := New("openai-compatible", config.Provider{
		Kind:    ProviderKindCompatible,
		BaseURL: server.URL + "/v1",
		Timeout: time.Second,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}

	ok, err := client.ProbeImageSupport(context.Background(), "demo-model")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected probe success to report image support")
	}
}

func TestProbeImageSupportUnsupported(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"image content part unsupported for this model"}`))
	}))
	defer server.Close()

	client, err := New("openai-compatible", config.Provider{
		Kind:    ProviderKindCompatible,
		BaseURL: server.URL + "/v1",
		Timeout: time.Second,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}

	ok, err := client.ProbeImageSupport(context.Background(), "demo-model")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("expected unsupported probe to report false")
	}
}

func TestProbeTTSSupportSuccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/audio/speech" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		raw := string(body)
		if !strings.Contains(raw, `"model":"omnivoice-base-Q8_0.gguf"`) || !strings.Contains(raw, `"input":"OK"`) {
			t.Fatalf("unexpected tts probe request: %s", raw)
		}
		w.Header().Set("Content-Type", "audio/pcm")
		_, _ = w.Write([]byte{0, 1, 2, 3})
	}))
	defer server.Close()

	client, err := New("openai-compatible", config.Provider{
		Kind:    ProviderKindCompatible,
		BaseURL: server.URL + "/v1",
		Timeout: time.Second,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}

	ok, err := client.ProbeTTSSupport(context.Background(), "omnivoice-base-Q8_0.gguf")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected probe success to report tts support")
	}
}

func TestProbeTTSSupportUnsupported(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer server.Close()

	client, err := New("openai-compatible", config.Provider{
		Kind:    ProviderKindCompatible,
		BaseURL: server.URL + "/v1",
		Timeout: time.Second,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}

	ok, err := client.ProbeTTSSupport(context.Background(), "demo-model")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("expected missing speech endpoint to report false")
	}
}

func TestCreateSpeech(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/audio/speech" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		raw := string(body)
		if !strings.Contains(raw, `"model":"omnivoice-base-Q8_0.gguf"`) || !strings.Contains(raw, `"input":"Hello"`) || !strings.Contains(raw, `"response_format":"wav"`) || !strings.Contains(raw, `"speed":1.25`) {
			t.Fatalf("unexpected tts request: %s", raw)
		}
		w.Header().Set("Content-Type", "audio/pcm")
		_, _ = w.Write([]byte{4, 5, 6})
	}))
	defer server.Close()

	client, err := New("openai-compatible", config.Provider{
		Kind:    ProviderKindCompatible,
		BaseURL: server.URL + "/v1",
		Timeout: time.Second,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}

	speech, err := client.CreateSpeech(context.Background(), SpeechRequest{Model: "omnivoice-base-Q8_0.gguf", Input: "Hello", Voice: "alloy", ResponseFormat: "wav", Speed: 1.25})
	if err != nil {
		t.Fatal(err)
	}
	if speech.ContentType != "audio/pcm" || string(speech.Audio) != string([]byte{4, 5, 6}) {
		t.Fatalf("unexpected speech response: %#v", speech)
	}
}

func TestCapabilityStoreDetectsTTSOnlyModel(t *testing.T) {
	var speechCalls int
	var chatCalls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/audio/speech":
			speechCalls++
			w.Header().Set("Content-Type", "audio/pcm")
			_, _ = w.Write([]byte{0, 1, 2, 3})
		case "/v1/chat/completions":
			chatCalls++
			http.NotFound(w, r)
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	store := NewCapabilityStore(t.TempDir())
	cfg := config.Provider{
		Kind:    ProviderKindCompatible,
		BaseURL: server.URL + "/v1",
		Timeout: time.Second,
	}

	model, err := store.EnrichModel("local-tts", cfg, domain.Model{ID: "omnivoice-base-Q8_0.gguf"})
	if err != nil {
		t.Fatal(err)
	}
	if !model.SupportsTTS || model.SupportsChat || !model.CapabilitiesKnown || model.CapabilitySource != "probe" {
		t.Fatalf("expected tts-only probe result, got %#v", model)
	}
	if speechCalls != 1 || chatCalls != 1 {
		t.Fatalf("expected one speech and chat probe, got speech=%d chat=%d", speechCalls, chatCalls)
	}

	model, err = store.EnrichModel("local-tts", cfg, domain.Model{ID: "omnivoice-base-Q8_0.gguf"})
	if err != nil {
		t.Fatal(err)
	}
	if !model.SupportsTTS || model.SupportsChat {
		t.Fatalf("expected cached tts-only result, got %#v", model)
	}
	if speechCalls != 1 || chatCalls != 1 {
		t.Fatalf("expected cached result without reprobe, got speech=%d chat=%d", speechCalls, chatCalls)
	}
}

func TestCapabilityStoreSupportsAttachmentCachesProbeResult(t *testing.T) {
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"OK"}}]}`))
	}))
	defer server.Close()

	store := NewCapabilityStore(t.TempDir())
	cfg := config.Provider{
		Kind:    ProviderKindCompatible,
		BaseURL: server.URL + "/v1",
		Timeout: time.Second,
	}

	ok, err := store.SupportsAttachment("openai-compatible", cfg, "custom-vision-model", attachment.KindImage)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected probed image support")
	}

	ok, err = store.SupportsAttachment("openai-compatible", cfg, "custom-vision-model", attachment.KindImage)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected cached probed image support")
	}
	if calls != 1 {
		t.Fatalf("expected one probe request, got %d", calls)
	}

	model, err := store.EnrichModel("openai-compatible", cfg, domain.Model{ID: "custom-vision-model"})
	if err != nil {
		t.Fatal(err)
	}
	if !model.SupportsImages || !model.CapabilitiesKnown || model.CapabilitySource != "probe" {
		t.Fatalf("expected probed image capability to enrich models, got %#v", model)
	}
}

func TestCapabilityStoreInvalidateForcesReprobe(t *testing.T) {
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"OK"}}]}`))
	}))
	defer server.Close()

	store := NewCapabilityStore(t.TempDir())
	cfg := config.Provider{
		Kind:    ProviderKindCompatible,
		BaseURL: server.URL + "/v1",
		Timeout: time.Second,
	}

	ok, err := store.SupportsAttachment("openai-compatible", cfg, "custom-vision-model", attachment.KindImage)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected initial probed image support")
	}
	if err := store.Invalidate("openai-compatible", cfg, "custom-vision-model"); err != nil {
		t.Fatal(err)
	}
	ok, err = store.SupportsAttachment("openai-compatible", cfg, "custom-vision-model", attachment.KindImage)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected reprobed image support")
	}
	if calls != 2 {
		t.Fatalf("expected invalidate to force reprobe, got %d probe requests", calls)
	}
}

func TestCapabilityStoreIgnoresStaleHeuristicFalseWhenProbeIsInconclusive(t *testing.T) {
	store := NewCapabilityStore(t.TempDir())
	cfg := config.Provider{
		BaseURL: "http://127.0.0.1:1/v1",
	}
	cache, err := store.load()
	if err != nil {
		t.Fatal(err)
	}
	cache.Entries[capabilityKey("openai", cfg.BaseURL, "Lorbus/Qwen3.6-27B-int4-AutoRound")] = capabilityEntry{
		ProviderID:        "openai",
		BaseURL:           cfg.BaseURL,
		ModelID:           "Lorbus/Qwen3.6-27B-int4-AutoRound",
		SupportsImages:    false,
		SupportsPDFs:      false,
		CapabilitySource:  "heuristic",
		CapabilitiesKnown: true,
		DetectedAt:        time.Now().UTC(),
	}
	if err := store.save(cache); err != nil {
		t.Fatal(err)
	}

	ok, err := store.SupportsAttachment("openai", cfg, "Lorbus/Qwen3.6-27B-int4-AutoRound", attachment.KindImage)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected inconclusive probe to remain permissive")
	}

	model, err := store.EnrichModel("openai", cfg, domain.Model{ID: "Lorbus/Qwen3.6-27B-int4-AutoRound"})
	if err != nil {
		t.Fatal(err)
	}
	if model.CapabilitiesKnown {
		t.Fatalf("expected stale heuristic entry to be ignored for model enrichment, got %#v", model)
	}
}
