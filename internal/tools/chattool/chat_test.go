package chattool

import (
	"context"
	"strings"
	"testing"

	"github.com/lkarlslund/koder/internal/chatrole"
	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/tools"
)

type fakeChatControl struct {
	statuses         []tools.ChatStatus
	lastStart        tools.ChatStartRequest
	lastSessionID    domain.ID
	lastParentChatID domain.ID
	lastChatID       domain.ID
}

func (f *fakeChatControl) ListChats(context.Context, domain.ID) ([]tools.ChatStatus, error) {
	return f.statuses, nil
}

func (f *fakeChatControl) StartChat(_ context.Context, sessionID, parentChatID domain.ID, req tools.ChatStartRequest) (tools.ChatStatus, error) {
	f.lastSessionID = sessionID
	f.lastParentChatID = parentChatID
	f.lastStart = req
	return f.statuses[0], nil
}

func (f *fakeChatControl) PollChat(_ context.Context, _ domain.ID, chatID domain.ID) (tools.ChatStatus, error) {
	f.lastChatID = chatID
	return f.statuses[0], nil
}

func testRuntime(control tools.ChatControl) tools.Runtime {
	return tools.Runtime{
		SessionID:   "session-10",
		ChatID:      "chat-20",
		ChatRole:    chatrole.Orchestrator,
		ChatControl: control,
	}
}

func TestNormalizeStartAndPollArgs(t *testing.T) {
	args, err := (startTool{}).NormalizeArgs(map[string]string{"profile": " execution ", "objective": " do it ", "ref": " alpha ", "title": "Worker", "todo_id": "todo-1"})
	if err != nil {
		t.Fatal(err)
	}
	if args["profile"] != "execution" || args["objective"] != "do it" || args["milestone_ref"] != "alpha" || args["title"] != "Worker" || args["todo_ref"] != "todo-1" {
		t.Fatalf("unexpected normalized args: %#v", args)
	}
	if _, err := (startTool{}).NormalizeArgs(map[string]string{"profile": "missing", "objective": "do it"}); err == nil || !strings.Contains(err.Error(), "not registered") {
		t.Fatalf("expected invalid profile error, got %v", err)
	}
	if _, err := (startTool{}).NormalizeArgs(map[string]string{"profile": "execution"}); err == nil || !strings.Contains(err.Error(), "objective") {
		t.Fatalf("expected objective error, got %v", err)
	}
	pollArgs, err := (pollTool{}).NormalizeArgs(map[string]string{"chat_id": " #019e2831-cbf8-79f6-9e6d-3ec97db3d9f9 "})
	if err != nil {
		t.Fatal(err)
	}
	if pollArgs["chat_id"] != "019e2831-cbf8-79f6-9e6d-3ec97db3d9f9" {
		t.Fatalf("unexpected poll args: %#v", pollArgs)
	}
	if _, err := (pollTool{}).NormalizeArgs(map[string]string{"chat_id": "   "}); err == nil {
		t.Fatal("expected empty chat id error")
	}
}

func TestListExecuteRequiresChatControlAndFormatsStoredOutput(t *testing.T) {
	_, err := (listTool{}).Execute(context.Background(), tools.Runtime{}, tools.Request{})
	if err == nil || !strings.Contains(err.Error(), "active persisted chat") {
		t.Fatalf("expected active chat error, got %v", err)
	}

	control := &fakeChatControl{statuses: []tools.ChatStatus{{
		Chat:       domain.Chat{ID: "chat-7", Title: "Worker", WorkflowRole: chatrole.Execution},
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

func TestStartUsesControl(t *testing.T) {
	control := &fakeChatControl{statuses: []tools.ChatStatus{{
		Chat:       domain.Chat{ID: "chat-9", Title: "Worker", WorkflowRole: chatrole.Execution},
		State:      tools.ChatRunStateRunning,
		StatusText: "Running",
	}}}
	result, err := (startTool{}).Execute(context.Background(), testRuntime(control), tools.Request{
		Tool: domain.ToolKindChatStart,
		Args: map[string]string{"profile": "execution", "objective": "Implement alpha", "milestone_ref": "alpha", "title": "Worker"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if control.lastStart.Profile != chatrole.Execution || control.lastStart.Objective != "Implement alpha" || control.lastStart.MilestoneRef != "alpha" || control.lastStart.Title != "Worker" || control.lastSessionID != "session-10" || control.lastParentChatID != "chat-20" {
		t.Fatalf("unexpected control call: %#v", control)
	}
	if !strings.Contains(result.Output, "Worker") {
		t.Fatalf("expected chat output, got %q", result.Output)
	}
}

func TestStartDefinitionOnlyAllowsOrchestrationRoles(t *testing.T) {
	for _, role := range []domain.WorkflowRole{chatrole.General, chatrole.Orchestrator, chatrole.Planning} {
		if _, ok := (startTool{}).Definition(tools.Runtime{ChatRole: role}, tools.ToolSpec{}); !ok {
			t.Fatalf("expected %s to expose chat_start", role)
		}
	}
	for _, role := range []domain.WorkflowRole{chatrole.Decomposition, chatrole.Execution} {
		if _, ok := (startTool{}).Definition(tools.Runtime{ChatRole: role}, tools.ToolSpec{}); ok {
			t.Fatalf("expected %s to hide chat_start", role)
		}
	}
}

func TestPollExecuteReturnsStatus(t *testing.T) {
	control := &fakeChatControl{statuses: []tools.ChatStatus{{
		Chat:       domain.Chat{ID: "chat-11", Title: "Worker", WorkflowRole: chatrole.Execution},
		State:      tools.ChatRunStateCompleted,
		StatusText: "Completed",
	}}}
	result, err := (pollTool{}).Execute(context.Background(), testRuntime(control), tools.Request{
		Tool: domain.ToolKindChatPoll,
		Args: map[string]string{"chat_id": "chat-11"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if control.lastChatID != "chat-11" {
		t.Fatalf("expected poll chat id chat-11, got %q", control.lastChatID)
	}
	if !strings.Contains(result.Output, "Completed") {
		t.Fatalf("expected poll output to include status, got %q", result.Output)
	}
}
