package chat

import (
	"context"
	"testing"

	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/tools"
)

func pendingInputCall() domain.ToolCall {
	return domain.ToolCall{
		Tool:       domain.ToolKindRequestUserInput,
		ToolCallID: domain.ToolCallID("call-1"),
		Status:     domain.ToolStatusAwaitingInput,
		Args:       map[string]string{"questions": `[{"id":"choice","header":"Choose","question":"Which?","options":[{"label":"A","description":"First"},{"label":"B","description":"Second"}]}]`},
	}
}

func TestLoadWithPendingUserInputStartsWaitingForInput(t *testing.T) {
	st := openTestStore(t)
	session, chatRecord, _ := createSessionWithPlan(t, st)
	if _, err := appendAssistantToolCalls(context.Background(), st, chatRecord.ID, []domain.ToolCall{pendingInputCall()}, "", domain.Usage{}); err != nil {
		t.Fatal(err)
	}
	rt, err := Load(context.Background(), session, chatRecord, depsForFake(st, &pendingToolFakeRunner{}), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(rt.Close)

	snapshot := rt.Snapshot()
	if snapshot.Status != StatusWaitingInput || snapshot.StatusText != "Waiting for input" {
		t.Fatalf("status = %q (%q), want waiting input", snapshot.Status, snapshot.StatusText)
	}
	if snapshot.PendingUserInput != 1 {
		t.Fatalf("pending user input = %d, want 1", snapshot.PendingUserInput)
	}
	if len(snapshot.PendingInputCalls) != 1 || snapshot.PendingInputCalls[0].ToolCallID != "call-1" {
		t.Fatalf("pending input calls = %#v, want call-1", snapshot.PendingInputCalls)
	}
}

func TestPendingUserInputSurvivesLaterTimelineItems(t *testing.T) {
	timeline := []domain.TimelineItem{
		{Content: domain.AssistantMessage{Tools: []domain.ToolCall{pendingInputCall()}}},
		{Content: domain.UserMessage{Text: "later"}},
		{Content: domain.AssistantMessage{Text: "temporary follow-up"}},
	}
	if got := pendingUserInputCount(timeline); got != 1 {
		t.Fatalf("pending count = %d, want 1", got)
	}
}

func TestPendingRequestUserInputIsRecoverableAfterHydration(t *testing.T) {
	call := pendingInputCall()
	call.Status = domain.ToolStatusPending
	timeline := []domain.TimelineItem{{Content: domain.AssistantMessage{Tools: []domain.ToolCall{call}}}}
	got := pendingUserInputCalls(timeline)
	if len(got) != 1 || got[0].ToolCallID != call.ToolCallID {
		t.Fatalf("pending calls = %#v, want recoverable request_user_input", got)
	}
}

func TestValidateUserInputAnswers(t *testing.T) {
	answers := []tools.UserInputAnswer{{ToolCallID: "call-1", QuestionID: "choice", Selected: "A", Comment: "because"}}
	grouped, err := validateUserInputAnswers([]domain.ToolCall{pendingInputCall()}, answers)
	if err != nil {
		t.Fatal(err)
	}
	if len(grouped["call-1"]) != 1 || grouped["call-1"][0].Comment != "because" {
		t.Fatalf("unexpected grouped answers: %#v", grouped)
	}
}

func TestValidateUserInputAnswersRequiresEveryQuestion(t *testing.T) {
	if _, err := validateUserInputAnswers([]domain.ToolCall{pendingInputCall()}, nil); err == nil {
		t.Fatal("expected missing answer error")
	}
	answers := []tools.UserInputAnswer{{ToolCallID: "call-1", QuestionID: "choice", Selected: "unknown"}}
	if _, err := validateUserInputAnswers([]domain.ToolCall{pendingInputCall()}, answers); err == nil {
		t.Fatal("expected invalid option error")
	}
}
