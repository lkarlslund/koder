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
	"github.com/lkarlslund/koder/internal/planning"
	"github.com/lkarlslund/koder/internal/version"
)

type fakeSource struct {
	sessions        []SessionDebug
	sessionDetails  map[id.ID]SessionDetail
	chatDetails     map[string]ChatDetail
	transcripts     map[string][]domain.TimelineItem
	defaults        map[id.ID][]domain.TimelineItem
	approvals       map[id.ID][]DebugApproval
	plans           map[id.ID]planning.Plan
	todos           map[id.ID][]planning.TodoItem
	tasks           map[id.ID][]planning.Task
	rewindSessionID id.ID
	rewindChatID    id.ID
	rewindAnchorID  id.ID
}

func (f *fakeSource) DebugSessions(context.Context, RuntimeDebug) ([]SessionDebug, error) {
	return f.sessions, nil
}

func (f *fakeSource) DebugSession(_ context.Context, sessionID id.ID, _ RuntimeDebug) (SessionDetail, error) {
	if f.sessionDetails != nil {
		if detail, ok := f.sessionDetails[sessionID]; ok {
			return detail, nil
		}
	}
	return SessionDetail{}, fmt.Errorf("session %s is not loaded", sessionID)
}

func (f *fakeSource) DebugChat(_ context.Context, sessionID, chatID id.ID, _ RuntimeDebug) (ChatDetail, error) {
	if f.chatDetails != nil {
		if detail, ok := f.chatDetails[debugChatKey(sessionID, chatID)]; ok {
			return detail, nil
		}
	}
	return ChatDetail{}, fmt.Errorf("chat %s does not belong to session %s", chatID, sessionID)
}

func (f *fakeSource) ChatTranscript(_ context.Context, sessionID, chatID id.ID, opts TranscriptOptions) ([]domain.TimelineItem, error) {
	items, ok := f.transcripts[debugChatKey(sessionID, chatID)]
	if !ok {
		return nil, fmt.Errorf("chat %s does not belong to session %s", chatID, sessionID)
	}
	return sliceFakeTranscript(items, opts), nil
}

func (f *fakeSource) DefaultTranscript(_ context.Context, sessionID id.ID, opts TranscriptOptions) ([]domain.TimelineItem, error) {
	items, ok := f.defaults[sessionID]
	if !ok {
		return nil, fmt.Errorf("session %s has no default transcript", sessionID)
	}
	return sliceFakeTranscript(items, opts), nil
}

func (f *fakeSource) SessionApprovals(_ context.Context, sessionID id.ID) ([]DebugApproval, error) {
	return f.approvals[sessionID], nil
}

func (f *fakeSource) Milestones(_ context.Context, sessionID id.ID) (planning.Plan, error) {
	return f.plans[sessionID], nil
}

func (f *fakeSource) Todos(_ context.Context, sessionID id.ID) ([]planning.TodoItem, error) {
	return f.todos[sessionID], nil
}

func (f *fakeSource) Tasks(_ context.Context, sessionID id.ID) ([]planning.Task, error) {
	return f.tasks[sessionID], nil
}

func (f *fakeSource) ResolveRewindAnchor(ctx context.Context, sessionID, chatID id.ID, selector string) (id.ID, error) {
	if strings.TrimSpace(selector) == "" {
		selector = "first_compaction_error"
	}
	if selector != "first_compaction_error" {
		return "", fmt.Errorf("unsupported rewind selector %q", selector)
	}
	items, err := f.ChatTranscript(ctx, sessionID, chatID, TranscriptOptions{All: true})
	if err != nil {
		return "", err
	}
	for _, item := range items {
		compaction, ok := item.Content.(domain.Compaction)
		if ok && compaction.Status == "failed" {
			return item.ID, nil
		}
	}
	return "", fmt.Errorf("chat %s has no failed compaction item", chatID)
}

func (f *fakeSource) RewindLiveChat(_ context.Context, sessionID, chatID, anchorItemID id.ID) (any, error) {
	f.rewindSessionID = sessionID
	f.rewindChatID = chatID
	f.rewindAnchorID = anchorItemID
	return map[string]any{"removed_count": 3}, nil
}

