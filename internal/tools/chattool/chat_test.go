package chattool

import (
	"context"
	"strings"
	"testing"

	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/tools"
)

type fakeChatControl struct {
	statuses         []tools.ChatStatus
	lastRef          string
	lastTitle        string
	lastSessionID    int64
	lastParentChatID int64
	lastChatID       int64
}

func (f *fakeChatControl) ListChats(context.Context, int64) ([]tools.ChatStatus, error) {
	return f.statuses, nil
}

func (f *fakeChatControl) StartDecomposition(_ context.Context, sessionID, parentChatID int64, ref, title string) (tools.ChatStatus, error) {
	f.lastSessionID = sessionID
	f.lastParentChatID = parentChatID
	f.lastRef = ref
	f.lastTitle = title
	return f.statuses[0], nil
}

func (f *fakeChatControl) StartExecution(_ context.Context, sessionID, parentChatID int64, ref, title string) (tools.ChatStatus, error) {
	f.lastSessionID = sessionID
	f.lastParentChatID = parentChatID
	f.lastRef = ref
	f.lastTitle = title
	return f.statuses[0], nil
}

func (f *fakeChatControl) PollChat(context.Context, int64, int64) (tools.ChatStatus, error) {
	f.lastChatID = f.statuses[0].Chat.ID
	return f.statuses[0], nil
}

func testRuntime(control tools.ChatControl) tools.Runtime {
	return tools.Runtime{
		SessionID:   10,
		ChatID:      20,
		ChatRole:    domain.WorkflowRoleOrchestrator,
		ChatControl: control,
	}
}

func TestNormalizeStartAndPollArgs(t *testing.T) {
	args, err := normalizeStartArgs(map[string]string{"ref": " alpha ", "title": "Worker"})
	if err != nil {
		t.Fatal(err)
	}
	if args["milestone_ref"] != "alpha" || args["title"] != "Worker" {
		t.Fatalf("unexpected normalized args: %#v", args)
	}
	if _, err := (pollTool{}).NormalizeArgs(map[string]string{"chat_id": "0"}); err == nil {
		t.Fatal("expected invalid chat id error")
	}
}

func TestListExecuteRequiresChatControlAndFormatsStoredOutput(t *testing.T) {
	_, err := (listTool{}).Execute(context.Background(), tools.Runtime{}, tools.Request{})
	if err == nil || !strings.Contains(err.Error(), "active persisted chat") {
		t.Fatalf("expected active chat error, got %v", err)
	}

	control := &fakeChatControl{statuses: []tools.ChatStatus{{
		Chat:       domain.Chat{ID: 7, Title: "Worker", WorkflowRole: domain.WorkflowRoleExecution},
		State:      tools.ChatRunStateRunning,
		StatusText: "Running",
	}}}
	result, err := (listTool{}).Execute(context.Background(), testRuntime(control), tools.Request{Tool: domain.ToolKindChatList})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.Output, "Worker") {
		t.Fatalf("expected stored chat output, got %q", result.Output)
	}
}

func TestStartExecutionUsesControl(t *testing.T) {
	control := &fakeChatControl{statuses: []tools.ChatStatus{{
		Chat:       domain.Chat{ID: 9, Title: "Worker", WorkflowRole: domain.WorkflowRoleExecution},
		State:      tools.ChatRunStateRunning,
		StatusText: "Running",
	}}}
	result, err := (startExecutionTool{}).Execute(context.Background(), testRuntime(control), tools.Request{
		Tool: domain.ToolKindChatStartExec,
		Args: map[string]string{"milestone_ref": "alpha", "title": "Worker"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if control.lastRef != "alpha" || control.lastTitle != "Worker" || control.lastSessionID != 10 || control.lastParentChatID != 20 {
		t.Fatalf("unexpected control call: %#v", control)
	}
	if !strings.Contains(result.Output, "Worker") {
		t.Fatalf("expected chat output, got %q", result.Output)
	}
}

func TestPollExecuteReturnsStatus(t *testing.T) {
	control := &fakeChatControl{statuses: []tools.ChatStatus{{
		Chat:       domain.Chat{ID: 11, Title: "Worker", WorkflowRole: domain.WorkflowRoleExecution},
		State:      tools.ChatRunStateCompleted,
		StatusText: "Completed",
	}}}
	result, err := (pollTool{}).Execute(context.Background(), testRuntime(control), tools.Request{
		Tool: domain.ToolKindChatPoll,
		Args: map[string]string{"chat_id": "11"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.Output, "Completed") {
		t.Fatalf("expected poll output to include status, got %q", result.Output)
	}
}
