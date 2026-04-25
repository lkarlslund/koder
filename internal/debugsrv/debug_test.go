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
	if _, err := st.AddPart(context.Background(), msg.ID, domain.PartKindText, "hello", ""); err != nil {
		t.Fatal(err)
	}

	rec := NewRecorder()
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
