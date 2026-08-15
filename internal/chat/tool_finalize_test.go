package chat

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/tools"
)

const finalizeFailureTestToolID tools.ID = "test_finalize_failure"

type finalizeFailureTestTool struct{}

func init() {
	tools.Register(finalizeFailureTestTool{}, tools.ToolSpec{Title: "Finalize failure test", ExposeToLLM: false})
}

func (finalizeFailureTestTool) ID() tools.ID             { return finalizeFailureTestToolID }
func (finalizeFailureTestTool) BypassesPermission() bool { return true }
func (finalizeFailureTestTool) NormalizeArgs(args map[string]string) (map[string]string, error) {
	return args, nil
}
func (finalizeFailureTestTool) Preview(tools.Request) string { return "finalize failure test" }
func (finalizeFailureTestTool) Call(context.Context, tools.Options) (tools.Result, error) {
	return tools.Result{Output: "preview"}, nil
}
func (finalizeFailureTestTool) FinalizeResult(context.Context, tools.Runtime, tools.Request, tools.Result) (tools.Result, error) {
	return tools.Result{}, errors.New("state changed; use the suggested recovery tool")
}

func TestToolFinalizationFailureIsRecordedAndDoesNotStopTurn(t *testing.T) {
	st := openTestStore(t)
	session, chatRecord, _ := createSessionWithPlan(t, st)
	rt, err := Load(context.Background(), session, chatRecord, Deps{
		Store: st,
		Runtime: cancelTestRuntime{runtime: tools.Runtime{
			SessionID: session.ID,
			ChatID:    chatRecord.ID,
		}},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(rt.Close)

	req := tools.Request{Tool: finalizeFailureTestToolID, ToolCallID: "call_finalize_failure"}
	if _, err := rt.AppendAssistantToolRequests(context.Background(), domain.TimelineItem{}, []tools.Request{req}, "", domain.ReasoningContent{}, domain.Usage{}, domain.ModelPerformance{}); err != nil {
		t.Fatal(err)
	}
	out := make(chan domain.Event, 4)
	waitingApproval, err := rt.RunToolCalls(context.Background(), []tools.Request{req}, out)
	if err != nil {
		t.Fatalf("finalization failure stopped the turn: %v", err)
	}
	if waitingApproval {
		t.Fatal("finalization failure unexpectedly requested approval")
	}
	close(out)
	var feedback string
	for event := range out {
		if event.Kind == domain.EventKindToolResult {
			feedback = event.Text
		}
	}
	if !strings.Contains(feedback, "state changed; use the suggested recovery tool") {
		t.Fatalf("tool feedback = %q, want recovery guidance", feedback)
	}

	timeline := rt.SnapshotTimeline()
	assistant, ok := timeline[len(timeline)-1].Content.(domain.AssistantMessage)
	if !ok || len(assistant.Tools) != 1 {
		t.Fatalf("unexpected tool timeline item: %#v", timeline[len(timeline)-1].Content)
	}
	call := assistant.Tools[0]
	if call.Status != domain.ToolStatusErrored || call.Error == nil {
		t.Fatalf("tool outcome = %#v, want recorded error", call)
	}
}
