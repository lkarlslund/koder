package voice

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestResponsePacingValidationAndLimits(t *testing.T) {
	for _, test := range []struct {
		wire  string
		want  ResponsePacing
		limit int
	}{
		{"", ResponsePacingNormal, 65},
		{"concise", ResponsePacingConcise, 30},
		{"DETAILED", ResponsePacingDetailed, 150},
	} {
		got, err := ParseResponsePacing(test.wire)
		if err != nil || got != test.want || got.MaxSpokenWords() != test.limit || !strings.Contains(got.Instruction(), "Response pacing") {
			t.Fatalf("ParseResponsePacing(%q) = %q, %v", test.wire, got, err)
		}
	}
	if _, err := ParseResponsePacing("essay"); err == nil {
		t.Fatal("expected unsupported response pacing to fail")
	}
}

type fakeBackend struct {
	sessions []Session
	history  []TranscriptEntry
	before   string
	limit    int
}

func (f *fakeBackend) ListVoiceSessions(context.Context) ([]Session, error) {
	return append([]Session(nil), f.sessions...), nil
}

func (f *fakeBackend) VoiceSessionHistory(_ context.Context, _ string, before string, limit int) (TranscriptPage, error) {
	f.before, f.limit = before, limit
	return TranscriptPage{Entries: append([]TranscriptEntry(nil), f.history...), HasMore: true}, nil
}

func TestCallStateSortsAndSelectsWorkSessions(t *testing.T) {
	backend := &fakeBackend{sessions: []Session{
		{ID: "older", Title: "Older", UpdatedAt: time.Now().Add(-time.Hour)},
		{ID: "newer", Title: "Newer", UpdatedAt: time.Now()},
	}}
	call := NewCall(backend)
	message, err := call.SelectSession(context.Background(), "older")
	if err != nil {
		t.Fatal(err)
	}
	state, err := call.State(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if state.ActiveSessionID != "older" || state.Sessions[0].ID != "newer" || message.SpokenText != "Using Older." {
		t.Fatalf("state=%#v message=%#v", state, message)
	}
}

func TestCallStateIncludesDurableVoiceHistory(t *testing.T) {
	backend := &fakeBackend{history: []TranscriptEntry{{ID: "one", Role: "user", Text: "What happened?"}}}
	state, err := NewCall(backend, "voice-1").State(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(state.History) != 1 || state.History[0].Text != "What happened?" || !state.HistoryHasMore || backend.limit != 5 || backend.before != "" {
		t.Fatalf("history = %#v", state.History)
	}
}

func TestCallRequestsOlderHistoryWithCursor(t *testing.T) {
	backend := &fakeBackend{history: []TranscriptEntry{{ID: "older"}}}
	page, err := NewCall(backend, "voice-1").History(context.Background(), "newer", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Entries) != 1 || backend.before != "newer" || backend.limit != 5 {
		t.Fatalf("page=%#v before=%q limit=%d", page, backend.before, backend.limit)
	}
}

func TestCallCanReturnToAutomaticSessionSelection(t *testing.T) {
	backend := &fakeBackend{sessions: []Session{{ID: "one", Title: "One"}}}
	call := NewCall(backend)
	if _, err := call.SelectSession(context.Background(), "one"); err != nil {
		t.Fatal(err)
	}
	message, err := call.SelectSession(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	state, err := call.State(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if state.ActiveSessionID != "" || message.SpokenText != "Automatic session selection is on." {
		t.Fatalf("state = %#v, message = %#v", state, message)
	}
}
