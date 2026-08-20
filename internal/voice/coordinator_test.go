package voice

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/lkarlslund/koder/internal/domain"
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
	sessions      []Session
	voiceSessions []Session
	chats         []Chat
	history       []TranscriptEntry
	before        string
	limit         int
}

func (f *fakeBackend) ListSessionChats(context.Context, string) ([]Chat, error) {
	return append([]Chat(nil), f.chats...), nil
}

func (f *fakeBackend) EnsureVoiceChat(_ context.Context, sessionID, chatID string) (Chat, error) {
	for _, chat := range f.chats {
		if chat.SessionID == sessionID && chat.ID == chatID && chat.Role == "voice" {
			return chat, nil
		}
	}
	return Chat{}, context.Canceled
}

func (f *fakeBackend) CreateVoiceChatInSession(_ context.Context, sessionID string, spec domain.ChatCreateSpec) (Chat, error) {
	chat := Chat{ID: "created-chat", SessionID: sessionID, Title: spec.Title, Role: "orchestrator", Backend: "koder", WorkflowRole: "orchestrator", InteractionMode: "voice"}
	f.chats = append(f.chats, chat)
	return chat, nil
}

func (f *fakeBackend) CreateTemporaryVoiceChat(_ context.Context, spec domain.ChatCreateSpec) (Session, Chat, error) {
	session := Session{ID: "temporary-session", Title: spec.Title, Kind: "quick"}
	chat := Chat{ID: "temporary-chat", SessionID: session.ID, Title: spec.Title, Role: "orchestrator", Backend: "koder", WorkflowRole: "orchestrator", InteractionMode: "voice"}
	f.sessions = append(f.sessions, session)
	f.chats = append(f.chats, chat)
	return session, chat, nil
}

func (f *fakeBackend) RunVoiceChatTurn(context.Context, string, string, string, TurnOptions, func(Session) error) (Message, error) {
	return Message{}, nil
}

func (f *fakeBackend) VoiceChatHistory(_ context.Context, _, _ string, before string, limit int) (TranscriptPage, error) {
	f.before, f.limit = before, limit
	return TranscriptPage{Entries: append([]TranscriptEntry(nil), f.history...), HasMore: true}, nil
}

func (f *fakeBackend) SearchVoiceChatHistory(context.Context, string, string, string, int) ([]TranscriptSearchResult, error) {
	return nil, nil
}

func (f *fakeBackend) ListVoiceSessions(context.Context) ([]Session, error) {
	return append([]Session(nil), f.sessions...), nil
}

func (f *fakeBackend) ListVoiceChats(context.Context) ([]Session, error) {
	return append([]Session(nil), f.voiceSessions...), nil
}

func (f *fakeBackend) EnsureVoiceSession(context.Context, string) (Session, error) {
	return Session{}, nil
}

func (f *fakeBackend) CreateVoiceSession(context.Context, string) (Session, error) {
	return Session{}, nil
}

func (f *fakeBackend) RenameVoiceSession(context.Context, string, string) (Session, error) {
	return Session{}, nil
}

func (f *fakeBackend) UpdateVoiceSession(context.Context, string, SessionUpdate) (Session, error) {
	return Session{}, nil
}

func (f *fakeBackend) DeleteVoiceSession(context.Context, string) error { return nil }

func (f *fakeBackend) RunVoiceTurn(context.Context, string, string, TurnOptions, func(Session) error) (Message, error) {
	return Message{}, nil
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
	if state.SessionID != "older" || state.Sessions[0].ID != "newer" || message.SpokenText != "Opened Older." {
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

func TestCallStateOnlyOffersActiveVoiceSessionsAndSortsPinnedFirst(t *testing.T) {
	now := time.Now().UTC()
	backend := &fakeBackend{voiceSessions: []Session{
		{ID: "newer", UpdatedAt: now},
		{ID: "pinned", Pinned: true, UpdatedAt: now.Add(-time.Hour)},
		{ID: "archived", Archived: true, UpdatedAt: now.Add(time.Hour)},
		{ID: "deleted", Deleted: true, UpdatedAt: now.Add(2 * time.Hour)},
	}}
	state, err := NewCall(backend).State(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(state.VoiceSessions) != 2 || state.VoiceSessions[0].ID != "pinned" || state.VoiceSessions[1].ID != "newer" {
		t.Fatalf("voice sessions = %#v", state.VoiceSessions)
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
	if state.SessionID != "" || message.SpokenText != "No session selected." {
		t.Fatalf("state = %#v, message = %#v", state, message)
	}
}

func TestNativeCallSelectsVoiceChatAndLoadsItsHistory(t *testing.T) {
	backend := &fakeBackend{
		sessions: []Session{{ID: "session-1", Title: "Laptop"}},
		chats: []Chat{
			{ID: "work", SessionID: "session-1", Title: "Firmware", Role: "execution"},
			{ID: "voice", SessionID: "session-1", Title: "Talk", Role: "voice"},
		},
		history: []TranscriptEntry{{ID: "turn", Role: "assistant", Text: "It boots."}},
	}
	call := NewSessionCall(backend, "session-1", "voice")
	state, err := call.State(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if state.SessionID != "session-1" || state.ChatID != "voice" || len(state.Chats) != 2 || len(state.History) != 1 {
		t.Fatalf("native state = %#v", state)
	}
	if _, err := call.SelectVoiceChat(context.Background(), "session-1", "work"); err == nil {
		t.Fatal("selected a non-voice chat")
	}
	created, err := call.CreateVoiceChat(context.Background(), "session-1", domain.ChatCreateSpec{Title: "Another conversation", InteractionMode: domain.InteractionModeVoice})
	if err != nil || created.ID != "created-chat" {
		t.Fatalf("created chat = %#v, %v", created, err)
	}
}
