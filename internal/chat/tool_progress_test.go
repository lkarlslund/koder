package chat

import (
	"context"
	"testing"

	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/tools"
)

const progressTestToolID tools.ID = "test_progress"

// progressTestTool reports progress, checks it reached the running call,
// then finishes.
type progressTestTool struct{}

func init() {
	tools.Register(progressTestTool{}, tools.ToolSpec{Title: "Progress test", ExposeToLLM: false})
}

func (progressTestTool) ID() tools.ID             { return progressTestToolID }
func (progressTestTool) BypassesPermission() bool { return true }
func (progressTestTool) NormalizeArgs(args map[string]string) (map[string]string, error) {
	return args, nil
}
func (progressTestTool) Preview(tools.Request) string { return "progress test tool" }
func (progressTestTool) Call(_ context.Context, opts tools.Options) (tools.Result, error) {
	opts.Progress(domain.ToolProgress{Current: "Fetching page 1"})
	opts.Progress(domain.ToolProgress{Current: "Ranking links", Steps: []string{"Examined page 1"}})
	return tools.Result{Text: "done", Status: domain.ToolResultStatusOK}, nil
}

func TestRunningToolProgressReachesTimelineAndSubscribers(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	session, chatRecord, _ := createSessionWithPlan(t, st)
	rt := newTestChat(t, st, session, chatRecord, &runtimeFakeRunner{})
	if _, err := rt.AppendAssistantToolCalls(ctx, domain.TimelineItem{}, []domain.ToolCall{
		{ToolCallID: "call-progress", Tool: domain.ToolKind(progressTestToolID), Status: domain.ToolStatusPending},
	}, "", domain.ReasoningContent{}, domain.Usage{}, domain.ModelPerformance{}); err != nil {
		t.Fatal(err)
	}
	updates, unsubscribe := rt.Subscribe()
	defer unsubscribe()

	if _, err := rt.RunToolCall(ctx, tools.Runtime{}, tools.Request{Tool: progressTestToolID, ToolCallID: "call-progress"}, nil); err != nil {
		t.Fatal(err)
	}

	var reports []domain.ToolProgress
	for len(updates) > 0 {
		update := <-updates
		assistant, ok := update.Item.Content.(domain.AssistantMessage)
		if !ok {
			continue
		}
		if call := assistant.ToolByID("call-progress"); call != nil && call.Status == domain.ToolStatusRunning && call.Progress != nil {
			reports = append(reports, *call.Progress)
		}
	}
	if len(reports) != 2 || reports[0].Current != "Fetching page 1" || reports[1].Current != "Ranking links" || len(reports[1].Steps) != 1 {
		t.Fatalf("progress updates = %+v, want both reports while running", reports)
	}

	call := findToolCall(t, rt, "call-progress")
	if call.Status != domain.ToolStatusDone || call.Progress == nil || call.Progress.Steps[0] != "Examined page 1" {
		t.Fatalf("finished call = %+v, want completed with its last progress kept", call)
	}
}

func findToolCall(t *testing.T, rt *Chat, id domain.ToolCallID) domain.ToolCall {
	t.Helper()
	for _, item := range rt.SnapshotTimeline() {
		if assistant, ok := item.Content.(domain.AssistantMessage); ok {
			if call := assistant.ToolByID(id); call != nil {
				return *call
			}
		}
	}
	t.Fatalf("tool call %s not found", id)
	return domain.ToolCall{}
}
