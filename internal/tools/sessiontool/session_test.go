package sessiontool

import (
	"context"
	"strings"
	"testing"

	"github.com/lkarlslund/koder/internal/chatrole"
	"github.com/lkarlslund/koder/internal/tools"
	"github.com/lkarlslund/koder/internal/voice"
)

type fakeControl struct {
	listed    []voice.Session
	delegated struct{ sessionID, message string }
	created   struct {
		title      string
		persistent bool
	}
}

func (f *fakeControl) ListVoiceSessions(context.Context) ([]voice.Session, error) {
	return append([]voice.Session(nil), f.listed...), nil
}

func (f *fakeControl) DelegateVoice(_ context.Context, sessionID, message string) (voice.DelegationResult, error) {
	f.delegated.sessionID, f.delegated.message = sessionID, message
	return voice.DelegationResult{SessionID: sessionID, Text: "done"}, nil
}

func (f *fakeControl) CreateVoiceTarget(_ context.Context, title string, persistent bool) (voice.Session, error) {
	f.created.title, f.created.persistent = title, persistent
	return voice.Session{ID: "created", Title: title}, nil
}

func voiceRuntime(control Control) tools.Runtime {
	return tools.Runtime{ChatRole: chatrole.Voice, Services: RuntimeService(control)}
}

func TestDefinitionsAreNotOfferedToVoiceChats(t *testing.T) {
	control := &fakeControl{}
	for _, toolID := range []tools.ID{tools.Sessions, tools.SessionList, tools.SessionDelegate, tools.SessionStart} {
		if !tools.IsBuiltinID(toolID) {
			t.Fatalf("legacy voice session tool %q is not registered as built in", toolID)
		}
		if definition, enabled := tools.DefinitionFor(toolID, voiceRuntime(control)); enabled {
			t.Fatalf("legacy tool %q offered to voice chat: %#v", toolID, definition)
		}
	}
}

func TestSessionToolsRejectVoiceCalls(t *testing.T) {
	runtime := voiceRuntime(&fakeControl{})
	for _, toolID := range []tools.ID{tools.Sessions, tools.SessionList, tools.SessionDelegate, tools.SessionStart} {
		_, err := tools.Call(context.Background(), tools.Options{
			Runtime: runtime,
			Request: tools.Request{Tool: toolID, Args: map[string]string{
				"action": "list", "session_id": "laptop", "message": "check it", "title": "One-off task", "temporary": "true",
			}},
		})
		if err == nil || !strings.Contains(err.Error(), "not available to voice chats") {
			t.Fatalf("legacy tool %q error = %v", toolID, err)
		}
	}
}

func TestSessionToolsRejectIncompleteArguments(t *testing.T) {
	if _, err := (delegateTool{}).NormalizeArgs(map[string]string{"session_id": "one"}); err == nil {
		t.Fatal("expected missing delegation message error")
	}
	if _, err := (startTool{}).NormalizeArgs(map[string]string{"title": "one", "temporary": "sometimes"}); err == nil {
		t.Fatal("expected invalid temporary flag error")
	}
}
