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

type fakeRoutingBackend struct {
	fakeBackend
	decision RouteDecision
	routeErr error
	request  RouteRequest
	created  []struct {
		title      string
		persistent bool
	}
}

func (f *fakeRoutingBackend) ResolveVoiceRoute(_ context.Context, request RouteRequest) (RouteDecision, error) {
	f.request = request
	return f.decision, f.routeErr
}

func (f *fakeRoutingBackend) CreateVoiceTarget(_ context.Context, title string, persistent bool) (Session, error) {
	f.created = append(f.created, struct {
		title      string
		persistent bool
	}{title, persistent})
	created := Session{ID: "created", Title: title}
	f.sessions = append(f.sessions, created)
	return created, nil
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

func TestCallUsesSemanticRouteAndDelegates(t *testing.T) {
	backend := &fakeRoutingBackend{
		fakeBackend: fakeBackend{sessions: []Session{
			{ID: "mail", Title: "Inbox maintenance"},
			{ID: "laptop", Title: "Linux repair"},
		}},
		decision: RouteDecision{Action: RouteExisting, SessionID: "mail", Delegate: true},
	}
	message, err := NewCall(backend).HandleText(context.Background(), "See whether Steen replied", "")
	if err != nil {
		t.Fatal(err)
	}
	if backend.request.Text != "See whether Steen replied" || backend.request.ActiveSessionID != "" {
		t.Fatalf("route request = %#v", backend.request)
	}
	if len(backend.delegations) != 1 || backend.delegations[0].sessionID != "mail" {
		t.Fatalf("delegations = %#v", backend.delegations)
	}
	if message.Delegation == nil {
		t.Fatalf("message = %#v, want delegation", message)
	}
}

func TestCallCreatesTemporaryTargetFromRoute(t *testing.T) {
	backend := &fakeRoutingBackend{
		decision: RouteDecision{Action: RouteNewTemporary, Title: "Appointment with Steen", Delegate: true},
	}
	_, err := NewCall(backend).HandleText(context.Background(), "I have an appointment tomorrow at ten with Steen", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(backend.created) != 1 || backend.created[0].title != "Appointment with Steen" || backend.created[0].persistent {
		t.Fatalf("created targets = %#v", backend.created)
	}
	if len(backend.delegations) != 1 || backend.delegations[0].sessionID != "created" {
		t.Fatalf("delegations = %#v", backend.delegations)
	}
}

func TestCallCreatesPersistentTargetWithoutDelegatingSelectionCommand(t *testing.T) {
	backend := &fakeRoutingBackend{
		decision: RouteDecision{Action: RouteNewPersistent, Title: "Travel planning", Delegate: false},
	}
	message, err := NewCall(backend).HandleText(context.Background(), "Start a persistent travel planning session", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(backend.created) != 1 || !backend.created[0].persistent {
		t.Fatalf("created targets = %#v", backend.created)
	}
	if len(backend.delegations) != 0 || message.SpokenText != "Created Travel planning. What should we do there?" {
		t.Fatalf("message = %#v, delegations = %#v", message, backend.delegations)
	}
}

func TestCallRejectsUnknownSemanticSession(t *testing.T) {
	backend := &fakeRoutingBackend{
		fakeBackend: fakeBackend{sessions: []Session{{ID: "known", Title: "Known"}}},
		decision:    RouteDecision{Action: RouteExisting, SessionID: "invented", Delegate: true},
	}
	if _, err := NewCall(backend).HandleText(context.Background(), "Do something", ""); err == nil {
		t.Fatal("expected invalid route error")
	}
}