func debugChatKey(sessionID, chatID id.ID) string {
	return string(sessionID) + "/" + string(chatID)
}

func sliceFakeTranscript(items []domain.TimelineItem, opts TranscriptOptions) []domain.TimelineItem {
	if opts.All || opts.Limit <= 0 || len(items) <= opts.Limit {
		out := make([]domain.TimelineItem, len(items))
		copy(out, items)
		return out
	}
	if opts.Tail {
		out := make([]domain.TimelineItem, opts.Limit)
		copy(out, items[len(items)-opts.Limit:])
		return out
	}
	out := make([]domain.TimelineItem, opts.Limit)
	copy(out, items[:opts.Limit])
	return out
}

func debugTimelineItem(chatID id.ID, seq int64, content domain.TimelineContent) domain.TimelineItem {
	item := domain.TimelineItem{
		ID:      id.ID(fmt.Sprintf("item-%d", seq)),
		ChatID:  chatID,
		Seq:     seq,
		Content: content,
	}
	item.Seal(item.UpdatedAt)
	return item
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

	sessionID := id.ID("session-debug")
	chatID := id.ID("chat-main")
	source := &fakeSource{chatDetails: map[string]ChatDetail{
		debugChatKey(sessionID, chatID): {Chat: SessionChatDebug{ID: chatID, SessionID: sessionID, Hydration: "live", Hydrated: true}},
	}}

	rec := NewRecorder()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	activeCtx, requestID, finish := rec.StartHTTP(ctx, HTTPTrace{
		ProviderID:  "llamacpp",
		SessionID:   sessionID,
		ChatID:      chatID,
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

	srv := httptest.NewServer(Handler(source, rec))
	defer srv.Close()

	url := srv.URL + "/debug/sessions/" + string(sessionID) + "/chats/" + string(chatID) + "/http/active"
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

	sessionID := id.ID("session-debug")
	chatID := id.ID("chat-main")
	source := &fakeSource{defaults: map[id.ID][]domain.TimelineItem{
		sessionID: {debugTimelineItem(chatID, 1, domain.UserMessage{Text: "hello"})},
	}}

	rec := NewRecorder()
	rec.RecordLifecycle("", "startup_timing", "list_sessions", map[string]string{"duration_ms": "42"})
	rec.RecordEvent(sessionID, domain.Event{Kind: domain.EventKindMessageDelta, Text: "hello"})

	srv := httptest.NewServer(Handler(source, rec))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/debug/sessions/" + string(sessionID) + "/transcript")
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

	resp, err = http.Get(srv.URL + "/debug/sessions/" + string(sessionID) + "/events")
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

	sessionID := id.ID("session-debug")
	defaultChatID := id.ID("chat-main")
	sideChatID := id.ID("chat-side")
	source := &fakeSource{
		chatDetails: map[string]ChatDetail{
			debugChatKey(sessionID, sideChatID): {Chat: SessionChatDebug{ID: sideChatID, SessionID: sessionID, Hydration: "live", Hydrated: true}},
		},
		transcripts: map[string][]domain.TimelineItem{
			debugChatKey(sessionID, sideChatID): {
				debugTimelineItem(sideChatID, 1, domain.UserMessage{Text: "side-one"}),
				debugTimelineItem(sideChatID, 2, domain.UserMessage{Text: "side-two"}),
			},
		},
	}

	rec := NewRecorder()
	rec.RecordHTTP(HTTPTrace{ProviderID: "p", SessionID: sessionID, ChatID: defaultChatID, RequestBody: `{"messages":[{"role":"user","content":"default"}]}`})
	rec.RecordHTTP(HTTPTrace{ProviderID: "p", SessionID: sessionID, ChatID: sideChatID, RequestBody: `{"messages":[{"role":"user","content":"side"}]}`})
	srv := httptest.NewServer(Handler(source, rec))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/debug/sessions/" + string(sessionID) + "/chats/" + string(sideChatID) + "/transcript?tail=true&limit=1")
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
	if transcript.ChatID != sideChatID || len(transcript.Timeline) != 1 {
		t.Fatalf("unexpected transcript response: %#v", transcript)
	}
	user, ok := transcript.Timeline[0].Content.(domain.UserMessage)
	if !ok || user.Text != "side-two" {
		t.Fatalf("expected side chat tail item, got %#v", transcript.Timeline)
	}

	resp, err = http.Get(srv.URL + "/debug/sessions/" + string(sessionID) + "/chats/" + string(sideChatID) + "/http")
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
	if len(traces.Traces) != 1 || traces.Traces[0].ChatID != sideChatID || !strings.Contains(traces.Traces[0].RequestBody, "side") {
		t.Fatalf("expected side chat HTTP trace, got %#v", traces.Traces)
	}

	resp, err = http.Get(srv.URL + "/debug/sessions/" + string(sessionID) + "/chats/not-a-chat/http")
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

	sessionID := id.ID("session-debug")
	chatID := id.ID("chat-main")
	timeline := []domain.TimelineItem{
		debugTimelineItem(chatID, 1, domain.Compaction{
			Status:              "completed",
			Summary:             "summary",
			FirstKeptItemID:     "kept-1",
			BeforeContextTokens: 1000,
			AfterContextTokens:  300,
		}),
		debugTimelineItem(chatID, 2, domain.AssistantMessage{
			Text:  "done",
			Usage: &domain.Usage{PromptTokens: 123, CompletionTokens: 4, CachedTokens: 100, TotalTokens: 127},
		}),
	}
	source := &fakeSource{chatDetails: map[string]ChatDetail{
		debugChatKey(sessionID, chatID): {
			Chat:             SessionChatDebug{ID: chatID, SessionID: sessionID, Hydration: "live", Hydrated: true},
			Timeline:         timeline,
			LatestCompaction: LatestCompactionDebug(timeline),
			LatestUsage:      LatestUsageDebug(timeline),
		},
	}}
	rec := NewRecorder()
	rec.RecordHTTP(HTTPTrace{ProviderID: "p", SessionID: sessionID, ChatID: chatID, RequestBody: `{"messages":[{"role":"user","content":"x"}]}`})
	srv := httptest.NewServer(Handler(source, rec))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/debug/sessions/" + string(sessionID) + "/chats/" + string(chatID))
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
	if len(payload.HTTPTraces) != 1 || payload.HTTPTraces[0].ChatID != chatID {
		t.Fatalf("expected matching HTTP trace, got %#v", payload.HTTPTraces)
	}
}

func TestServerRewindResolvesFirstCompactionError(t *testing.T) {
	t.Parallel()

	sessionID := id.ID("session-debug")
	chatID := id.ID("chat-main")
	failed := debugTimelineItem(chatID, 2, domain.Compaction{Status: "failed", Trigger: "manual"})
	source := &fakeSource{transcripts: map[string][]domain.TimelineItem{
		debugChatKey(sessionID, chatID): {
			debugTimelineItem(chatID, 1, domain.UserMessage{Text: "keep"}),
			failed,
			debugTimelineItem(chatID, 3, domain.UserMessage{Text: "remove"}),
		},
	}}
	debugServer := NewServer(source, NewRecorder())
	mux := http.NewServeMux()
	debugServer.Register(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/debug/sessions/"+string(sessionID)+"/chats/"+string(chatID)+"/rewind", "application/json", strings.NewReader(`{"selector":"first_compaction_error"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("unexpected rewind status %d: %s", resp.StatusCode, body)
	}
	if source.rewindSessionID != sessionID || source.rewindChatID != chatID || source.rewindAnchorID != failed.ID {
		t.Fatalf("rewinder got session=%s chat=%s anchor=%s, want session=%s chat=%s anchor=%s", source.rewindSessionID, source.rewindChatID, source.rewindAnchorID, sessionID, chatID, failed.ID)
	}
}

func TestServerExposesSessionAnalysis(t *testing.T) {
	t.Parallel()

	sessionID := id.ID("session-debug")
	chatID := id.ID("chat-main")
	assistantStop := debugTimelineItem(chatID, 1, domain.AssistantMessage{Text: "Now update `CollidesWith`:"})
	toolMsg := debugTimelineItem(chatID, 2, domain.AssistantMessage{Tools: []domain.ToolCall{{
		ToolCallID: "call_1",
		Tool:       domain.ToolKindFileEdit,
		Args:       map[string]string{"path": "main.go"},
		Status:     domain.ToolStatusPending,
	}}})
	source := &fakeSource{defaults: map[id.ID][]domain.TimelineItem{sessionID: {assistantStop, toolMsg}}}

	rec := NewRecorder()
	rec.RecordLifecycle(sessionID, "continue", "", nil)

	srv := httptest.NewServer(Handler(source, rec))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/debug/sessions/" + string(sessionID) + "/analysis")
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
	if analysis.SessionID != sessionID {
		t.Fatalf("expected session id %s, got %#v", sessionID, analysis)
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

	sessionID := id.ID("session-debug")
	defaultChatID := id.ID("chat-main")
	sideChatID := id.ID("chat-side")

	rec := NewRecorder()
	rec.RegisterClient(ClientDebug{ID: "client-1", SelectedSession: sessionID, SelectedChat: defaultChatID})
	rec.UpdateChats([]ChatDebug{{
		ID:               defaultChatID,
		SessionID:        sessionID,
		Title:            "Main",
		Status:           "idle",
		QueueLen:         0,
		PendingApprovals: 0,
	}})
	source := &fakeSource{sessions: []SessionDebug{{
		ID:                  sessionID,
		Title:               "debug",
		ProjectRoot:         "/workspace",
		Hydration:           "live",
		Hydrated:            true,
		StoredChatCount:     2,
		HydratedChatCount:   2,
		VisibleChatCount:    2,
		SelectedClientCount: 1,
		Record:              domain.Session{ID: sessionID, Title: "debug", ProjectRoot: "/workspace"},
		Chats: []SessionChatDebug{
			{
				ID:                         defaultChatID,
				SessionID:                  sessionID,
				Title:                      "Main",
				Hydration:                  "live",
				Hydrated:                   true,
				PendingExecutableToolCalls: 1,
				Runtime:                    &ChatDebug{ID: defaultChatID, SessionID: sessionID, Status: "idle"},
			},
			{
				ID:        sideChatID,
				SessionID: sessionID,
				Title:     "Side",
				Hydration: "live",
				Hydrated:  true,
				QueueLen:  1,
			},
		},
	}}}
	srv := httptest.NewServer(Handler(source, rec))
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
	if !got.Hydrated || got.Hydration != "live" || got.StoredChatCount != 2 || got.HydratedChatCount != 2 || got.SelectedClientCount != 1 {
		t.Fatalf("unexpected session hydration summary: %#v", got)
	}
	byID := map[id.ID]SessionChatDebug{}
	for _, chat := range got.Chats {
		byID[chat.ID] = chat
	}
	mainDebug := byID[defaultChatID]
	if !mainDebug.Hydrated || mainDebug.Runtime == nil || mainDebug.PendingExecutableToolCalls != 1 {
		t.Fatalf("unexpected hydrated main chat debug: %#v", mainDebug)
	}
	sideDebug := byID[sideChatID]
	if !sideDebug.Hydrated || sideDebug.QueueLen != 1 {
		t.Fatalf("unexpected side chat debug: %#v", sideDebug)
	}
}

func TestServerExposesPprofHandlers(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(Handler(nil, NewRecorder()))
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

	rec := NewRecorder()
	srv := httptest.NewServer(Handler(nil, rec))
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

	rec := NewRecorder()
	rec.RegisterClient(ClientDebug{ID: "client-1", SelectedSession: "session-7", SelectedChat: "chat-9"})
	rec.UpdateChats([]ChatDebug{{ID: "chat-9", SessionID: "session-7", Status: "idle"}})
	srv := httptest.NewServer(Handler(nil, rec))
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
