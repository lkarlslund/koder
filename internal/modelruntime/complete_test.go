package modelruntime

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	chatpkg "github.com/lkarlslund/koder/internal/chat"
	"github.com/lkarlslund/koder/internal/config"
	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/provider"
)

func testConfig(t *testing.T) config.Config {
	t.Helper()
	return config.Default().WithStateDir(t.TempDir())
}

func hasStatusEvent(events []domain.Event, text string) bool {
	for _, evt := range events {
		if evt.Kind == domain.EventKindStatus && strings.Contains(evt.Text, text) {
			return true
		}
	}
	return false
}

func TestCavemanThinkingHonorsMinimumTokenSetting(t *testing.T) {
	t.Parallel()

	cfg := testConfig(t)
	cfg.Thinking.CavemanEnabled = true
	cfg.Thinking.CavemanMinTokens = 64
	runtime := New(Config{Config: cfg})

	if runtime.shouldCavemanThinking("short thought") {
		t.Fatal("expected short reasoning to skip caveman conversion")
	}
	if !runtime.shouldCavemanThinking(strings.Repeat("reasoning ", 80)) {
		t.Fatal("expected reasoning above minimum tokens to activate caveman conversion")
	}
}

func TestCavemanReasoningStopsOversizedThinkingStream(t *testing.T) {
	t.Parallel()

	requestCanceled := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("expected flusher")
		}
		for range 12 {
			_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"" + strings.Repeat("x", 512) + "\"}}]}\n\n"))
			flusher.Flush()
			select {
			case <-r.Context().Done():
				requestCanceled <- struct{}{}
				return
			case <-time.After(20 * time.Millisecond):
			}
		}
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer server.Close()

	cfg := testConfig(t)
	cfg.Providers = map[string]config.Provider{"test": {BaseURL: server.URL, Timeout: time.Second}}
	runtime := New(Config{Config: cfg})
	client, err := provider.New("test", cfg.Providers["test"], nil)
	if err != nil {
		t.Fatal(err)
	}
	events := make(chan domain.Event, 32)
	resp, err := runtime.completeCavemanThinking(context.Background(), "test", client, provider.ChatRequest{
		Model:    "test",
		Messages: []provider.Message{{Role: provider.RoleUser, Content: "rewrite"}},
		Stream:   true,
	}, events)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Reasoning != "" {
		t.Fatalf("expected oversized reasoning-only caveman stream to be discarded, got %.80q", resp.Reasoning)
	}
	select {
	case <-requestCanceled:
	case <-time.After(time.Second):
		t.Fatal("expected oversized caveman stream to cancel provider request")
	}
	close(events)
	var gotEvents []domain.Event
	for evt := range events {
		gotEvents = append(gotEvents, evt)
	}
	if !hasStatusEvent(gotEvents, "Caveman thinking exceeded") {
		t.Fatalf("expected over-limit caveman status, got %#v", gotEvents)
	}
}

func TestChatWithRetryRetriesTransientEOFBeforeStreamingStarts(t *testing.T) {
	t.Parallel()

	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		switch requests {
		case 1:
			hijacker, ok := w.(http.Hijacker)
			if !ok {
				t.Fatal("response writer does not support hijacking")
			}
			conn, _, err := hijacker.Hijack()
			if err != nil {
				t.Fatalf("hijack: %v", err)
			}
			_ = conn.Close()
		case 2:
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"hel\"}}]}\n\n"))
			_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"lo\"}}],\"usage\":{\"total_tokens\":1}}\n\n"))
			_, _ = w.Write([]byte("data: [DONE]\n\n"))
		default:
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ignored title"}}],"usage":{"total_tokens":1}}`))
		}
	}))
	defer server.Close()

	cfg := testConfig(t)
	cfg.Providers = map[string]config.Provider{
		"test": {
			BaseURL: server.URL + "/v1",
			Timeout: time.Second,
			Stream:  true,
		},
	}
	cfg.Defaults.ProviderID = "test"
	cfg.Defaults.ModelID = "test-model"

	runtime := New(Config{Config: cfg})
	var waited []time.Duration
	runtime.SetRetryPause(func(_ context.Context, delay time.Duration, onTick func(time.Duration)) error {
		if onTick != nil {
			onTick(delay)
		}
		waited = append(waited, delay)
		return nil
	})
	client, err := provider.New("test", cfg.Providers["test"], nil)
	if err != nil {
		t.Fatal(err)
	}

	events := make(chan domain.Event, 16)
	session := domain.Session{ID: "session-test"}
	chat := domain.Chat{ID: "chat-test", SessionID: session.ID, ProviderID: "test", ModelID: "test-model"}
	resp, streamed, _, err := runtime.chatWithRetry(context.Background(), session, chat, client, events, provider.ChatRequest{
		Model: "test-model",
		Messages: []provider.Message{{
			Role:    provider.RoleUser,
			Content: "hello",
		}},
		Stream: true,
	}, domain.TimelineItem{ID: chatpkg.NewTimelineID(time.Now().UTC())})
	if err != nil {
		t.Fatalf("expected transient retry to succeed, got %v", err)
	}
	close(events)
	if !streamed {
		t.Fatal("expected streaming request")
	}
	if resp.Text != "hello" {
		t.Fatalf("expected final response text hello, got %#v", resp)
	}
	var sawRetry bool
	for evt := range events {
		if evt.Kind == domain.EventKindStatus && strings.Contains(evt.Text, "connection dropped") {
			sawRetry = true
		}
	}
	if len(waited) != 1 || waited[0] != defaultTransientRetryWait {
		t.Fatalf("expected single transient retry wait of %s, got %#v", defaultTransientRetryWait, waited)
	}
	if !sawRetry {
		t.Fatal("expected transient retry status event")
	}
	if requests < 2 {
		t.Fatalf("expected retried provider request, got %d requests", requests)
	}
}

