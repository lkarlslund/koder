package chattool

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/lkarlslund/koder/internal/chatrole"
	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/id"
	"github.com/lkarlslund/koder/internal/tools"
)

type fakeChatControl struct {
	statuses         []Status
	lastStart        StartRequest
	lastSessionID    id.ID
	lastOwnerChatID  id.ID
	lastParentChatID id.ID
	lastChatID       id.ID
	lastUpdate       UpdateRequest
	updateErr        error
}

func (f *fakeChatControl) ListChats(context.Context, id.ID) ([]Status, error) {
	return f.statuses, nil
}

func (f *fakeChatControl) StartChat(_ context.Context, sessionID, parentChatID id.ID, req StartRequest) (Status, error) {
	f.lastSessionID = sessionID
	f.lastParentChatID = parentChatID
	f.lastStart = req
	return f.statuses[0], nil
}

func (f *fakeChatControl) UpdateChat(_ context.Context, sessionID, ownerChatID, chatID id.ID, update UpdateRequest) (Status, error) {
	if f.updateErr != nil {
		return Status{}, f.updateErr
	}
	f.lastSessionID = sessionID
	f.lastOwnerChatID = ownerChatID
	f.lastChatID = chatID
	f.lastUpdate = update
	status := Status{ID: chatID}
	for _, item := range f.statuses {
		if item.ID == chatID {
			status = item
			break
		}
	}
	if update.Archived != nil {
		status.Archived = *update.Archived
	}
	if update.Title != "" {
		status.Title = update.Title
	}
	return status, nil
}

func testRuntime(control Control) tools.Runtime {
	return tools.Runtime{
		SessionID: "session-10",
		ChatID:    "chat-20",
		ChatRole:  chatrole.Orchestrator,
		Services:  RuntimeService(control),
	}
}

func TestNormalizeArgs(t *testing.T) {
	listArgs, err := (listTool{}).NormalizeArgs(map[string]string{"archived": "true"})
	if err != nil {
		t.Fatal(err)
	}
	if listArgs["archived"] != "true" {
		t.Fatalf("unexpected list args: %#v", listArgs)
	}

	args, err := (startTool{}).NormalizeArgs(map[string]string{"profile": " execution ", "objective": " do it ", "milestone_key": " M001 ", "title": "Worker", "task_key": "M001T001"})
	if err != nil {
		t.Fatal(err)
	}
	if args["profile"] != "execution" || args["objective"] != "do it" || args["milestone_key"] != "M001" || args["title"] != "Worker" || args["task_key"] != "M001T001" {
		t.Fatalf("unexpected normalized args: %#v", args)
	}
	if _, err := (startTool{}).NormalizeArgs(map[string]string{"profile": "execution", "objective": "do it", "task_key": "M001"}); err == nil || !strings.Contains(err.Error(), "invalid task_key") {
		t.Fatalf("expected milestone-as-task error, got %v", err)
	}
	if _, err := (startTool{}).NormalizeArgs(map[string]string{"profile": "missing", "objective": "do it"}); err == nil || !strings.Contains(err.Error(), "not registered") {
		t.Fatalf("expected invalid profile error, got %v", err)
	}
	if _, err := (startTool{}).NormalizeArgs(map[string]string{"profile": "execution"}); err == nil || !strings.Contains(err.Error(), "objective") {
		t.Fatalf("expected objective error, got %v", err)
	}

	sendArgs, err := (sendTool{}).NormalizeArgs(map[string]string{"chat_id": " #child ", "message": " continue this ", "steer": "true"})
	if err != nil {
		t.Fatal(err)
	}
	if sendArgs["chat_id"] != "child" || sendArgs["message"] != "continue this" || sendArgs["steer"] != "true" {
		t.Fatalf("unexpected send args: %#v", sendArgs)
	}
	if _, err := (sendTool{}).NormalizeArgs(map[string]string{"chat_id": "child"}); err == nil || !strings.Contains(err.Error(), "message") {
		t.Fatalf("expected message error, got %v", err)
	}

	cancelArgs, err := (cancelTool{}).NormalizeArgs(map[string]string{"hard": "false"})
	if err != nil {
		t.Fatal(err)
	}
	if cancelArgs["hard"] != "false" {
		t.Fatalf("unexpected cancel args: %#v", cancelArgs)
	}

	archiveArgs, err := (archiveTool{}).NormalizeArgs(map[string]string{"chat_id": " #child ", "archived": "false"})
	if err != nil {
		t.Fatal(err)
	}
	if archiveArgs["chat_id"] != "child" || archiveArgs["archived"] != "false" {
		t.Fatalf("unexpected archive args: %#v", archiveArgs)
	}
	if _, err := (archiveTool{}).NormalizeArgs(map[string]string{"archived": "true"}); err == nil || !strings.Contains(err.Error(), "chat_id") {
		t.Fatalf("expected archive chat_id error, got %v", err)
	}
	if _, err := (archiveTool{}).NormalizeArgs(map[string]string{"archived": "maybe"}); err == nil {
		t.Fatal("expected archived bool error")
	}

	renameArgs, err := (renameTool{}).NormalizeArgs(map[string]string{"title": " Restored "})
	if err != nil {
		t.Fatal(err)
	}
	if renameArgs["title"] != "Restored" {
		t.Fatalf("unexpected rename args: %#v", renameArgs)
	}
	if _, err := (renameTool{}).NormalizeArgs(map[string]string{}); err == nil || !strings.Contains(err.Error(), "title") {
		t.Fatalf("expected title error, got %v", err)
	}
}

