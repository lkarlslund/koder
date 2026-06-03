package debugsrv

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/id"
	"github.com/lkarlslund/koder/internal/modeltest"
	"github.com/lkarlslund/koder/internal/store"
	"github.com/lkarlslund/koder/internal/version"
)

type fakeChatRewinder struct {
	sessionID id.ID
	chatID    id.ID
	anchorID  id.ID
}

func (f *fakeChatRewinder) RewindLiveChat(_ context.Context, sessionID, chatID, anchorItemID id.ID) (any, error) {
	f.sessionID = sessionID
	f.chatID = chatID
	f.anchorID = anchorItemID
	return map[string]any{"removed_count": 3}, nil
}

func TestRecorderTracksSessionEventsAndRuntime(t *testing.T) {
	t.Parallel()

	rec := NewRecorder()
	rec.RecordLifecycle("session-7", "prompt_submitted", "hello", map[string]string{"source": "web"})
	rec.RecordEvent("session-7", domain.Event{Kind: domain.EventKindToolResult, Text: "done"})
	rec.UpdateProcess(ProcessDebug{Status: "Ready"})
	rec.RegisterClient(ClientDebug{ID: "client-1", SelectedSession: "session-7", SelectedChat: "chat-9", ViewportWidth: 80})
	rec.UpdateChats([]ChatDebug{{ID: "chat-9", SessionID: "session-7", Status: "idle"}})

	events := rec.Events("session-7")
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
	runtime := rec.Runtime()
	payload, err := json.Marshal(runtime)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(payload), "current_chat") || strings.Contains(string(payload), "current_session") || strings.Contains(string(payload), "focused_window") {
		t.Fatalf("runtime debug still contains stale single-focus fields: %s", string(payload))
	}
	if runtime.Process.Status != "Ready" {
		t.Fatalf("unexpected runtime process: %#v", runtime.Process)
	}
	if len(runtime.Clients) != 1 || runtime.Clients[0].SelectedSession != "session-7" || runtime.Clients[0].ViewportWidth != 80 {
		t.Fatalf("expected per-client focus details, got %#v", runtime.Clients)
	}
	if len(runtime.Chats) != 1 || runtime.Chats[0].ID != "chat-9" {
		t.Fatalf("expected chat debug state, got %#v", runtime.Chats)
	}
	if runtime.Process.Build.Version != version.Version {
		t.Fatalf("expected runtime build version %q, got %#v", version.Version, runtime.Process.Build)
	}
	if runtime.DeepDebug {
		t.Fatalf("expected deep debug off by default, got %#v", runtime)
	}
}

func TestRecorderAddsHTTPRequestDiagnostics(t *testing.T) {
	t.Parallel()

	rec := NewRecorder()
	bodyA := `{"messages":[{"role":"system","content":"base"},{"role":"user","content":"hello"}],"model":"m","stream":true,"tools":[{"type":"function","function":{"name":"file_read"}}]}`
	bodyB := `{"messages":[{"role":"system","content":"base"},{"role":"user","content":"hello again"}],"model":"m","stream":true,"tools":[{"type":"function","function":{"name":"file_read"}}]}`
	rec.RecordHTTP(HTTPTrace{ProviderID: "p", SessionID: "session-a", ChatID: "chat-a", Method: http.MethodPost, Path: "/v1/chat/completions", RequestBody: bodyA})
	rec.RecordHTTP(HTTPTrace{ProviderID: "p", SessionID: "session-a", ChatID: "chat-a", Method: http.MethodPost, Path: "/v1/chat/completions", RequestBody: bodyB})

	traces := rec.HTTPTraces()
	if len(traces) != 2 {
		t.Fatalf("expected two traces, got %#v", traces)
	}
	first := traces[0].Meta
	for _, key := range []string{"request_bytes", "request_sha256", "messages_sha256", "system_sha256", "tools_sha256"} {
		if first[key] == "" {
			t.Fatalf("expected first trace meta %q, got %#v", key, first)
		}
	}
	if traces[1].Meta["previous_lcp_bytes"] == "" {
		t.Fatalf("expected second trace previous_lcp_bytes, got %#v", traces[1].Meta)
	}
	if traces[1].ChatID != "chat-a" || traces[1].SessionID != "session-a" {
		t.Fatalf("expected trace ids to be retained, got %#v", traces[1])
	}
}

