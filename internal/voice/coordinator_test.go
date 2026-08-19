package voice

import (
	"context"
	"testing"
	"time"
)

type fakeBackend struct {
	sessions    []Session
	delegations []struct{ sessionID, text string }
	resultText  string
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

type fakeSummarizingBackend struct {
	fakeBackend
	summary string
	err     error
}

func (f *fakeSummarizingBackend) SummarizeVoiceResult(_ context.Context, request string, result DelegationResult) (string, error) {
	if request == "" || result.Text == "" {
		return "", context.Canceled
	}
	return f.summary, f.err
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
	resultText := f.resultText
	if resultText == "" {
		resultText = "The laptop now boots."
	}
	return DelegationResult{SessionID: sessionID, SessionTitle: "Laptop", ChatID: "chat-1", Text: resultText}, nil
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

func TestCallSpeaksSummaryButKeepsFullDelegatedResult(t *testing.T) {
	backend := &fakeSummarizingBackend{
		fakeBackend: fakeBackend{sessions: []Session{{ID: "laptop", Title: "Laptop"}}},
		summary:     "The laptop now boots after resetting its firmware setting.",
	}
	message, err := NewCall(backend).HandleText(context.Background(), "What fixed it?", "laptop")
	if err != nil {
		t.Fatal(err)
	}
	if message.SpokenText != backend.summary {
		t.Fatalf("spoken text = %q", message.SpokenText)
	}
	if len(message.Parts) == 0 || message.Parts[0].Data != "The laptop now boots." {
		t.Fatalf("visual parts = %#v", message.Parts)
	}
}

func TestCallStripsMarkdownFromSummaryFallback(t *testing.T) {
	backend := &fakeSummarizingBackend{
		fakeBackend: fakeBackend{
			sessions:   []Session{{ID: "laptop", Title: "Laptop"}},
			resultText: "# Result\n**The laptop** now `boots`.",
		},
		err: context.Canceled,
	}
	message, err := NewCall(backend).HandleText(context.Background(), "What fixed it?", "laptop")
	if err != nil {
		t.Fatal(err)
	}
	if message.SpokenText != "Result. The laptop now boots." {
		t.Fatalf("fallback spoken text = %q", message.SpokenText)
	}
}

func TestCallReportsWorkingOnlyWhenDelegationStarts(t *testing.T) {
	backend := &fakeBackend{sessions: []Session{{ID: "laptop", Title: "Laptop repair"}}}
	var working []Session
	message, err := NewCall(backend).HandleTextWithWorking(context.Background(), "Check it", "laptop", func(session Session) error {
		if len(backend.delegations) != 0 {
			t.Fatal("working callback arrived after delegation")
		}
		working = append(working, session)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(working) != 1 || working[0].ID != "laptop" || message.Delegation == nil {
		t.Fatalf("working=%#v message=%#v", working, message)
	}

	working = nil
	if _, err := NewCall(backend).HandleTextWithWorking(context.Background(), "What sessions are available?", "", func(session Session) error {
		working = append(working, session)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(working) != 0 {
		t.Fatalf("session listing reported working: %#v", working)
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
