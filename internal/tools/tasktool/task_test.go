package tasktool

import (
	"context"
	"testing"

	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/modeltest"
	"github.com/lkarlslund/koder/internal/planning"
	"github.com/lkarlslund/koder/internal/store"
	"github.com/lkarlslund/koder/internal/tools"
	"github.com/lkarlslund/koder/internal/tools/tooltest"
)

func openTaskStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.OpenWithOptions(t.TempDir(), store.Options{Backend: store.BackendJSONFS})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func TestNormalizeAndExecute(t *testing.T) {
	if _, err := (tool{}).NormalizeArgs(map[string]string{}); err == nil {
		t.Fatal("expected empty body error")
	}
	req, err := (tool{}).NormalizeArgs(map[string]string{"body": " Ship it "})
	if err != nil {
		t.Fatal(err)
	}
	result, err := tool{}.Execute(context.Background(), tools.Runtime{}, tools.Request{Args: req})
	if err != nil {
		t.Fatal(err)
	}
	if result.Output != "Ship it" {
		t.Fatalf("unexpected output: %q", result.Output)
	}
}

func TestPersistResultCreatesPendingTask(t *testing.T) {
	st := openTaskStore(t)
	session, err := modeltest.CreateSession(context.Background(), st, "test", "provider", "model", nil)
	if err != nil {
		t.Fatal(err)
	}
	control := tooltest.NewSessionControl(st)
	runtime := tools.Runtime{Store: st, SessionID: session.ID, SessionControl: control, TaskControl: control}
	chat, err := modeltest.DefaultChat(context.Background(), st, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	runtime.ChatID = chat.ID
	if _, err := modeltest.AppendTimeline(context.Background(), st, chat.ID, domain.AssistantMessage{
		Tools: []domain.ToolCall{{
			ToolCallID: "call_task",
			Tool:       domain.ToolKindTask,
			Status:     domain.ToolStatusPending,
		}},
	}); err != nil {
		t.Fatal(err)
	}
	events, err := tool{}.PersistResult(context.Background(), runtime, tools.Request{
		Tool:       domain.ToolKindTask,
		ToolCallID: "call_task",
		Args:       map[string]string{"body": "Ship it"},
	}, tools.Result{Output: "Ship it"})
	if err != nil {
		t.Fatal(err)
	}
	evt := <-events
	if evt.Kind != domain.EventKindToolResult || evt.Tool != domain.ToolKindTask || evt.Item.ID == "" {
		t.Fatalf("unexpected event: %#v", evt)
	}
	assistant, ok := evt.Item.Content.(domain.AssistantMessage)
	if !ok {
		t.Fatalf("expected assistant item, got %#v", evt.Item.Content)
	}
	call := assistant.ToolByID("call_task")
	if call == nil || call.Status != domain.ToolStatusDone || call.Result == nil {
		t.Fatalf("expected completed task tool call, got %#v", call)
	}
	tasks, err := modeltest.ListTasks(context.Background(), st, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 || tasks[0].Status != planning.TaskStatusPending || tasks[0].Body != "Ship it" {
		t.Fatalf("unexpected tasks: %#v", tasks)
	}
}
