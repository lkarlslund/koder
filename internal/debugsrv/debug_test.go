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
	"github.com/lkarlslund/koder/internal/ui"
	"github.com/lkarlslund/koder/internal/version"
)

func TestRecorderTracksSessionEventsAndRuntime(t *testing.T) {
	t.Parallel()

	rec := NewRecorder()
	rec.RecordLifecycle(7, "prompt_submitted", "hello", map[string]string{"source": "tui"})
	rec.RecordEvent(7, domain.Event{Kind: domain.EventKindToolResult, Text: "done"})
	rec.UpdateRuntime(RuntimeSnapshot{CurrentSession: 7, Status: "Ready", ViewportWidth: 80, MessageCount: 2, ViewportPreview: "hello"})

	events := rec.Events(7)
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
	if rec.Runtime().CurrentSession != 7 {
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
	msg, err := st.AddMessage(context.Background(), session.ID, domain.MessageRoleUser, "hello")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddPart(context.Background(), msg.ID, domain.TextPayload{Text: "hello"}); err != nil {
		t.Fatal(err)
	}

	rec := NewRecorder()
	rec.RecordLifecycle(0, "startup_timing", "list_sessions", map[string]string{"duration_ms": "42"})
	rec.RecordEvent(session.ID, domain.Event{Kind: domain.EventKindMessageDelta, Text: "hello"})

	srv, err := Start("127.0.0.1:0", st, rec)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()

	resp, err := http.Get("http://" + srv.Addr() + "/debug/sessions/" + "1/transcript")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unexpected transcript status: %d", resp.StatusCode)
	}
	var transcript struct {
		Messages []struct {
			Message domain.Message `json:"message"`
			Parts   []domain.Part  `json:"parts"`
		} `json:"messages"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&transcript); err != nil {
		t.Fatal(err)
	}
	if len(transcript.Messages) != 1 || len(transcript.Messages[0].Parts) != 1 {
		t.Fatalf("unexpected transcript payload: %#v", transcript)
	}

	resp, err = http.Get("http://" + srv.Addr() + "/debug/sessions/1/events")
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
		SessionID int64           `json:"session_id"`
		Events    []RecordedEvent `json:"events"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&global); err != nil {
		t.Fatal(err)
	}
	if global.SessionID != 0 {
		t.Fatalf("expected global session id 0, got %d", global.SessionID)
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
	assistantStop, err := st.AddMessage(context.Background(), session.ID, domain.MessageRoleAssistant, "Now update `CollidesWith`:")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddPart(context.Background(), assistantStop.ID, domain.TextPayload{Text: "Now update `CollidesWith`:"}); err != nil {
		t.Fatal(err)
	}
	toolMsg, err := st.AddMessage(context.Background(), session.ID, domain.MessageRoleAssistant, "edit")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddPart(context.Background(), toolMsg.ID, domain.ToolCallPayload{Tool: domain.ToolKindEdit, Args: map[string]string{"path": "main.go"}}); err != nil {
		t.Fatal(err)
	}

	rec := NewRecorder()
	rec.RecordLifecycle(session.ID, "continue", "", nil)

	srv, err := Start("127.0.0.1:0", st, rec)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()

	resp, err := http.Get("http://" + srv.Addr() + "/debug/sessions/1/analysis")
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
		t.Fatalf("expected session id %d, got %#v", session.ID, analysis)
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

func TestServerAcceptsLiveInput(t *testing.T) {
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

	received := make(chan ui.Msg, 1)
	srv.SetInputSink(func(msg ui.Msg) {
		received <- msg
	})

	body := strings.NewReader(`{"mouse":{"x":12,"y":7,"button":"left","action":"press"}}`)
	resp, err := http.Post("http://"+srv.Addr()+"/debug/input", "application/json", body)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(resp.Body)
		t.Fatalf("unexpected input status %d: %s", resp.StatusCode, string(data))
	}

	select {
	case msg := <-received:
		mouse, ok := msg.(ui.MouseMsg)
		if !ok {
			t.Fatalf("expected mouse msg, got %#v", msg)
		}
		if mouse.X != 12 || mouse.Y != 7 || mouse.Button != ui.MouseButtonLeft || mouse.Action != ui.MouseActionPress {
			t.Fatalf("unexpected mouse msg %#v", mouse)
		}
	default:
		t.Fatal("expected live input sink to receive a message")
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
		CurrentSession:          7,
		CurrentChat:             9,
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
	if runtime.CurrentSession != 7 || runtime.CurrentChat != 9 {
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