func TestRecorderKeepsLastTwentyFullHTTPRequestBodies(t *testing.T) {
	t.Parallel()

	rec := NewRecorder()
	for i := 0; i < 25; i++ {
		body := `{"messages":[{"role":"user","content":"` + strings.Repeat(fmt.Sprintf("%02d", i), 5000) + `"}]}`
		rec.RecordHTTP(HTTPTrace{
			ProviderID:   "p",
			SessionID:    "session-a",
			ChatID:       "chat-a",
			Method:       http.MethodPost,
			Path:         "/v1/chat/completions",
			RequestBody:  body,
			ResponseBody: `data: {"prompt_progress":{"total":123,"cache":45,"processed":67}}`,
		})
	}

	traces := rec.HTTPTraces()
	if len(traces) != 20 {
		t.Fatalf("expected last 20 traces, got %d", len(traces))
	}
	if !strings.Contains(traces[0].RequestBody, strings.Repeat("05", 100)) {
		t.Fatalf("expected oldest retained trace to be request 05, got %.80q", traces[0].RequestBody)
	}
	if len(traces[0].RequestBody) <= 8192 {
		t.Fatalf("expected full request body larger than old 8 KB cap, got %d", len(traces[0].RequestBody))
	}
	if traces[0].RequestBytes != len(traces[0].RequestBody) {
		t.Fatalf("request bytes = %d, want %d", traces[0].RequestBytes, len(traces[0].RequestBody))
	}
	if traces[0].Meta["prompt_progress_total"] != "123" {
		t.Fatalf("expected prompt progress metadata, got %#v", traces[0].Meta)
	}
}

func TestRecorderFiltersHTTPTraces(t *testing.T) {
	t.Parallel()

	rec := NewRecorder()
	rec.RecordHTTP(HTTPTrace{ProviderID: "p1", SessionID: "session-a", ChatID: "chat-a", RequestBody: `{"messages":[]}`})
	rec.RecordHTTP(HTTPTrace{ProviderID: "p1", SessionID: "session-a", ChatID: "chat-b", RequestBody: `{"messages":[]}`})
	rec.RecordHTTP(HTTPTrace{ProviderID: "p2", SessionID: "session-b", ChatID: "chat-c", RequestBody: `{"messages":[]}`})

	traces := rec.HTTPTraces(HTTPTraceFilter{SessionID: "session-a", ChatID: "chat-b"})
	if len(traces) != 1 || traces[0].ChatID != "chat-b" {
		t.Fatalf("expected chat-b trace, got %#v", traces)
	}
	traces = rec.HTTPTraces(HTTPTraceFilter{ProviderID: "p1", Limit: 1})
	if len(traces) != 1 || traces[0].ChatID != "chat-b" {
		t.Fatalf("expected latest p1 trace, got %#v", traces)
	}
}

func TestServerExposesAndCancelsActiveChatHTTP(t *testing.T) {
	t.Parallel()

	st, err := store.OpenWithOptions(t.TempDir(), store.Options{Backend: store.BackendJSONFS})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	session, err := modeltest.CreateSession(context.Background(), st, "debug", "provider", "model", nil)
	if err != nil {
		t.Fatal(err)
	}
	chat, err := modeltest.DefaultChat(context.Background(), st, session.ID)
	if err != nil {
		t.Fatal(err)
	}

	rec := NewRecorder()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	activeCtx, requestID, finish := rec.StartHTTP(ctx, HTTPTrace{
		ProviderID:  "llamacpp",
		SessionID:   session.ID,
		ChatID:      chat.ID,
		Method:      http.MethodPost,
		Path:        "/v1/chat/completions",
		RequestBody: `{"messages":[{"role":"user","content":"hello"}]}`,
	})
	defer finish()
	rec.UpdateActiveHTTP(requestID, HTTPTrace{
		Status:       http.StatusOK,
		ResponseBody: `data: {"choices":[{"delta":{"reasoning_content":"me think"}}]}`,
		Meta:         map[string]string{"phase": "streaming", "chunk_count": "1"},
	})

	srv := httptest.NewServer(Handler(st, rec))
	defer srv.Close()

	url := srv.URL + "/debug/sessions/" + string(session.ID) + "/chats/" + string(chat.ID) + "/http/active"
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var active struct {
		Active []ActiveHTTPTrace `json:"active"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&active); err != nil {
		t.Fatal(err)
	}
	if len(active.Active) != 1 || active.Active[0].ID != requestID || !strings.Contains(active.Active[0].ResponseBody, "me think") {
		t.Fatalf("expected active partial HTTP trace, got %#v", active.Active)
	}

	resp, err = http.Post(url, "application/json", strings.NewReader(fmt.Sprintf(`{"request_id":%q}`, requestID)))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var canceled struct {
		Canceled int               `json:"canceled"`
		Active   []ActiveHTTPTrace `json:"active"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&canceled); err != nil {
		t.Fatal(err)
	}
	if canceled.Canceled != 1 || len(canceled.Active) != 1 || !canceled.Active[0].Canceling {
		t.Fatalf("expected active request canceling response, got %#v", canceled)
	}
	if err := activeCtx.Err(); err != context.Canceled {
		t.Fatalf("expected active request context canceled, got %v", err)
	}
}

