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

func TestSerializePromptEnvelopeCollapsesInstructionsIntoSingleSystemMessage(t *testing.T) {
	env := PromptEnvelope{
		Instructions: []InstructionBlock{
			{Kind: InstructionKindBaseSystem, Text: "Base prompt"},
			{Kind: InstructionKindProjectInstructions, Text: "Project instructions"},
			{Kind: InstructionKindSkills, Text: "Skills"},
			{Kind: InstructionKindSessionNote, Text: "Session update:\nPermission mode changed", Ephemeral: true},
		},
		Items: []Message{
			{Role: domain.MessageRoleUser, Content: "hello"},
		},
	}

	got := SerializePromptEnvelope(env)
	if len(got) != 2 {
		t.Fatalf("expected one system message plus user item, got %#v", got)
	}
	if got[0].Role != domain.MessageRoleSystem {
		t.Fatalf("expected leading system message, got %#v", got)
	}
	if strings.Contains(got[0].Content, "\n\n\n") {
		t.Fatalf("expected normalized system join, got %q", got[0].Content)
	}
	for _, want := range []string{"Base prompt", "Project instructions", "Skills", "Session update:\nPermission mode changed"} {
		if !strings.Contains(got[0].Content, want) {
			t.Fatalf("expected %q in joined system prompt, got %q", want, got[0].Content)
		}
	}
	if got[1].Role != domain.MessageRoleUser || got[1].Content != "hello" {
		t.Fatalf("unexpected trailing item: %#v", got[1])
	}
}

func TestMessageMarshalJSONOmitsEmptyAssistantToolCallContent(t *testing.T) {
	data, err := json.Marshal(Message{
		Role: domain.MessageRoleAssistant,
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
	if got != 16384 {
		t.Fatalf("unexpected detected context window: %d", got)
	}
}

func TestDetectContextWindowUsesCompatibleLocalPropsWithoutV1(t *testing.T) {
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
		Kind:          ProviderKindCompatible,
		BaseURL:       server.URL + "/v1",
		ContextWindow: 32768,
		Timeout:       time.Second,
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
	var sawToolCallDelta bool
	resp, err := client.StreamChatResponse(context.Background(), ChatRequest{Model: "test"}, func(evt domain.Event) {
		switch evt.Kind {
		case domain.EventKindMessageDelta:
			deltas = append(deltas, evt.Text)
		case domain.EventKindToolCallDelta:
			sawToolCallDelta = true
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
	if !sawToolCallDelta {
		t.Fatal("expected streamed tool call delta event")
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
	if traces[0].ResponseBody != "[stream omitted]" {
		t.Fatalf("expected stream body omission marker, got %q", traces[0].ResponseBody)
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