func TestChatWithRetryDoesNotRetryAfterPartialStreamFailure(t *testing.T) {
	t.Parallel()

	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		switch requests {
		case 1:
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"hel\"}}]}\n\n"))
			_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"broken\"}}\n\n"))
		default:
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"should not retry"}}],"usage":{"total_tokens":1}}`))
		}
	}))
	defer server.Close()

	cfg := testConfig(t)
	cfg.Providers = map[string]config.Provider{
		"test": {
			BaseURL: server.URL + "/v1",
			Timeout: time.Second,
			Stream:  true,
		},
	}
	cfg.Defaults.ProviderID = "test"
	cfg.Defaults.ModelID = "test-model"

	runtime := New(Config{Config: cfg})
	client, err := provider.New("test", cfg.Providers["test"], nil)
	if err != nil {
		t.Fatal(err)
	}

	events := make(chan domain.Event, 16)
	session := domain.Session{ID: "session-test"}
	chat := domain.Chat{ID: "chat-test", SessionID: session.ID, ProviderID: "test", ModelID: "test-model"}
	_, streamed, _, err := runtime.chatWithRetry(context.Background(), session, chat, client, events, provider.ChatRequest{
		Model: "test-model",
		Messages: []provider.Message{{
			Role:    provider.RoleUser,
			Content: "hello",
		}},
		Stream: true,
	}, domain.TimelineItem{ID: chatpkg.NewTimelineID(time.Now().UTC())})
	close(events)
	if err == nil {
		t.Fatal("expected stream failure error")
	}
	if !streamed {
		t.Fatal("expected streaming request")
	}

	var (
		deltas   []string
		sawRetry bool
	)
	for evt := range events {
		if evt.Kind == domain.EventKindMessageDelta {
			deltas = append(deltas, evt.Text)
		}
		if evt.Kind == domain.EventKindStatus && strings.Contains(evt.Text, "connection dropped") {
			sawRetry = true
		}
	}
	if strings.Join(deltas, "") != "hel" {
		t.Fatalf("expected partial streamed delta before failure, got %#v", deltas)
	}
	if sawRetry {
		t.Fatal("did not expect retry status after partial stream failure")
	}
	if requests != 1 {
		t.Fatalf("expected no retry after partial stream failure, got %d requests", requests)
	}
}

func TestChatWithRetryOpportunisticallyDisablesRejectedPromptProgress(t *testing.T) {
	t.Parallel()

	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil && err != io.EOF {
			t.Fatalf("decode request: %v", err)
		}
		switch requests {
		case 1:
			if body["return_progress"] != true {
				t.Fatalf("expected first request to try return_progress, got %#v", body)
			}
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"message":"unknown field return_progress"}}`))
		case 2:
			if _, ok := body["return_progress"]; ok {
				t.Fatalf("expected retry without return_progress, got %#v", body)
			}
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"hello\"}}]}\n\n"))
			_, _ = w.Write([]byte("data: [DONE]\n\n"))
		default:
			t.Fatalf("unexpected request %d", requests)
		}
	}))
	defer server.Close()

	cfg := testConfig(t)
	cfg.Providers = map[string]config.Provider{
		"test": {
			BaseURL: server.URL + "/v1",
			Timeout: time.Second,
			Stream:  true,
		},
	}
	runtime := New(Config{Config: cfg})
	client, err := provider.New("test", cfg.Providers["test"], nil)
	if err != nil {
		t.Fatal(err)
	}

	events := make(chan domain.Event, 16)
	session := domain.Session{ID: "session-test"}
	chat := domain.Chat{ID: "chat-test", SessionID: session.ID, ProviderID: "test", ModelID: "test-model"}
	resp, streamed, _, err := runtime.chatWithRetry(context.Background(), session, chat, client, events, provider.ChatRequest{
		Model: "test-model",
		Messages: []provider.Message{{
			Role:    provider.RoleUser,
			Content: "hello",
		}},
		Stream:    true,
		ExtraBody: provider.RequestExtraBody(cfg.Providers["test"], config.ModelConfig{ModelID: "test-model", ModelPreset: provider.ModelPresetDefault}),
	}, domain.TimelineItem{ID: chatpkg.NewTimelineID(time.Now().UTC())})
	close(events)
	if err != nil {
		t.Fatal(err)
	}
	if !streamed || resp.Text != "hello" {
		t.Fatalf("unexpected response: streamed=%v resp=%#v", streamed, resp)
	}
	if requests != 2 {
		t.Fatalf("expected one prompt-progress retry, got %d requests", requests)
	}
	updated := runtime.cfg.Providers["test"]
	if !updated.PromptProgressProbed || updated.PromptProgressSupported {
		t.Fatalf("expected prompt progress to be persisted unsupported, got %#v", updated)
	}
}
