package debugsrv

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/store"
	"github.com/lkarlslund/koder/internal/version"
)

func TestRecorderTracksSessionEventsAndRuntime(t *testing.T) {
	t.Parallel()

	rec := NewRecorder()
	rec.RecordLifecycle("session-7", "prompt_submitted", "hello", map[string]string{"source": "web"})
	rec.RecordEvent("session-7", domain.Event{Kind: domain.EventKindToolResult, Text: "done"})
	rec.UpdateRuntime(RuntimeSnapshot{CurrentSession: "session-7", Status: "Ready", ViewportWidth: 80, MessageCount: 2, ViewportPreview: "hello"})

	events := rec.Events("session-7")
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
	if rec.Runtime().CurrentSession != "session-7" {
		t.Fatalf("unexpected runtime snapshot: %#v", rec.Runtime())
	}
	if rec.Runtime().ViewportWidth != 80 || rec.Runtime().MessageCount != 2 {
		t.Fatalf("expected runtime viewport details, got %#v", rec.Runtime())
	}
	if rec.Runtime().Build.Version != version.Version {
		t.Fatalf("expected runtime build version %q, got %#v", version.Version, rec.Runtime().Build)
	}
	if rec.Runtime().DeepDebug {
		t.Fatalf("expected deep debug off by default, got %#v", rec.Runtime())
	}
}

func TestServerExposesTranscriptAndEvents(t *testing.T) {
	t.Parallel()

	st, err := store.OpenWithOptions(t.TempDir(), store.Options{Backend: store.BackendJSONFS})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	session, err := st.CreateSession(context.Background(), "debug", "provider", "model", nil)
	if err != nil {
		t.Fatal(err)
	}
	chat, err := st.DefaultChat(context.Background(), session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := appendDebugTimelineItem(st, chat.ID, domain.UserMessage{Text: "hello"}); err != nil {
		t.Fatal(err)
	}

	rec := NewRecorder()
	rec.RecordLifecycle("", "startup_timing", "list_sessions", map[string]string{"duration_ms": "42"})
	rec.RecordEvent(session.ID, domain.Event{Kind: domain.EventKindMessageDelta, Text: "hello"})

	srv, err := Start("127.0.0.1:0", st, rec)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()

	resp, err := http.Get("http://" + srv.Addr() + "/debug/sessions/" + session.ID + "/transcript")
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

	resp, err = http.Get("http://" + srv.Addr() + "/debug/sessions/" + session.ID + "/events")
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

	resp, err = http.Get("http://" + srv.Addr() + "/debug/events")
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

	session, err := st.CreateSession(context.Background(), "debug", "provider", "model", nil)
	if err != nil {
		t.Fatal(err)
	}
	chat, err := st.DefaultChat(context.Background(), session.ID)
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

	srv, err := Start("127.0.0.1:0", st, rec)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()

	resp, err := http.Get("http://" + srv.Addr() + "/debug/sessions/" + session.ID + "/analysis")
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
	item, err := st.AppendTimeline(context.Background(), chatID, content)
	if err != nil {
		return domain.TimelineItem{}, err
	}
	item.Seal(item.UpdatedAt)
	if err := st.Timeline().Put(context.Background(), item); err != nil {
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

	srv, err := Start("127.0.0.1:0", st, NewRecorder())
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()

	resp, err := http.Get("http://" + srv.Addr() + "/debug/pprof/")
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

	srv, err := Start("127.0.0.1:0", st, NewRecorder())
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()

	reqBody := strings.NewReader(`{"deep_debug":true}`)
	req, err := http.NewRequest(http.MethodPost, "http://"+srv.Addr()+"/debug/runtime", reqBody)
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
	var runtime RuntimeSnapshot
	if err := json.NewDecoder(resp.Body).Decode(&runtime); err != nil {
		t.Fatal(err)
	}
	if !runtime.DeepDebug {
		t.Fatalf("expected deep debug enabled, got %#v", runtime)
	}
	if !srv.Recorder().DeepDebug() {
		t.Fatal("expected recorder deep debug flag to be enabled")
	}
}

func TestRuntimeSnapshotIncludesInteractiveState(t *testing.T) {
	t.Parallel()

	recorder := NewRecorder()
	recorder.UpdateRuntime(RuntimeSnapshot{
		CurrentSession:          "session-7",
		CurrentChat:             "chat-9",
		Busy:                    true,
		BusyStatus:              "Waiting for LLM response",
		Loading:                 true,
		ActiveEventStream:       true,
		RuntimeAttached:         true,
		RuntimeSubscribed:       true,
		RuntimeStatus:           "streaming_response",
		RuntimeStatusText:       "Streaming LLM response ...",
		RuntimeActive:           true,
		RuntimeQueueLen:         2,
		RuntimePendingText:      17,
		RuntimePendingReasoning: 9,
		TranscriptBusy:          true,
		SidebarBusy:             true,
		BusyScope:               "transcript",
		CanInterrupt:            true,
		HasActiveCancel:         true,
		HasChatCancel:           true,
		QueueEditMode:           false,
		FocusedWindow:           "main",
		ComposerFocused:         true,
		InterruptKeyTarget:      true,
	})

	runtime := recorder.Runtime()
	if runtime.CurrentSession != "session-7" || runtime.CurrentChat != "chat-9" {
		t.Fatalf("unexpected session/chat ids: %#v", runtime)
	}
	if !runtime.Loading || !runtime.ActiveEventStream {
		t.Fatalf("expected loading and event stream flags, got %#v", runtime)
	}
	if !runtime.RuntimeAttached || !runtime.RuntimeSubscribed || !runtime.RuntimeActive {
		t.Fatalf("expected runtime attachment state, got %#v", runtime)
	}
	if runtime.RuntimeStatus != "streaming_response" || runtime.RuntimeQueueLen != 2 || runtime.RuntimePendingText != 17 || runtime.RuntimePendingReasoning != 9 {
		t.Fatalf("expected runtime detail fields, got %#v", runtime)
	}
	if !runtime.CanInterrupt || !runtime.HasActiveCancel || !runtime.HasChatCancel {
		t.Fatalf("expected interrupt state flags, got %#v", runtime)
	}
	if runtime.FocusedWindow != "main" || !runtime.ComposerFocused || !runtime.InterruptKeyTarget {
		t.Fatalf("expected focus and interrupt target state, got %#v", runtime)
	}
}