func TestServerExposesTranscriptAndEvents(t *testing.T) {
	t.Parallel()

	st, err := store.OpenWithOptions(t.TempDir(), store.Options{Backend: store.BackendJSONFS})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	session, err := modeltest.CreateSession(context.Background(), st, "debug", "provider", "model", nil)
	if err != nil {
		t.Fatal(err)
	}
	chat, err := modeltest.DefaultChat(context.Background(), st, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := appendDebugTimelineItem(st, chat.ID, domain.UserMessage{Text: "hello"}); err != nil {
		t.Fatal(err)
	}

	rec := NewRecorder()
	rec.RecordLifecycle("", "startup_timing", "list_sessions", map[string]string{"duration_ms": "42"})
	rec.RecordEvent(session.ID, domain.Event{Kind: domain.EventKindMessageDelta, Text: "hello"})

	srv := httptest.NewServer(Handler(st, rec))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/debug/sessions/" + session.ID + "/transcript")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unexpected transcript status: %d", resp.StatusCode)
	}
	var transcript struct {
		Timeline []domain.TimelineItem `json:"timeline"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&transcript); err != nil {
		t.Fatal(err)
	}
	if len(transcript.Timeline) != 1 {
		t.Fatalf("unexpected transcript payload: %#v", transcript)
	}

	resp, err = http.Get(srv.URL + "/debug/sessions/" + session.ID + "/events")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unexpected events status: %d", resp.StatusCode)
	}
	var events struct {
		Events []RecordedEvent `json:"events"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&events); err != nil {
		t.Fatal(err)
	}
	if len(events.Events) != 1 || events.Events[0].Text != "hello" {
		t.Fatalf("unexpected events payload: %#v", events)
	}

	resp, err = http.Get(srv.URL + "/debug/events")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unexpected global events status: %d", resp.StatusCode)
	}
	var global struct {
		SessionID id.ID           `json:"session_id"`
		Events    []RecordedEvent `json:"events"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&global); err != nil {
		t.Fatal(err)
	}
	if global.SessionID != "" {
		t.Fatalf("expected global session id empty, got %s", global.SessionID)
	}
	if len(global.Events) != 2 {
		t.Fatalf("expected 2 global events, got %#v", global)
	}
	if global.Events[0].Kind != "startup_timing" {
		t.Fatalf("expected startup timing event in global stream, got %#v", global.Events[0])
	}
}

func TestServerExposesSpecificChatTranscriptAndHTTPTraces(t *testing.T) {
	t.Parallel()

	st, err := store.OpenWithOptions(t.TempDir(), store.Options{Backend: store.BackendJSONFS})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	session, err := modeltest.CreateSession(context.Background(), st, "debug", "provider", "model", nil)
	if err != nil {
		t.Fatal(err)
	}
	defaultChat, err := modeltest.DefaultChat(context.Background(), st, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	sideChat, err := modeltest.CreateChat(context.Background(), st, session.ID, "Side", "execution", &defaultChat.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := appendDebugTimelineItem(st, defaultChat.ID, domain.UserMessage{Text: "default"}); err != nil {
		t.Fatal(err)
	}
	if _, err := appendDebugTimelineItem(st, sideChat.ID, domain.UserMessage{Text: "side-one"}); err != nil {
		t.Fatal(err)
	}
	if _, err := appendDebugTimelineItem(st, sideChat.ID, domain.UserMessage{Text: "side-two"}); err != nil {
		t.Fatal(err)
	}

	rec := NewRecorder()
	rec.RecordHTTP(HTTPTrace{ProviderID: "p", SessionID: session.ID, ChatID: defaultChat.ID, RequestBody: `{"messages":[{"role":"user","content":"default"}]}`})
	rec.RecordHTTP(HTTPTrace{ProviderID: "p", SessionID: session.ID, ChatID: sideChat.ID, RequestBody: `{"messages":[{"role":"user","content":"side"}]}`})
	srv := httptest.NewServer(Handler(st, rec))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/debug/sessions/" + string(session.ID) + "/chats/" + string(sideChat.ID) + "/transcript?tail=true&limit=1")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(resp.Body)
		t.Fatalf("unexpected transcript status %d: %s", resp.StatusCode, string(data))
	}
	var transcript struct {
		SessionID id.ID                 `json:"session_id"`
		ChatID    id.ID                 `json:"chat_id"`
		Timeline  []domain.TimelineItem `json:"timeline"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&transcript); err != nil {
		t.Fatal(err)
	}
	if transcript.ChatID != sideChat.ID || len(transcript.Timeline) != 1 {
		t.Fatalf("unexpected transcript response: %#v", transcript)
	}
	user, ok := transcript.Timeline[0].Content.(domain.UserMessage)
	if !ok || user.Text != "side-two" {
		t.Fatalf("expected side chat tail item, got %#v", transcript.Timeline)
	}

	resp, err = http.Get(srv.URL + "/debug/sessions/" + string(session.ID) + "/chats/" + string(sideChat.ID) + "/http")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var traces struct {
		Traces []HTTPTrace `json:"traces"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&traces); err != nil {
		t.Fatal(err)
	}
	if len(traces.Traces) != 1 || traces.Traces[0].ChatID != sideChat.ID || !strings.Contains(traces.Traces[0].RequestBody, "side") {
		t.Fatalf("expected side chat HTTP trace, got %#v", traces.Traces)
	}

	resp, err = http.Get(srv.URL + "/debug/sessions/" + string(session.ID) + "/chats/not-a-chat/http")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected invalid chat/session pair to fail, got %d", resp.StatusCode)
	}
}

func TestServerExposesSpecificChatDebugDetails(t *testing.T) {
	t.Parallel()

	st, err := store.OpenWithOptions(t.TempDir(), store.Options{Backend: store.BackendJSONFS})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	session, err := modeltest.CreateSession(context.Background(), st, "debug", "provider", "model", nil)
	if err != nil {
		t.Fatal(err)
	}
	chat, err := modeltest.DefaultChat(context.Background(), st, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := appendDebugTimelineItem(st, chat.ID, domain.Compaction{
		Status:              "completed",
		Summary:             "summary",
		FirstKeptItemID:     "kept-1",
		BeforeContextTokens: 1000,
		AfterContextTokens:  300,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := appendDebugTimelineItem(st, chat.ID, domain.AssistantMessage{
		Text:  "done",
		Usage: &domain.Usage{PromptTokens: 123, CompletionTokens: 4, CachedTokens: 100, TotalTokens: 127},
	}); err != nil {
		t.Fatal(err)
	}
	rec := NewRecorder()
	rec.RecordHTTP(HTTPTrace{ProviderID: "p", SessionID: session.ID, ChatID: chat.ID, RequestBody: `{"messages":[{"role":"user","content":"x"}]}`})
	srv := httptest.NewServer(Handler(st, rec))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/debug/sessions/" + string(session.ID) + "/chats/" + string(chat.ID))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(resp.Body)
		t.Fatalf("unexpected chat detail status %d: %s", resp.StatusCode, string(data))
	}
	var payload struct {
		LatestCompaction map[string]any `json:"latest_compaction"`
		LatestUsage      map[string]any `json:"latest_usage"`
		HTTPTraces       []HTTPTrace    `json:"http_traces"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if payload.LatestCompaction["first_kept_item_id"] != "kept-1" {
		t.Fatalf("expected latest compaction detail, got %#v", payload.LatestCompaction)
	}
	if payload.LatestUsage["usage"] == nil {
		t.Fatalf("expected latest usage detail, got %#v", payload.LatestUsage)
	}
	if len(payload.HTTPTraces) != 1 || payload.HTTPTraces[0].ChatID != chat.ID {
		t.Fatalf("expected matching HTTP trace, got %#v", payload.HTTPTraces)
	}
}

