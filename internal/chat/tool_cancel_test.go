package chat

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/tools"
)

const cancelTestToolID tools.ID = "test_cancel_blocking"

type cancelTestTool struct{}

type cancelTestService struct {
	started chan struct{}
}

type cancelTestRuntime struct {
	runtime tools.Runtime
}

func init() {
	tools.Register(cancelTestTool{}, tools.ToolSpec{Title: "Cancel test", ExposeToLLM: false})
}

func (cancelTestTool) ID() tools.ID             { return cancelTestToolID }
func (cancelTestTool) BypassesPermission() bool { return true }
func (cancelTestTool) NormalizeArgs(args map[string]string) (map[string]string, error) {
	return args, nil
}
func (cancelTestTool) Preview(tools.Request) string { return "blocking test tool" }
func (cancelTestTool) Call(ctx context.Context, opts tools.Options) (tools.Result, error) {
	service, _ := opts.Runtime.Services["cancel_test"].(*cancelTestService)
	if service == nil {
		return tools.Result{}, errors.New("cancel test service is unavailable")
	}
	close(service.started)
	<-ctx.Done()
	return tools.Result{}, ctx.Err()
}

func (r cancelTestRuntime) ToolRuntime(context.Context, *Chat) (tools.Runtime, error) {
	return r.runtime, nil
}

func TestCancelToolStopsOnlySelectedToolAndRecordsCanceledOutcome(t *testing.T) {
	st := openTestStore(t)
	session, chatRecord, _ := createSessionWithPlan(t, st)
	service := &cancelTestService{started: make(chan struct{})}
	rt, err := Load(context.Background(), session, chatRecord, Deps{
		Store: st,
		Runtime: cancelTestRuntime{runtime: tools.Runtime{
			SessionID: session.ID,
			ChatID:    chatRecord.ID,
			Services:  map[string]any{"cancel_test": service},
		}},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(rt.Close)

	req := tools.Request{Tool: cancelTestToolID, ToolCallID: "call_cancel_me"}
	if _, err := rt.AppendAssistantToolRequests(context.Background(), domain.TimelineItem{}, []tools.Request{req}, "", domain.ReasoningContent{}, domain.Usage{}); err != nil {
		t.Fatal(err)
	}
	parentCtx, parentCancel := context.WithCancel(context.Background())
	defer parentCancel()
	done := make(chan error, 1)
	go func() {
		_, err := rt.RunToolCalls(parentCtx, []tools.Request{req}, make(chan domain.Event, 4))
		done <- err
	}()

	select {
	case <-service.started:
	case <-time.After(2 * time.Second):
		t.Fatal("tool did not start")
	}
	if err := rt.CancelTool(req.ToolCallID); err != nil {
		t.Fatalf("cancel tool: %v", err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("run canceled tool: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("tool cancellation did not unblock execution")
	}
	if parentCtx.Err() != nil {
		t.Fatalf("tool cancellation canceled the parent turn: %v", parentCtx.Err())
	}

	timeline := rt.SnapshotTimeline()
	assistant, ok := timeline[len(timeline)-1].Content.(domain.AssistantMessage)
	if !ok || len(assistant.Tools) != 1 {
		t.Fatalf("unexpected tool timeline item: %#v", timeline[len(timeline)-1].Content)
	}
	call := assistant.Tools[0]
	if call.Status != domain.ToolStatusCanceled {
		t.Fatalf("tool status = %q, want canceled", call.Status)
	}
	if call.Error == nil || call.Error.Code != "canceled" || call.Error.Message != "test_cancel_blocking canceled by user" {
		t.Fatalf("unexpected canceled error: %#v", call.Error)
	}
	if err := rt.CancelTool(req.ToolCallID); err == nil {
		t.Fatal("expected completed tool to no longer be cancelable")
	}
}
