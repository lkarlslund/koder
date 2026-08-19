package voice

import (
	"context"
	"testing"
	"time"
)

type fakeBackend struct {
	sessions []Session
}

func (f *fakeBackend) ListVoiceSessions(context.Context) ([]Session, error) {
	return append([]Session(nil), f.sessions...), nil
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