func TestServerRewindResolvesFirstCompactionError(t *testing.T) {
	t.Parallel()

	st, err := store.OpenWithOptions(t.TempDir(), store.Options{Backend: store.BackendJSONFS})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	session, err := modeltest.CreateSession(context.Background(), st, "debug", "provider", "model", nil)
	if err != nil {
		t.Fatal(err)
	}
	chat, err := modeltest.DefaultChat(context.Background(), st, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := appendDebugTimelineItem(st, chat.ID, domain.UserMessage{Text: "keep"}); err != nil {
		t.Fatal(err)
	}
	failed, err := appendDebugTimelineItem(st, chat.ID, domain.Compaction{Status: "failed", Trigger: "manual"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := appendDebugTimelineItem(st, chat.ID, domain.UserMessage{Text: "remove"}); err != nil {
		t.Fatal(err)
	}

	rewinder := &fakeChatRewinder{}
	debugServer := NewServer(st, NewRecorder())
	debugServer.SetChatRewinder(rewinder)
	mux := http.NewServeMux()
	debugServer.Register(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/debug/sessions/"+string(session.ID)+"/chats/"+string(chat.ID)+"/rewind", "application/json", strings.NewReader(`{"selector":"first_compaction_error"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("unexpected rewind status %d: %s", resp.StatusCode, body)
	}
	if rewinder.sessionID != session.ID || rewinder.chatID != chat.ID || rewinder.anchorID != failed.ID {
		t.Fatalf("rewinder got session=%s chat=%s anchor=%s, want session=%s chat=%s anchor=%s", rewinder.sessionID, rewinder.chatID, rewinder.anchorID, session.ID, chat.ID, failed.ID)
	}
}

func TestServerExposesSessionAnalysis(t *testing.T) {
	t.Parallel()

	st, err := store.OpenWithOptions(t.TempDir(), store.Options{Backend: store.BackendJSONFS})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	session, err := modeltest.CreateSession(context.Background(), st, "debug", "provider", "model", nil)
	if err != nil {
		t.Fatal(err)
	}
	chat, err := modeltest.DefaultChat(context.Background(), st, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	assistantStop, err := appendDebugTimelineItem(st, chat.ID, domain.AssistantMessage{Text: "Now update `CollidesWith`:"})
	if err != nil {
		t.Fatal(err)
	}
	toolMsg, err := appendDebugTimelineItem(st, chat.ID, domain.AssistantMessage{Tools: []domain.ToolCall{{
		ToolCallID: "call_1",
		Tool:       domain.ToolKindFileEdit,
		Args:       map[string]string{"path": "main.go"},
		Status:     domain.ToolStatusPending,
	}}})
	if err != nil {
		t.Fatal(err)
	}

	rec := NewRecorder()
	rec.RecordLifecycle(session.ID, "continue", "", nil)

	srv := httptest.NewServer(Handler(st, rec))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/debug/sessions/" + session.ID + "/analysis")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(resp.Body)
		t.Fatalf("unexpected analysis status %d: %s", resp.StatusCode, string(data))
	}
	var analysis SessionAnalysis
	if err := json.NewDecoder(resp.Body).Decode(&analysis); err != nil {
		t.Fatal(err)
	}
	if analysis.SessionID != session.ID {
		t.Fatalf("expected session id %s, got %#v", session.ID, analysis)
	}
	if analysis.ContinueCount != 1 || len(analysis.Continues) != 1 || analysis.Continues[0].Kind != "continue" {
		t.Fatalf("expected one continue event, got %#v", analysis)
	}
	if analysis.BadStopCount != 1 || len(analysis.BadStops) != 1 {
		t.Fatalf("expected one bad stop, got %#v", analysis)
	}
	if analysis.BadStops[0].MessageID != assistantStop.ID || analysis.BadStops[0].NextMessageID != toolMsg.ID {
		t.Fatalf("unexpected bad stop linkage %#v", analysis.BadStops[0])
	}
	if analysis.BadStops[0].NextTool != "FileEdit" {
		t.Fatalf("expected next tool edit, got %#v", analysis.BadStops[0])
	}
}

func TestServerExposesSessionHydrationDebug(t *testing.T) {
	t.Parallel()

	st, err := store.OpenWithOptions(t.TempDir(), store.Options{Backend: store.BackendJSONFS})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	session, err := modeltest.CreateSession(context.Background(), st, "debug", "provider", "model", nil)
	if err != nil {
		t.Fatal(err)
	}
	defaultChat, err := modeltest.DefaultChat(context.Background(), st, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	sideChat, err := modeltest.CreateChat(context.Background(), st, session.ID, "Side", "executor", &defaultChat.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := appendDebugTimelineItem(st, defaultChat.ID, domain.AssistantMessage{Tools: []domain.ToolCall{{
		ToolCallID: "call_1",
		Tool:       domain.ToolKindFileRead,
		Args:       map[string]string{"path": "main.go"},
		Status:     domain.ToolStatusPending,
	}}}); err != nil {
		t.Fatal(err)
	}
	if err := modeltest.SetChatQueuedInputs(context.Background(), st, sideChat.ID, []domain.QueuedInput{{Text: "queued"}}); err != nil {
		t.Fatal(err)
	}

	rec := NewRecorder()
	rec.RegisterClient(ClientDebug{ID: "client-1", SelectedSession: session.ID, SelectedChat: defaultChat.ID})
	rec.UpdateChats([]ChatDebug{{
		ID:               defaultChat.ID,
		SessionID:        session.ID,
		Title:            defaultChat.Title,
		Status:           "idle",
		QueueLen:         0,
		PendingApprovals: 0,
	}})

	srv := httptest.NewServer(Handler(st, rec))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/debug/sessions")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(resp.Body)
		t.Fatalf("unexpected sessions status %d: %s", resp.StatusCode, string(data))
	}
	var payload struct {
		Architecture ArchitectureDebug `json:"architecture"`
		Sessions     []SessionDebug    `json:"sessions"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if payload.Architecture.Summary == "" || len(payload.Architecture.MoreDataNeeded) == 0 {
		t.Fatalf("expected architecture guidance, got %#v", payload.Architecture)
	}
	if len(payload.Sessions) != 1 {
		t.Fatalf("expected one session debug entry, got %#v", payload.Sessions)
	}
	got := payload.Sessions[0]
	if !got.Hydrated || got.Hydration != "hydrated" || got.StoredChatCount != 2 || got.HydratedChatCount != 1 || got.SelectedClientCount != 1 {
		t.Fatalf("unexpected session hydration summary: %#v", got)
	}
	byID := map[id.ID]SessionChatDebug{}
	for _, chat := range got.Chats {
		byID[chat.ID] = chat
	}
	mainDebug := byID[defaultChat.ID]
	if !mainDebug.Hydrated || mainDebug.Runtime == nil || mainDebug.PendingExecutableToolCalls != 1 {
		t.Fatalf("unexpected hydrated main chat debug: %#v", mainDebug)
	}
	sideDebug := byID[sideChat.ID]
	if sideDebug.Hydrated || sideDebug.QueueLen != 1 || len(sideDebug.Diagnostics) == 0 {
		t.Fatalf("unexpected stored-only side chat debug: %#v", sideDebug)
	}
}

func appendDebugTimelineItem(st *store.Store, chatID id.ID, content domain.TimelineContent) (domain.TimelineItem, error) {
	item, err := modeltest.AppendTimeline(context.Background(), st, chatID, content)
	if err != nil {
		return domain.TimelineItem{}, err
	}
	item.Seal(item.UpdatedAt)
	if err := modeltest.PutTimelineItem(context.Background(), st, item); err != nil {
		return domain.TimelineItem{}, err
	}
	return item, nil
}

func TestServerExposesPprofHandlers(t *testing.T) {
	t.Parallel()

	st, err := store.OpenWithOptions(t.TempDir(), store.Options{Backend: store.BackendJSONFS})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	srv := httptest.NewServer(Handler(st, NewRecorder()))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/debug/pprof/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unexpected pprof status: %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "profile") {
		t.Fatalf("expected pprof index to mention profiles, got %q", string(body))
	}
}

func TestServerRuntimeCanToggleDeepDebug(t *testing.T) {
	t.Parallel()

	st, err := store.OpenWithOptions(t.TempDir(), store.Options{Backend: store.BackendJSONFS})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	rec := NewRecorder()
	srv := httptest.NewServer(Handler(st, rec))
	defer srv.Close()

	reqBody := strings.NewReader(`{"deep_debug":true}`)
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/debug/runtime", reqBody)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(resp.Body)
		t.Fatalf("unexpected runtime status %d: %s", resp.StatusCode, string(data))
	}
	var runtime RuntimeDebug
	if err := json.NewDecoder(resp.Body).Decode(&runtime); err != nil {
		t.Fatal(err)
	}
	if !runtime.DeepDebug {
		t.Fatalf("expected deep debug enabled, got %#v", runtime)
	}
	if !rec.DeepDebug() {
		t.Fatal("expected recorder deep debug flag to be enabled")
	}
}

func TestServerExposesClientsAndChats(t *testing.T) {
	t.Parallel()

	st, err := store.OpenWithOptions(t.TempDir(), store.Options{Backend: store.BackendJSONFS})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	rec := NewRecorder()
	rec.RegisterClient(ClientDebug{ID: "client-1", SelectedSession: "session-7", SelectedChat: "chat-9"})
	rec.UpdateChats([]ChatDebug{{ID: "chat-9", SessionID: "session-7", Status: "idle"}})
	srv := httptest.NewServer(Handler(st, rec))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/debug/clients")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var clients struct {
		Clients []ClientDebug `json:"clients"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&clients); err != nil {
		t.Fatal(err)
	}
	if len(clients.Clients) != 1 || clients.Clients[0].ID != "client-1" {
		t.Fatalf("unexpected clients response: %#v", clients)
	}

	resp, err = http.Get(srv.URL + "/debug/chats/chat-9")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var chat ChatDebug
	if err := json.NewDecoder(resp.Body).Decode(&chat); err != nil {
		t.Fatal(err)
	}
	if chat.ID != "chat-9" || chat.SessionID != "session-7" {
		t.Fatalf("unexpected chat response: %#v", chat)
	}
}

func TestRuntimeDebugSeparatesClientAndChatState(t *testing.T) {
	t.Parallel()

	recorder := NewRecorder()
	recorder.UpdateProcess(ProcessDebug{Status: "Web UI running"})
	recorder.RegisterClient(ClientDebug{
		ID:               "client-1",
		SelectedSession:  "session-7",
		SelectedChat:     "chat-9",
		DocumentVisible:  true,
		WindowFocused:    true,
		ComposerFocused:  true,
		ViewportWidth:    120,
		ViewportHeight:   40,
		StickToBottom:    true,
		OpenDialog:       "models",
		InterruptVisible: true,
		InterruptArmed:   true,
	})
	recorder.UpdateChats([]ChatDebug{{
		ID:                        "chat-9",
		SessionID:                 "session-7",
		Title:                     "Main",
		Status:                    "streaming_response",
		StatusText:                "Streaming LLM response ...",
		Active:                    true,
		Busy:                      true,
		QueueLen:                  2,
		PendingAssistantText:      17,
		PendingAssistantReasoning: 9,
		PendingApprovals:          1,
		RunningToolCalls:          3,
	}})

	runtime := recorder.Runtime()
	if runtime.Process.Status != "Web UI running" {
		t.Fatalf("unexpected process debug state: %#v", runtime.Process)
	}
	if len(runtime.Clients) != 1 || runtime.Clients[0].SelectedChat != "chat-9" || !runtime.Clients[0].ComposerFocused || runtime.Clients[0].OpenDialog != "models" {
		t.Fatalf("expected per-client state, got %#v", runtime.Clients)
	}
	if !runtime.Clients[0].InterruptVisible || !runtime.Clients[0].InterruptArmed {
		t.Fatalf("expected client interrupt button state, got %#v", runtime.Clients[0])
	}
	if len(runtime.Chats) != 1 || runtime.Chats[0].Status != "streaming_response" || runtime.Chats[0].QueueLen != 2 || runtime.Chats[0].RunningToolCalls != 3 {
		t.Fatalf("expected per-chat state, got %#v", runtime.Chats)
	}
	if runtime.Chats[0].PendingAssistantText != 17 || runtime.Chats[0].PendingAssistantReasoning != 9 {
		t.Fatalf("expected pending assistant lengths, got %#v", runtime.Chats[0])
	}
}
