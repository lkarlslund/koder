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

func TestDefinitionsAreVoiceOnlyAndCarryUsageInstructions(t *testing.T) {
	control := &fakeControl{}
	for _, toolID := range []tools.ID{tools.SessionList, tools.SessionDelegate, tools.SessionStart} {
		if !tools.IsBuiltinID(toolID) {
			t.Fatalf("voice session tool %q is not registered as built in", toolID)
		}
		definition, enabled := tools.DefinitionFor(toolID, voiceRuntime(control))
		if !enabled || strings.TrimSpace(definition.Function.Description) == "" {
			t.Fatalf("voice definition %q = %#v, enabled=%v", toolID, definition, enabled)
		}
		if _, enabled := tools.DefinitionFor(toolID, tools.Runtime{ChatRole: chatrole.Execution, Services: RuntimeService(control)}); enabled {
			t.Fatalf("tool %q offered outside voice profile", toolID)
		}
	}
}

func TestSessionToolsCallInjectedControl(t *testing.T) {
	control := &fakeControl{listed: []voice.Session{{ID: "laptop", Title: "Laptop"}}}
	runtime := voiceRuntime(control)

	listed, err := tools.Call(context.Background(), tools.Options{
		Runtime: runtime, Request: tools.Request{Tool: tools.SessionList},
	})
	if err != nil || !strings.Contains(listed.Output, `"id":"laptop"`) {
		t.Fatalf("list result=%#v err=%v", listed, err)
	}
	delegated, err := tools.Call(context.Background(), tools.Options{
		Runtime: runtime, Request: tools.Request{Tool: tools.SessionDelegate, Args: map[string]string{
			"session_id": " laptop ", "message": " check it ",
		}},
	})
	if err != nil || control.delegated.sessionID != "laptop" || control.delegated.message != "check it" || !strings.Contains(delegated.Output, `"text":"done"`) {
		t.Fatalf("delegated=%#v call=%#v err=%v", delegated, control.delegated, err)
	}
	started, err := tools.Call(context.Background(), tools.Options{
		Runtime: runtime, Request: tools.Request{Tool: tools.SessionStart, Args: map[string]string{
			"title": " One-off task ", "temporary": "true",
		}},
	})
	if err != nil || control.created.title != "One-off task" || control.created.persistent || !strings.Contains(started.Output, `"id":"created"`) {
		t.Fatalf("started=%#v call=%#v err=%v", started, control.created, err)
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