func TestListExecuteRequiresChatControlAndFormatsStoredOutput(t *testing.T) {
	_, err := (listTool{}).Call(context.Background(), tools.Options{Runtime: tools.Runtime{}, Request: tools.Request{}})
	if err == nil || !strings.Contains(err.Error(), "active persisted chat") {
		t.Fatalf("expected active chat error, got %v", err)
	}

	control := &fakeChatControl{statuses: []Status{{
		ID:         "chat-7",
		Title:      "Worker",
		Role:       chatrole.Execution,
		State:      RunStateRunning,
		StatusText: "Running",
	}, {
		ID:         "chat-8",
		Title:      "Archived",
		Role:       chatrole.Execution,
		Archived:   true,
		State:      RunStateIdle,
		StatusText: "Idle",
	}}}
	result, err := (listTool{}).Call(context.Background(), tools.Options{Runtime: testRuntime(control), Request: tools.Request{Tool: domain.ToolKindChatList}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.Output, "Worker") {
		t.Fatalf("expected stored chat output, got %q", result.Output)
	}
	if strings.Contains(result.Output, "Archived") {
		t.Fatalf("expected archived chat hidden by default, got %q", result.Output)
	}
	result, err = (listTool{}).Call(context.Background(), tools.Options{Runtime: testRuntime(control), Request: tools.Request{Tool: domain.ToolKindChatList, Args: map[string]string{"archived": "true"}}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.Output, "Archived") {
		t.Fatalf("expected archived chat with archived=true, got %q", result.Output)
	}
}

func TestStartUsesControlAndReportsNoPollingContract(t *testing.T) {
	control := &fakeChatControl{statuses: []Status{{
		ID:         "chat-9",
		Title:      "Worker",
		Role:       chatrole.Execution,
		State:      RunStateRunning,
		StatusText: "Running",
	}}}
	result, err := (startTool{}).Call(context.Background(), tools.Options{Runtime: testRuntime(control), Request: tools.Request{
		Tool: domain.ToolKindChatStart,
		Args: map[string]string{"profile": "execution", "objective": "Implement alpha", "milestone_key": "alpha", "title": "Worker"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if control.lastStart.Profile != chatrole.Execution || control.lastStart.Objective != "Implement alpha" || control.lastStart.MilestoneRef != "alpha" || control.lastStart.Title != "Worker" || control.lastSessionID != "session-10" || control.lastParentChatID != "chat-20" {
		t.Fatalf("unexpected control call: %#v", control)
	}
	if !strings.Contains(result.Output, "Worker") || !strings.Contains(result.Output, "will report back automatically") || !strings.Contains(result.Output, "Do not poll") {
		t.Fatalf("expected chat output with reporting guidance, got %q", result.Output)
	}
}

func TestStartDefinitionOnlyAllowsOrchestrationRoles(t *testing.T) {
	for _, role := range []domain.WorkflowRole{chatrole.General, chatrole.Orchestrator, chatrole.Planning} {
		if _, ok := (startTool{}).Definition(tools.Runtime{ChatRole: role}, tools.ToolSpec{}); !ok {
			t.Fatalf("expected %s to expose chat_start", role)
		}
	}
	for _, role := range []domain.WorkflowRole{chatrole.Execution} {
		if _, ok := (startTool{}).Definition(tools.Runtime{ChatRole: role}, tools.ToolSpec{}); ok {
			t.Fatalf("expected %s to hide chat_start", role)
		}
	}
}

func TestSendPreviewIncludesMessage(t *testing.T) {
	got := (sendTool{}).Preview(tools.Request{Args: map[string]string{
		"chat_id": "child-chat",
		"message": "Use jadx output",
	}})
	if !strings.Contains(got, "child-chat") || !strings.Contains(got, "Use jadx output") {
		t.Fatalf("expected chat id and message in preview, got %q", got)
	}
}

func TestSendCancelArchiveRenameUseControl(t *testing.T) {
	control := &fakeChatControl{statuses: []Status{{
		ID:         "child-chat",
		Title:      "Worker",
		Role:       chatrole.Execution,
		State:      RunStateRunning,
		StatusText: "Running",
	}}}

	_, err := (sendTool{}).Call(context.Background(), tools.Options{Runtime: testRuntime(control), Request: tools.Request{
		Tool: domain.ToolKindChatSend,
		Args: map[string]string{"chat_id": "child-chat", "message": "Use jadx output", "steer": "true"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if control.lastOwnerChatID != "chat-20" || control.lastChatID != "child-chat" || control.lastUpdate.Message != "Use jadx output" || !control.lastUpdate.Steer {
		t.Fatalf("unexpected send request: %#v", control)
	}

	_, err = (cancelTool{}).Call(context.Background(), tools.Options{Runtime: testRuntime(control), Request: tools.Request{
		Tool: domain.ToolKindChatCancel,
		Args: map[string]string{"chat_id": "child-chat", "hard": "true"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if !control.lastUpdate.Interrupt || !control.lastUpdate.Hard {
		t.Fatalf("unexpected cancel request: %#v", control.lastUpdate)
	}

	_, err = (archiveTool{}).Call(context.Background(), tools.Options{Runtime: testRuntime(control), Request: tools.Request{
		Tool: domain.ToolKindChatArchive,
		Args: map[string]string{"chat_id": "child-chat", "archived": "false"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if control.lastChatID != "child-chat" || control.lastUpdate.Archived == nil || *control.lastUpdate.Archived {
		t.Fatalf("unexpected archive request: %#v", control)
	}

	result, err := (renameTool{}).Call(context.Background(), tools.Options{Runtime: testRuntime(control), Request: tools.Request{
		Tool: domain.ToolKindChatRename,
		Args: map[string]string{"chat_id": "child-chat", "title": "Renamed"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if control.lastUpdate.Title != "Renamed" || !strings.Contains(result.Output, "Renamed") {
		t.Fatalf("unexpected rename result: update=%#v output=%q", control.lastUpdate, result.Output)
	}
}

func TestArchiveExecuteSurfacesArchiveRuleErrors(t *testing.T) {
	control := &fakeChatControl{updateErr: errors.New("cannot archive chat chat-20 while it is not idle")}
	_, err := (archiveTool{}).Call(context.Background(), tools.Options{Runtime: testRuntime(control), Request: tools.Request{
		Tool: domain.ToolKindChatArchive,
		Args: map[string]string{"chat_id": "child-chat", "archived": "true"},
	}})
	if err == nil || !strings.Contains(err.Error(), "not idle") {
		t.Fatalf("expected archive rule error, got %v", err)
	}
}

func TestArchiveRejectsCurrentChat(t *testing.T) {
	control := &fakeChatControl{}
	_, err := (archiveTool{}).Call(context.Background(), tools.Options{Runtime: testRuntime(control), Request: tools.Request{
		Tool: domain.ToolKindChatArchive,
		Args: map[string]string{"chat_id": "chat-20", "archived": "true"},
	}})
	if err == nil || !strings.Contains(err.Error(), "current chat") {
		t.Fatalf("expected current chat archive error, got %v", err)
	}
	if control.lastChatID != "" {
		t.Fatalf("archive should not call control for current chat, got %s", control.lastChatID)
	}
}

func TestCleanupArchivesIdleExecutionChildren(t *testing.T) {
	control := &fakeChatControl{statuses: []Status{{
		ID:           "chat-20",
		Title:        "Root",
		Role:         chatrole.Orchestrator,
		State:        RunStateRunning,
		ParentChatID: "",
	}, {
		ID:           "child-idle",
		ParentChatID: "chat-20",
		Title:        "Done worker",
		Role:         chatrole.Execution,
		State:        RunStateIdle,
	}, {
		ID:           "child-running",
		ParentChatID: "chat-20",
		Title:        "Busy worker",
		Role:         chatrole.Execution,
		State:        RunStateRunning,
		Busy:         true,
	}, {
		ID:           "side-chat",
		ParentChatID: "other-parent",
		Title:        "Side",
		Role:         chatrole.Execution,
		State:        RunStateIdle,
	}, {
		ID:           "child-plan",
		ParentChatID: "chat-20",
		Title:        "Planner",
		Role:         chatrole.Planning,
		State:        RunStateIdle,
	}}}
	result, err := (cleanupTool{}).Call(context.Background(), tools.Options{Runtime: testRuntime(control), Request: tools.Request{
		Tool: domain.ToolKindChatCleanup,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if control.lastChatID != "child-idle" || control.lastUpdate.Archived == nil || !*control.lastUpdate.Archived {
		t.Fatalf("expected cleanup to archive only idle execution child, got %#v", control)
	}
	if !strings.Contains(result.Output, "Archived 1 idle execution child chat") ||
		!strings.Contains(result.Output, "child-running") ||
		!strings.Contains(result.Output, "not idle") ||
		!strings.Contains(result.Output, "side-chat") ||
		!strings.Contains(result.Output, "not a direct child") ||
		!strings.Contains(result.Output, "child-plan") ||
		!strings.Contains(result.Output, "not an execution chat") {
		t.Fatalf("unexpected cleanup output: %q", result.Output)
	}
}
