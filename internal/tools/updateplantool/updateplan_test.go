package updateplantool

import (
	"context"
	"strings"
	"testing"

	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/modeltest"
	"github.com/lkarlslund/koder/internal/store"
	"github.com/lkarlslund/koder/internal/tools"
	"github.com/lkarlslund/koder/internal/tools/tooltest"
)

func openPlanStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.OpenWithOptions(t.TempDir(), store.Options{Backend: store.BackendJSONFS})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func TestNormalizeArgsValidatesPlan(t *testing.T) {
	if _, err := (tool{}).NormalizeArgs(map[string]string{}); err == nil {
		t.Fatal("expected empty plan error")
	}
	if _, err := (tool{}).NormalizeArgs(map[string]string{"plan": `[{"step":"one","status":"in_progress"},{"step":"two","status":"in_progress"}]`}); err == nil {
		t.Fatal("expected multiple in_progress validation error")
	}
}

func TestExecuteFormatsPlan(t *testing.T) {
	result, err := tool{}.Execute(context.Background(), tools.Runtime{}, tools.Request{
		Args: map[string]string{
			"explanation": "Do the work",
			"plan":        `[{"step":"Inspect","status":"completed"},{"step":"Implement","status":"in_progress"}]`,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.Output, "Do the work") || !strings.Contains(result.Output, "[completed] Inspect") {
		t.Fatalf("unexpected plan output: %q", result.Output)
	}
}

func TestPersistResultStoresPlanUpdate(t *testing.T) {
	st := openPlanStore(t)
	session, err := modeltest.CreateSession(context.Background(), st, "test", "provider", "model", nil)
	if err != nil {
		t.Fatal(err)
	}
	chat, err := modeltest.DefaultChat(context.Background(), st, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := modeltest.AppendTimeline(context.Background(), st, chat.ID, domain.AssistantMessage{
		Tools: []domain.ToolCall{{
			ToolCallID: "call_plan",
			Tool:       domain.ToolKindUpdatePlan,
			Status:     domain.ToolStatusPending,
		}},
	}); err != nil {
		t.Fatal(err)
	}
	runtime := tools.Runtime{Store: st, SessionID: session.ID, ChatID: chat.ID, SessionControl: tooltest.NewSessionControl(st)}
	events, err := tool{}.PersistResult(context.Background(), runtime, tools.Request{
		Tool:       domain.ToolKindUpdatePlan,
		ToolCallID: "call_plan",
		Args: map[string]string{
			"plan":        `[{"step":"Inspect","status":"completed"}]`,
			"explanation": "Done",
		},
	}, tools.Result{
		Output: "Done\n[completed] Inspect",
		Meta:   map[string]string{"step_count": "1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	evt := <-events
	if evt.Kind != domain.EventKindToolResult || evt.Tool != domain.ToolKindUpdatePlan || evt.Item.ID == "" {
		t.Fatalf("unexpected event: %#v", evt)
	}
	assistant, ok := evt.Item.Content.(domain.AssistantMessage)
	if !ok {
		t.Fatalf("expected assistant item, got %#v", evt.Item.Content)
	}
	call := assistant.ToolByID("call_plan")
	if call == nil || call.Status != domain.ToolStatusDone || call.Result == nil {
		t.Fatalf("expected completed plan tool call, got %#v", call)
	}
	items, err := modeltest.TimelineForChat(context.Background(), st, chat.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("unexpected stored plan output: %#v", items)
	}
	storedAssistant, ok := items[0].Content.(domain.AssistantMessage)
	if !ok {
		t.Fatalf("expected assistant plan tool call, got %#v", items[0])
	}
	storedCall := storedAssistant.ToolByID("call_plan")
	if storedCall == nil || storedCall.Result == nil {
		t.Fatalf("expected stored plan result, got %#v", storedCall)
	}
	if _, ok := storedCall.Result.Data.(domain.UpdatePlanStoredResult); !ok {
		t.Fatalf("expected typed plan result, got %#v", storedCall.Result.Data)
	}
}

func TestPersistResultRejectsInvalidPlanBeforeWriting(t *testing.T) {
	st := openPlanStore(t)
	session, err := modeltest.CreateSession(context.Background(), st, "test", "provider", "model", nil)
	if err != nil {
		t.Fatal(err)
	}

	runtime := tools.Runtime{Store: st, SessionID: session.ID, SessionControl: tooltest.NewSessionControl(st)}
	_, err = tool{}.PersistResult(context.Background(), runtime, tools.Request{
		Tool: domain.ToolKindUpdatePlan,
		Args: map[string]string{
			"plan": `[{"step":"one","status":"in_progress"},{"step":"two","status":"in_progress"}]`,
		},
	}, tools.Result{})
	if err == nil {
		t.Fatal("expected invalid plan error")
	}

	chat, err := modeltest.DefaultChat(context.Background(), st, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	items, err := modeltest.TimelineForChat(context.Background(), st, chat.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 0 {
		t.Fatalf("expected no partial plan item on error, got %#v", items)
	}
}
