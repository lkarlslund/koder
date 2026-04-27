package chatruntime

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/lkarlslund/koder/internal/attachment"
	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/reference"
	"github.com/lkarlslund/koder/internal/store"
	"github.com/lkarlslund/koder/internal/tools"
)

type fakeRunner struct {
	events  <-chan domain.Event
	err     error
	session domain.Session
	chat    domain.Chat
	prompt  string
	calls   int
}

func (f *fakeRunner) RunPromptInChat(_ context.Context, session domain.Session, chat domain.Chat, prompt string, _ []attachment.Draft, _ []reference.Draft, _ string) (<-chan domain.Event, error) {
	f.calls++
	f.session = session
	f.chat = chat
	f.prompt = prompt
	if f.err != nil {
		return nil, f.err
	}
	return f.events, nil
}

func openTestStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.OpenWithOptions(t.TempDir(), store.Options{Backend: store.BackendJSONFS})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = st.Close()
	})
	return st
}

func createSessionWithPlan(t *testing.T, st *store.Store) (domain.Session, domain.Chat, store.MilestonePlan) {
	t.Helper()
	ctx := context.Background()
	session, err := st.CreateSession(ctx, "test", "provider", "model", nil)
	if err != nil {
		t.Fatal(err)
	}
	chat, err := st.DefaultChat(ctx, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := st.SetMilestonePlan(ctx, session.ID, "Ship it", []store.Milestone{
		{Ref: "alpha", Title: "Alpha", Status: domain.MilestoneStatusInProgress, Position: 0},
		{Ref: "beta", Title: "Beta", Status: domain.MilestoneStatusPending, Position: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddTodoItems(ctx, session.ID, "alpha", []string{"Inspect state", "Write tests"}); err != nil {
		t.Fatal(err)
	}
	return session, chat, plan
}

func TestNewInitializesRunMap(t *testing.T) {
	mgr := New(nil, nil)
	if mgr.runs == nil {
		t.Fatal("expected runs map to be initialized")
	}
	if len(mgr.runs) != 0 {
		t.Fatalf("expected empty runs map, got %d entries", len(mgr.runs))
	}
}

func TestBootstrapPromptIncludesMilestoneAndTodosForDecomposition(t *testing.T) {
	st := openTestStore(t)
	session, _, plan := createSessionWithPlan(t, st)
	mgr := &Manager{store: st, runs: map[int64]runState{}}

	got := mgr.bootstrapPrompt(context.Background(), session.ID, plan.Milestones[0], domain.WorkflowRoleDecomposition)
	for _, want := range []string{
		"Milestone ref: alpha",
		"Milestone title: Alpha",
		"Current todos:",
		"- [pending] #1 Inspect state",
		"Decompose only this milestone into concrete todo items.",
		"Do not edit code in this chat.",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected prompt to contain %q, got %q", want, got)
		}
	}
}

func TestBootstrapPromptIncludesExecutionInstructionsWithoutTodos(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	session, err := st.CreateSession(ctx, "test", "provider", "model", nil)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := st.SetMilestonePlan(ctx, session.ID, "Ship it", []store.Milestone{
		{Ref: "solo", Title: "Solo", Status: domain.MilestoneStatusPending, Notes: "keep scope narrow", Position: 0},
	})
	if err != nil {
		t.Fatal(err)
	}

	mgr := &Manager{store: st, runs: map[int64]runState{}}
	got := mgr.bootstrapPrompt(ctx, session.ID, plan.Milestones[0], domain.WorkflowRoleExecution)
	for _, want := range []string{
		"Milestone notes:",
		"keep scope narrow",
		"Current todos: none",
		"Execute only this milestone using its todo bucket as the working queue.",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected prompt to contain %q, got %q", want, got)
		}
	}
}

func TestUpdateMilestoneStatusReplacesActiveState(t *testing.T) {
	st := openTestStore(t)
	session, _, _ := createSessionWithPlan(t, st)
	ctx := context.Background()
	_, err := st.SetMilestonePlan(ctx, session.ID, "Ship it", []store.Milestone{
		{Ref: "alpha", Title: "Alpha", Status: domain.MilestoneStatusExecuting, Position: 0},
		{Ref: "beta", Title: "Beta", Status: domain.MilestoneStatusInProgress, Position: 1},
		{Ref: "gamma", Title: "Gamma", Status: domain.MilestoneStatusCompleted, Position: 2},
	})
	if err != nil {
		t.Fatal(err)
	}
	mgr := &Manager{store: st, runs: map[int64]runState{}}

	if err := mgr.updateMilestoneStatus(ctx, session.ID, "beta", domain.MilestoneStatusDecomposing); err != nil {
		t.Fatal(err)
	}
	plan, err := st.GetMilestonePlan(ctx, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Milestones[0].Status != domain.MilestoneStatusPending {
		t.Fatalf("expected alpha reset to pending, got %s", plan.Milestones[0].Status)
	}
	if plan.Milestones[1].Status != domain.MilestoneStatusDecomposing {
		t.Fatalf("expected beta to become decomposing, got %s", plan.Milestones[1].Status)
	}
	if plan.Milestones[2].Status != domain.MilestoneStatusCompleted {
		t.Fatalf("expected completed milestone to be preserved, got %s", plan.Milestones[2].Status)
	}
}

func TestRoleHelpers(t *testing.T) {
	if got := roleMilestoneStatus(domain.WorkflowRoleDecomposition); got != domain.MilestoneStatusDecomposing {
		t.Fatalf("unexpected decomposition milestone status: %s", got)
	}
	if got := roleMilestoneStatus(domain.WorkflowRoleExecution); got != domain.MilestoneStatusExecuting {
		t.Fatalf("unexpected execution milestone status: %s", got)
	}
	if got := roleMilestoneStatus(domain.WorkflowRoleOrchestrator); got != domain.MilestoneStatusInProgress {
		t.Fatalf("unexpected orchestrator milestone status: %s", got)
	}

	if got := roleDisplayName(domain.WorkflowRoleDecomposition); got != "Decompose" {
		t.Fatalf("unexpected display name: %q", got)
	}
	if got := roleDisplayName(domain.WorkflowRoleExecution); got != "Execute" {
		t.Fatalf("unexpected display name: %q", got)
	}
	if got := roleDisplayName(domain.WorkflowRoleOrchestrator); got != "Orchestrate" {
		t.Fatalf("unexpected display name: %q", got)
	}
	if got := roleDisplayName("unknown"); got != "Chat" {
		t.Fatalf("unexpected fallback display name: %q", got)
	}
}

func TestMilestoneByRef(t *testing.T) {
	plan := store.MilestonePlan{
		Milestones: []store.Milestone{
			{Ref: "one", Title: "One"},
			{Ref: "two", Title: "Two"},
		},
	}
	got, ok := milestoneByRef(plan, "two")
	if !ok || got.Title != "Two" {
		t.Fatalf("expected to find milestone two, got %#v found=%v", got, ok)
	}
	if _, ok := milestoneByRef(plan, "missing"); ok {
		t.Fatal("expected missing milestone lookup to fail")
	}
}

func TestPollChatRejectsSessionMismatch(t *testing.T) {
	st := openTestStore(t)
	_, chat, _ := createSessionWithPlan(t, st)
	otherSession, err := st.CreateSession(context.Background(), "other", "provider", "model", nil)
	if err != nil {
		t.Fatal(err)
	}
	mgr := &Manager{store: st, runs: map[int64]runState{}}

	_, err = mgr.PollChat(context.Background(), otherSession.ID, chat.ID)
	if err == nil || !strings.Contains(err.Error(), "does not belong to session") {
		t.Fatalf("expected session mismatch error, got %v", err)
	}
}

func TestListChatsAndChatStatus(t *testing.T) {
	st := openTestStore(t)
	session, chat, _ := createSessionWithPlan(t, st)
	if _, err := st.CreateChat(context.Background(), session.ID, "child", domain.WorkflowRoleExecution, &chat.ID); err != nil {
		t.Fatal(err)
	}
	mgr := &Manager{
		store: st,
		runs: map[int64]runState{
			chat.ID: {state: tools.ChatRunStateRunning, statusText: "Running"},
		},
	}

	statuses, err := mgr.ListChats(context.Background(), session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(statuses) != 2 {
		t.Fatalf("expected two chat statuses, got %d", len(statuses))
	}
	if !statuses[0].Busy && !statuses[1].Busy {
		t.Fatalf("expected one busy chat status, got %#v", statuses)
	}
}

func TestChatStatusUsesPendingApprovalsWhenIdle(t *testing.T) {
	st := openTestStore(t)
	session, chat, _ := createSessionWithPlan(t, st)
	if _, err := st.CreateChatApproval(context.Background(), chat.ID, domain.ToolKindBash, "echo hi"); err != nil {
		t.Fatal(err)
	}
	mgr := &Manager{store: st, runs: map[int64]runState{}}

	status, err := mgr.chatStatus(context.Background(), chat)
	if err != nil {
		t.Fatal(err)
	}
	if status.State != tools.ChatRunStateWaitingApproval {
		t.Fatalf("expected waiting approval state, got %s", status.State)
	}
	if !status.Busy {
		t.Fatal("expected waiting approval state to be busy")
	}
	if status.PendingApprovals != 1 {
		t.Fatalf("expected one pending approval, got %d", status.PendingApprovals)
	}
	_ = session
}

func TestConsumeTransitionsStateAndCapturesErrors(t *testing.T) {
	mgr := &Manager{runs: map[int64]runState{}}
	events := make(chan domain.Event, 4)
	events <- domain.Event{Kind: domain.EventKindStatus, Text: "Booting"}
	events <- domain.Event{Kind: domain.EventKindApprovalAsk, Text: "Need approval"}
	events <- domain.Event{Kind: domain.EventKindError, Err: errors.New("boom")}
	close(events)

	mgr.consume(42, func() {}, events)

	state := mgr.runs[42]
	if state.state != tools.ChatRunStateFailed {
		t.Fatalf("expected failed state, got %s", state.state)
	}
	if state.statusText != "Failed" {
		t.Fatalf("expected failed status text, got %q", state.statusText)
	}
	if state.lastError != "boom" {
		t.Fatalf("expected stored error, got %q", state.lastError)
	}
}

func TestConsumeCompletesOnMessageDone(t *testing.T) {
	mgr := &Manager{runs: map[int64]runState{}}
	events := make(chan domain.Event, 2)
	events <- domain.Event{Kind: domain.EventKindStatus, Text: "Running"}
	events <- domain.Event{Kind: domain.EventKindMessageDone}
	close(events)

	mgr.consume(7, func() {}, events)

	state := mgr.runs[7]
	if state.state != tools.ChatRunStateCompleted {
		t.Fatalf("expected completed state, got %s", state.state)
	}
	if state.statusText != "Completed" {
		t.Fatalf("expected completed status text, got %q", state.statusText)
	}
}

func TestStartExecutionCreatesWorkflowChatAndInvokesRunner(t *testing.T) {
	st := openTestStore(t)
	session, parentChat, _ := createSessionWithPlan(t, st)
	events := make(chan domain.Event)
	runner := &fakeRunner{events: events}
	mgr := &Manager{engine: runner, store: st, runs: map[int64]runState{}}
	defer close(events)

	status, err := mgr.StartExecution(context.Background(), session.ID, parentChat.ID, "alpha", "")
	if err != nil {
		t.Fatal(err)
	}
	if runner.calls != 1 {
		t.Fatalf("expected one runner call, got %d", runner.calls)
	}
	if runner.session.ID != session.ID {
		t.Fatalf("expected session %d, got %d", session.ID, runner.session.ID)
	}
	if runner.chat.WorkflowRole != domain.WorkflowRoleExecution {
		t.Fatalf("expected execution chat role, got %s", runner.chat.WorkflowRole)
	}
	if runner.chat.ActiveMilestoneRef != "alpha" {
		t.Fatalf("expected active milestone ref alpha, got %q", runner.chat.ActiveMilestoneRef)
	}
	if !strings.Contains(runner.prompt, "Execute only this milestone") {
		t.Fatalf("expected execution bootstrap prompt, got %q", runner.prompt)
	}
	if status.State != tools.ChatRunStateRunning {
		t.Fatalf("expected initial running state, got %s", status.State)
	}
}

func TestStartDecompositionReturnsRunnerError(t *testing.T) {
	st := openTestStore(t)
	session, parentChat, _ := createSessionWithPlan(t, st)
	runner := &fakeRunner{err: errors.New("runner failed")}
	mgr := &Manager{engine: runner, store: st, runs: map[int64]runState{}}

	_, err := mgr.StartDecomposition(context.Background(), session.ID, parentChat.ID, "alpha", "")
	if err == nil || !strings.Contains(err.Error(), "runner failed") {
		t.Fatalf("expected runner error, got %v", err)
	}
}
