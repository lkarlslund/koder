package debugsrv

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lkarlslund/koder/internal/chatstore"
	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/sessionstore"
	"github.com/lkarlslund/koder/internal/store"
	"github.com/lkarlslund/koder/internal/version"
)

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

func TestServerExposesTranscriptAndEvents(t *testing.T) {
	t.Parallel()

	st, err := store.OpenWithOptions(t.TempDir(), store.Options{Backend: store.BackendJSONFS})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	session, err := sessionstore.CreateSession(context.Background(), st, "debug", "provider", "model", nil)
	if err != nil {
		t.Fatal(err)
	}
	chat, err := sessionstore.DefaultChat(context.Background(), st, session.ID)
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
		SessionID domain.ID       `json:"session_id"`
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

func TestServerExposesSessionAnalysis(t *testing.T) {
	t.Parallel()

	st, err := store.OpenWithOptions(t.TempDir(), store.Options{Backend: store.BackendJSONFS})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	session, err := sessionstore.CreateSession(context.Background(), st, "debug", "provider", "model", nil)
	if err != nil {
		t.Fatal(err)
	}
	chat, err := sessionstore.DefaultChat(context.Background(), st, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	assistantStop, err := appendDebugTimelineItem(st, chat.ID, domain.AssistantMessage{Text: "Now update `CollidesWith`:"})
	if err != nil {
		t.Fatal(err)
	}
	toolMsg, err := appendDebugTimelineItem(st, chat.ID, domain.AssistantMessage{Tools: []domain.ToolCall{{
		ToolCallID: "call_1",
		Tool:       domain.ToolKindEdit,
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
	if analysis.BadStops[0].NextTool != "edit" {
		t.Fatalf("expected next tool edit, got %#v", analysis.BadStops[0])
	}
}

func appendDebugTimelineItem(st *store.Store, chatID domain.ID, content domain.TimelineContent) (domain.TimelineItem, error) {
	item, err := chatstore.AppendTimeline(context.Background(), st, chatID, content)
	if err != nil {
		return domain.TimelineItem{}, err
	}
	item.Seal(item.UpdatedAt)
	if err := chatstore.PutTimelineItem(context.Background(), st, item); err != nil {
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
