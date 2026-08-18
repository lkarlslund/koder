package voice

import (
	"context"
	"testing"
	"time"
)

type fakeBackend struct {
	sessions    []Session
	delegations []struct{ sessionID, text string }
}

func (f *fakeBackend) ListVoiceSessions(context.Context) ([]Session, error) {
	return append([]Session(nil), f.sessions...), nil
}

func (f *fakeBackend) DelegateVoice(_ context.Context, sessionID, text string) (DelegationResult, error) {
	f.delegations = append(f.delegations, struct{ sessionID, text string }{sessionID, text})
	return DelegationResult{SessionID: sessionID, SessionTitle: "Laptop", ChatID: "chat-1", Text: "The laptop now boots."}, nil
}

func TestCallSelectsSessionByConversationSummaryThenDelegates(t *testing.T) {
	backend := &fakeBackend{sessions: []Session{
		{ID: "calendar", Title: "Calendar work", UpdatedAt: time.Now()},
		{ID: "laptop", Title: "Linux repair", LastMessage: "Debugged the broken laptop boot sequence", UpdatedAt: time.Now().Add(-time.Hour)},
	}}
	call := NewCall(backend)

	message, err := call.HandleText(context.Background(), "Let's pick up on that session where we debugged a laptop", "")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := message.SpokenText, "I found Linux repair. What should we do there?"; got != want {
		t.Fatalf("selection response = %q, want %q", got, want)
	}
	message, err = call.HandleText(context.Background(), "Check whether the fix still works", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(backend.delegations) != 1 || backend.delegations[0].sessionID != "laptop" {
		t.Fatalf("delegations = %#v, want one laptop delegation", backend.delegations)
	}
	if message.Delegation == nil || message.SpokenText != "The laptop now boots." {
		t.Fatalf("delegated message = %#v", message)
	}
}

func TestCallAsksForTargetWhenAmbiguous(t *testing.T) {
	backend := &fakeBackend{sessions: []Session{{ID: "one", Title: "One"}, {ID: "two", Title: "Two"}}}
	message, err := NewCall(backend).HandleText(context.Background(), "Please check my email", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(backend.delegations) != 0 {
		t.Fatalf("unexpected delegations: %#v", backend.delegations)
	}
	if message.SpokenText != "Which session should I use? Available sessions: One, Two." {
		t.Fatalf("response = %q", message.SpokenText)
	}
}

func TestCallUsesExplicitTarget(t *testing.T) {
	backend := &fakeBackend{sessions: []Session{{ID: "one", Title: "One"}, {ID: "two", Title: "Two"}}}
	_, err := NewCall(backend).HandleText(context.Background(), "Do the work", "two")
	if err != nil {
		t.Fatal(err)
	}
	if len(backend.delegations) != 1 || backend.delegations[0].sessionID != "two" {
		t.Fatalf("delegations = %#v", backend.delegations)
	}
}
