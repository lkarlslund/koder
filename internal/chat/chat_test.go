package chat

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lkarlslund/koder/internal/accesssettings"
	"github.com/lkarlslund/koder/internal/attachment"
	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/id"
	"github.com/lkarlslund/koder/internal/modeltest"
	"github.com/lkarlslund/koder/internal/planning"
	"github.com/lkarlslund/koder/internal/provider"
	"github.com/lkarlslund/koder/internal/reference"
	"github.com/lkarlslund/koder/internal/store"
)

var errTestProviderFailure = errors.New("provider failed")

type runtimeFakeRunner struct {
	mu             sync.Mutex
	promptCalls    int
	continueCalls  int
	approveCalls   int
	denyCalls      int
	prompts        []string
	promptNotes    []string
	continueNotes  []string
	turnTimelines  []int
	promptTimeline [][]domain.TimelineItem
	events         []<-chan domain.Event
}

type cancelAwareRunner struct {
	ctxSeen chan context.Context
	events  chan domain.Event
}

type pendingToolFakeRunner struct {
	runtimeFakeRunner
	resumeCalls  int
	resumeEvents []<-chan domain.Event
}

type turnPromptFakeRunner struct {
	runtimeFakeRunner
}

type controlledTurnPromptRunner struct {
	runtimeFakeRunner
	mu      sync.Mutex
	calls   int
	prompts []string
	events  []<-chan domain.Event
}

type queuedSteerBoundaryRunner struct {
	step0Started chan struct{}
	continueStep chan struct{}
	step1Done    chan struct{}

	mu            sync.Mutex
	step1Timeline []domain.TimelineItem
}

type queuedSteerBoundaryLoop struct {
	runner *queuedSteerBoundaryRunner
}

type fakeTurnLoop struct {
	next func() <-chan domain.Event
}

func depsForFake(st *store.Store, runner any) Deps {
	deps := Deps{Store: st}
	if prompt, ok := runner.(PromptTurnService); ok {
		deps.Prompt = prompt
	}
	if turns, ok := runner.(TurnLoopService); ok {
		deps.Turns = turns
	}
	if tools, ok := runner.(ToolTurnService); ok {
		deps.Tools = tools
	}
	if pending, ok := runner.(PendingToolService); ok {
		deps.Pending = pending
	}
	if compact, ok := runner.(CompactService); ok {
		deps.Compact = compact
	}
	if errors, ok := runner.(TurnErrorHandler); ok {
		deps.Errors = errors
	}
	return deps
}

func (f fakeTurnLoop) MaxSteps() int { return 1 }

func (f fakeTurnLoop) PauseLimit(context.Context, *TurnState, chan<- domain.Event) {}

func (f fakeTurnLoop) Step(_ context.Context, _ *TurnState, _ int, _ []provider.InstructionBlock, out chan<- domain.Event) (TurnStepResult, error) {
	events := f.next()
	waitingApproval := false
	for evt := range events {
		if evt.Kind == domain.EventKindApprovalAsk {
			waitingApproval = true
		}
		out <- evt
	}
	return TurnStepResult{Done: !waitingApproval, WaitingApproval: waitingApproval}, nil
}

func (f *queuedSteerBoundaryRunner) PreparePromptTurn(ctx context.Context, turn *TurnState, prompt string, _ []attachment.Draft, _ []reference.Draft, _ string, out chan<- domain.Event) ([]provider.InstructionBlock, error) {
	item, err := turn.AppendUserMessage(ctx, domain.UserMessage{Text: prompt})
	if err != nil {
		return nil, err
	}
	out <- domain.Event{Kind: domain.EventKindStatus, Text: "User message added", Item: item}
	return nil, nil
}

func (f *queuedSteerBoundaryRunner) PrepareContinueTurn(context.Context, *TurnState, string, chan<- domain.Event) ([]provider.InstructionBlock, error) {
	return nil, nil
}

func (f *queuedSteerBoundaryRunner) NewTurnLoop(*TurnState) TurnLoop {
	return queuedSteerBoundaryLoop{runner: f}
}

func (queuedSteerBoundaryLoop) MaxSteps() int { return 2 }

func (queuedSteerBoundaryLoop) PauseLimit(context.Context, *TurnState, chan<- domain.Event) {}

func (l queuedSteerBoundaryLoop) Step(_ context.Context, turn *TurnState, step int, _ []provider.InstructionBlock, _ chan<- domain.Event) (TurnStepResult, error) {
	switch step {
	case 0:
		close(l.runner.step0Started)
		<-l.runner.continueStep
		return TurnStepResult{Continue: true}, nil
	default:
		l.runner.mu.Lock()
		l.runner.step1Timeline = turn.Timeline()
		l.runner.mu.Unlock()
		close(l.runner.step1Done)
		return TurnStepResult{Done: true}, nil
	}
}

func (f *cancelAwareRunner) PreparePromptTurn(ctx context.Context, turn *TurnState, prompt string, _ []attachment.Draft, _ []reference.Draft, _ string, out chan<- domain.Event) ([]provider.InstructionBlock, error) {
	f.ctxSeen <- ctx
	item, err := turn.AppendUserMessage(ctx, domain.UserMessage{Text: prompt})
	if err != nil {
		return nil, err
	}
	out <- domain.Event{Kind: domain.EventKindStatus, Text: "User message added", Item: item}
	return nil, nil
}

func (f *cancelAwareRunner) PrepareContinueTurn(ctx context.Context, _ *TurnState, _ string, _ chan<- domain.Event) ([]provider.InstructionBlock, error) {
	f.ctxSeen <- ctx
	return nil, nil
}

func (f *cancelAwareRunner) NewTurnLoop(*TurnState) TurnLoop {
	return fakeTurnLoop{next: func() <-chan domain.Event { return f.events }}
}

func (f *runtimeFakeRunner) PreparePromptTurn(ctx context.Context, turn *TurnState, prompt string, _ []attachment.Draft, _ []reference.Draft, note string, out chan<- domain.Event) ([]provider.InstructionBlock, error) {
	f.mu.Lock()
	f.promptCalls++
	f.prompts = append(f.prompts, prompt)
	f.promptNotes = append(f.promptNotes, note)
	f.mu.Unlock()
	item, err := turn.AppendUserMessage(ctx, domain.UserMessage{Text: prompt})
	if err != nil {
		return nil, err
	}
	f.mu.Lock()
	f.promptTimeline = append(f.promptTimeline, turn.Timeline())
	f.mu.Unlock()
	out <- domain.Event{Kind: domain.EventKindStatus, Text: "User message added", Item: item}
	return nil, nil
}

func (f *turnPromptFakeRunner) PreparePromptTurn(ctx context.Context, turn *TurnState, prompt string, _ []attachment.Draft, _ []reference.Draft, note string, out chan<- domain.Event) ([]provider.InstructionBlock, error) {
	f.mu.Lock()
	f.promptCalls++
	f.prompts = append(f.prompts, prompt)
	f.promptNotes = append(f.promptNotes, note)
	f.mu.Unlock()

	item, err := turn.AppendUserMessage(ctx, domain.UserMessage{Text: prompt})
	if err != nil {
		return nil, err
	}
	out <- domain.Event{Kind: domain.EventKindStatus, Text: "User message added", Item: item}
	out <- domain.Event{Kind: domain.EventKindMessageDone}
	return nil, nil
}

func (f *controlledTurnPromptRunner) PreparePromptTurn(ctx context.Context, turn *TurnState, prompt string, _ []attachment.Draft, _ []reference.Draft, _ string, out chan<- domain.Event) ([]provider.InstructionBlock, error) {
	item, err := turn.AppendUserMessage(ctx, domain.UserMessage{Text: prompt})
	if err != nil {
		return nil, err
	}
	f.mu.Lock()
	f.calls++
	f.prompts = append(f.prompts, prompt)
	var events <-chan domain.Event
	if len(f.events) > 0 {
		events = f.events[0]
		f.events = f.events[1:]
	}
	f.mu.Unlock()
	if events == nil {
		ch := make(chan domain.Event)
		close(ch)
		events = ch
	}
	out <- domain.Event{Kind: domain.EventKindStatus, Text: "User message added", Item: item}
	for evt := range events {
		out <- evt
	}
	return nil, nil
}

func (f *controlledTurnPromptRunner) promptCallCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func (f *runtimeFakeRunner) PrepareContinueTurn(_ context.Context, turn *TurnState, note string, _ chan<- domain.Event) ([]provider.InstructionBlock, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.continueCalls++
	f.continueNotes = append(f.continueNotes, note)
	f.turnTimelines = append(f.turnTimelines, len(turn.Timeline()))
	return nil, nil
}

func (f *runtimeFakeRunner) NewTurnLoop(*TurnState) TurnLoop {
	return fakeTurnLoop{next: f.nextEvents}
}

func (f *runtimeFakeRunner) nextEvents() <-chan domain.Event {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.events) == 0 {
		ch := make(chan domain.Event)
		close(ch)
		return ch
	}
	evt := f.events[0]
	f.events = f.events[1:]
	return evt
}

func (f *runtimeFakeRunner) ApproveToolForTurn(_ context.Context, _ *TurnState, _ string, _ *accesssettings.PermissionOverride, out chan<- domain.Event) (bool, error) {
	f.mu.Lock()
	f.approveCalls++
	f.mu.Unlock()
	for evt := range f.nextEvents() {
		out <- evt
	}
	return false, nil
}

func (f *runtimeFakeRunner) DenyToolForTurn(_ context.Context, _ *TurnState, _ string, out chan<- domain.Event) error {
	f.mu.Lock()
	f.denyCalls++
	f.mu.Unlock()
	for evt := range f.nextEvents() {
		out <- evt
	}
	return nil
}

func (f *pendingToolFakeRunner) ResumePendingToolsForTurn(_ context.Context, _ *TurnState, out chan<- domain.Event) (bool, error) {
	f.mu.Lock()
	f.resumeCalls++
	var events <-chan domain.Event
	if len(f.resumeEvents) > 0 {
		events = f.resumeEvents[0]
		f.resumeEvents = f.resumeEvents[1:]
	}
	f.mu.Unlock()
	if events == nil {
		return false, nil
	}
	for evt := range events {
		out <- evt
	}
	return false, nil
}

func (f *pendingToolFakeRunner) resumeCallCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.resumeCalls
}

func (f *runtimeFakeRunner) promptCallCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.promptCalls
}

func (f *runtimeFakeRunner) continueCallCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.continueCalls
}

func (f *runtimeFakeRunner) approveCallCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.approveCalls
}

func (f *runtimeFakeRunner) promptAt(i int) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.prompts[i]
}

func (f *runtimeFakeRunner) promptNoteAt(i int) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.promptNotes[i]
}

func (f *runtimeFakeRunner) continueNoteAt(i int) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.continueNotes[i]
}

func (f *runtimeFakeRunner) turnTimelineLenAt(i int) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.turnTimelines[i]
}

func (f *runtimeFakeRunner) promptTimelineAt(i int) []domain.TimelineItem {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.promptTimeline[i])
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

func createSessionWithPlan(t *testing.T, st *store.Store) (domain.Session, domain.Chat, planning.Plan) {
	t.Helper()
	ctx := context.Background()
	session, err := modeltest.CreateSession(ctx, st, "test", "provider", "model", nil)
	if err != nil {
		t.Fatal(err)
	}
	chat, err := modeltest.DefaultChat(ctx, st, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	plan := planning.Plan{SessionID: session.ID, Summary: "Ship it", Milestones: []planning.Milestone{
		{Ref: "alpha", Title: "Alpha", Status: planning.MilestoneStatusExecuting, Position: 0},
		{Ref: "beta", Title: "Beta", Status: planning.MilestoneStatusPending, Position: 1},
	}}
	if err := modeltest.PutPlan(ctx, st, plan); err != nil {
		t.Fatal(err)
	}
	if _, err := modeltest.AddTodoItems(ctx, st, session.ID, "alpha", []string{"Inspect state", "Write tests"}); err != nil {
		t.Fatal(err)
	}
	return session, chat, plan
}

func newTestChat(t *testing.T, st *store.Store, session domain.Session, chatRecord domain.Chat, runner any) *Chat {
	t.Helper()
	chat, err := Load(context.Background(), session, chatRecord, depsForFake(st, runner), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(chat.Close)
	return chat
}

func TestRuntimeEnqueueStartsPrompt(t *testing.T) {
	st := openTestStore(t)
	session, chat, _ := createSessionWithPlan(t, st)
	events := make(chan domain.Event)
	runner := &runtimeFakeRunner{events: []<-chan domain.Event{events}}
	rt := newTestChat(t, st, session, chat, runner)
	updates, unsub := rt.Subscribe()
	defer unsub()

	rt.Enqueue(QueueItem{Kind: QueueKindUser, Text: "first prompt"})

	deadline := time.After(2 * time.Second)
	for runner.promptCallCount() == 0 {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for prompt start")
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}
	snapshot := rt.Snapshot()
	if len(snapshot.QueuedInputs) != 0 {
		t.Fatalf("queued inputs = %#v", snapshot.QueuedInputs)
	}
	if len(snapshot.Timeline) == 0 {
		t.Fatal("expected optimistic user message")
	}
	last := snapshot.Timeline[len(snapshot.Timeline)-1]
	user, ok := last.Content.(domain.UserMessage)
	if !ok || user.Text != "first prompt" {
		t.Fatalf("last timeline item = %#v", last)
	}
	if user.Source != domain.UserMessageSourceUser {
		t.Fatalf("user source = %q, want %q", user.Source, domain.UserMessageSourceUser)
	}

	events <- domain.Event{Kind: domain.EventKindMessageDone}
	close(events)

	deadline = time.After(2 * time.Second)
	for {
		select {
		case update := <-updates:
			if update.Event != nil && update.Event.Kind == domain.EventKindMessageDone {
				if got := runner.promptCallCount(); got != 1 {
					t.Fatalf("prompt calls = %d", got)
				}
				if got := runner.promptAt(0); got != "first prompt" {
					t.Fatalf("prompt = %q", got)
				}
				return
			}
		case <-deadline:
			t.Fatal("timed out waiting for runtime update")
		}
	}
}

func TestRuntimeArchivedChatDoesNotStartQueuedTurn(t *testing.T) {
	st := openTestStore(t)
	session, chatRecord, _ := createSessionWithPlan(t, st)
	chatRecord.Archived = true
	events := make(chan domain.Event)
	runner := &runtimeFakeRunner{events: []<-chan domain.Event{events}}
	rt := newTestChat(t, st, session, chatRecord, runner)

	rt.Enqueue(QueueItem{Kind: QueueKindUser, Text: "should stay queued"})
	deadline := time.After(2 * time.Second)
	var snapshot Snapshot
	for {
		snapshot = rt.Snapshot()
		if len(snapshot.QueuedInputs) == 1 {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for archived queue: %#v", snapshot.QueuedInputs)
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}
	time.Sleep(50 * time.Millisecond)
	if got := runner.promptCallCount(); got != 0 {
		t.Fatalf("prompt calls = %d, want 0", got)
	}
	if snapshot.Active {
		t.Fatalf("archived chat became active: %#v", snapshot)
	}
	if len(snapshot.QueuedInputs) != 1 || snapshot.QueuedInputs[0].Text != "should stay queued" {
		t.Fatalf("queued inputs = %#v", snapshot.QueuedInputs)
	}
	close(events)
}

func TestRuntimeUnarchiveStartsQueuedTurn(t *testing.T) {
	st := openTestStore(t)
	session, chatRecord, _ := createSessionWithPlan(t, st)
	chatRecord.Archived = true
	events := make(chan domain.Event)
	runner := &runtimeFakeRunner{events: []<-chan domain.Event{events}}
	rt := newTestChat(t, st, session, chatRecord, runner)

	rt.Enqueue(QueueItem{Kind: QueueKindUser, Text: "run after restore"})
	deadline := time.After(2 * time.Second)
	for len(rt.Snapshot().QueuedInputs) == 0 {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for archived queue")
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}
	archived := false
	if _, err := rt.UpdateMetadata(context.Background(), MetadataUpdate{Archived: &archived}); err != nil {
		t.Fatal(err)
	}
	deadline = time.After(2 * time.Second)
	for runner.promptCallCount() == 0 {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for restored chat to start")
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}
	if got := runner.promptAt(0); got != "run after restore" {
		t.Fatalf("prompt = %q", got)
	}
	close(events)
}

func TestRuntimeIdleSteerDispatchesAsTurn(t *testing.T) {
	st := openTestStore(t)
	session, chatRecord, _ := createSessionWithPlan(t, st)
	events := make(chan domain.Event)
	runner := &runtimeFakeRunner{events: []<-chan domain.Event{events}}
	rt := newTestChat(t, st, session, chatRecord, runner)

	rt.Enqueue(QueueItem{Kind: QueueKindSteer, Text: "run this steer"})
	deadline := time.After(2 * time.Second)
	for runner.promptCallCount() == 0 {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for idle steer dispatch")
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}
	if got := runner.promptAt(0); got != "run this steer" {
		t.Fatalf("prompt = %q", got)
	}
	if got := rt.Snapshot().QueuedInputs; len(got) != 0 {
		t.Fatalf("queued inputs = %#v", got)
	}
	events <- domain.Event{Kind: domain.EventKindMessageDone}
	close(events)
}

func TestRuntimeArchiveRequiresIdleChat(t *testing.T) {
	st := openTestStore(t)
	session, chatRecord, _ := createSessionWithPlan(t, st)
	events := make(chan domain.Event)
	runner := &runtimeFakeRunner{events: []<-chan domain.Event{events}}
	rt := newTestChat(t, st, session, chatRecord, runner)

	rt.Enqueue(QueueItem{Kind: QueueKindUser, Text: "busy"})
	deadline := time.After(2 * time.Second)
	for runner.promptCallCount() == 0 {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for prompt start")
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}
	archived := true
	if _, err := rt.UpdateMetadata(context.Background(), MetadataUpdate{Archived: &archived}); err == nil || !strings.Contains(err.Error(), "not idle") {
		t.Fatalf("expected non-idle archive error, got %v", err)
	}
	close(events)
}

func TestRuntimeArchiveRequiresEmptyQueue(t *testing.T) {
	st := openTestStore(t)
	session, chatRecord, _ := createSessionWithPlan(t, st)
	runner := &runtimeFakeRunner{}
	rt := newTestChat(t, st, session, chatRecord, runner)
	rt.ReplaceQueue([]domain.QueuedInput{{
		ID:        id.New(),
		Kind:      domain.QueuedInputKindQueued,
		Delivery:  domain.QueuedInputDeliveryNextTurn,
		Origin:    domain.QueuedInputOriginUser,
		Text:      "later",
		Source:    domain.UserMessageSourceUser,
		Held:      true,
		CreatedAt: time.Now().UTC(),
	}})
	deadline := time.After(2 * time.Second)
	for len(rt.Snapshot().QueuedInputs) == 0 {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for queued input")
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}

	archived := true
	if _, err := rt.UpdateMetadata(context.Background(), MetadataUpdate{Archived: &archived}); err == nil || !strings.Contains(err.Error(), "not idle") {
		t.Fatalf("expected queued archive error, got %v", err)
	}
}

func TestRuntimeQueuedUserWaitsForNextSerialTurn(t *testing.T) {
	st := openTestStore(t)
	session, chatRecord, _ := createSessionWithPlan(t, st)
	firstEvents := make(chan domain.Event)
	secondEvents := make(chan domain.Event)
	runner := &controlledTurnPromptRunner{events: []<-chan domain.Event{firstEvents, secondEvents}}
	rt := newTestChat(t, st, session, chatRecord, runner)

	rt.Enqueue(QueueItem{Kind: QueueKindUser, Source: domain.UserMessageSourceUser, Text: "first prompt"})
	deadline := time.After(2 * time.Second)
	for runner.promptCallCount() == 0 {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for first prompt start")
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}

	rt.Enqueue(QueueItem{Kind: QueueKindUser, Source: domain.UserMessageSourceUser, Text: "queued while busy"})
	deadline = time.After(2 * time.Second)
	var queued domain.QueuedInput
	for {
		snapshot := rt.Snapshot()
		if len(snapshot.QueuedInputs) == 1 && snapshot.QueuedInputs[0].Text == "queued while busy" {
			queued = snapshot.QueuedInputs[0]
			if queued.CreatedAt.IsZero() {
				t.Fatalf("queued input missing CreatedAt: %#v", queued)
			}
			if len(snapshot.Timeline) != 1 {
				t.Fatalf("queued input should not enter transcript while active, got %#v", snapshot.Timeline)
			}
			break
		}
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for queued user: %#v", snapshot)
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}

	errorItem := domain.TimelineItem{
		ID:        NewTimelineID(time.Now().UTC()),
		ChatID:    chatRecord.ID,
		Content:   domain.Notice{Text: "provider failed", Kind: "model_error", Level: "error"},
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
		SealedAt:  time.Now().UTC(),
	}
	firstEvents <- domain.Event{Kind: domain.EventKindError, Err: errTestProviderFailure, Item: errorItem}
	close(firstEvents)
	deadline = time.After(2 * time.Second)
	for runner.promptCallCount() < 2 {
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for queued prompt dispatch: %#v", rt.Snapshot())
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}
	close(secondEvents)
	var seenQueuedUser int
	for _, item := range rt.Snapshot().Timeline {
		if user, ok := item.Content.(domain.UserMessage); ok && user.Text == "queued while busy" {
			seenQueuedUser++
			if user.QueueID != queued.ID {
				t.Fatalf("user queue id = %q, want %q", user.QueueID, queued.ID)
			}
			if !user.QueuedAt.Equal(queued.CreatedAt) {
				t.Fatalf("user queued_at = %s, want %s", user.QueuedAt, queued.CreatedAt)
			}
			if !queued.CreatedAt.Before(item.CreatedAt) && !queued.CreatedAt.Equal(item.CreatedAt) {
				t.Fatalf("queue created_at %s should not be after transcript created_at %s", queued.CreatedAt, item.CreatedAt)
			}
		}
	}
	if seenQueuedUser != 1 {
		t.Fatalf("expected one queued user timeline item, got %d in %#v", seenQueuedUser, rt.Snapshot().Timeline)
	}
}

func TestRuntimeDispatchesQueuedUserAfterPreviousAssistant(t *testing.T) {
	st := openTestStore(t)
	session, chatRecord, _ := createSessionWithPlan(t, st)
	firstEvents := make(chan domain.Event)
	secondEvents := make(chan domain.Event)
	runner := &runtimeFakeRunner{events: []<-chan domain.Event{firstEvents, secondEvents}}
	rt := newTestChat(t, st, session, chatRecord, runner)

	rt.Enqueue(QueueItem{Kind: QueueKindUser, Source: domain.UserMessageSourceUser, Text: "first prompt"})
	deadline := time.After(2 * time.Second)
	for runner.promptCallCount() == 0 {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for first prompt start")
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}

	rt.Enqueue(QueueItem{Kind: QueueKindUser, Source: domain.UserMessageSourceUser, Text: "queued while busy"})
	deadline = time.After(2 * time.Second)
	for {
		snapshot := rt.Snapshot()
		if len(snapshot.QueuedInputs) == 1 && snapshot.QueuedInputs[0].Text == "queued while busy" {
			if len(snapshot.Timeline) != 1 {
				t.Fatalf("queued input should not enter transcript while active, got %#v", snapshot.Timeline)
			}
			break
		}
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for queued user: %#v", snapshot)
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}

	firstEvents <- domain.Event{
		Kind: domain.EventKindMessageDelta,
		Text: "assistant answer",
		Item: domain.TimelineItem{
			ID:        NewTimelineID(time.Now().UTC()),
			ChatID:    chatRecord.ID,
			Content:   domain.AssistantMessage{},
			CreatedAt: time.Now().UTC(),
			UpdatedAt: time.Now().UTC(),
		},
	}
	firstEvents <- domain.Event{Kind: domain.EventKindMessageDone}
	close(firstEvents)

	deadline = time.After(2 * time.Second)
	for runner.promptCallCount() < 2 {
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for queued prompt dispatch: %#v", rt.Snapshot())
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}
	close(secondEvents)

	promptTimeline := runner.promptTimelineAt(1)
	if len(promptTimeline) < 2 {
		t.Fatalf("prompt timeline too short: %#v", promptTimeline)
	}
	last := promptTimeline[len(promptTimeline)-1]
	user, ok := last.Content.(domain.UserMessage)
	if !ok || user.Text != "queued while busy" {
		t.Fatalf("active queued input should be last in prompt timeline, got %#v", last)
	}
	previous := promptTimeline[len(promptTimeline)-2]
	if _, ok := previous.Content.(domain.AssistantMessage); !ok {
		t.Fatalf("expected assistant before active queued input, got %#v", previous)
	}
}

func TestRuntimeAppliesQueuedSteerBeforeAutoContinuingTurn(t *testing.T) {
	st := openTestStore(t)
	session, chatRecord, _ := createSessionWithPlan(t, st)
	runner := &queuedSteerBoundaryRunner{
		step0Started: make(chan struct{}),
		continueStep: make(chan struct{}),
		step1Done:    make(chan struct{}),
	}
	rt := newTestChat(t, st, session, chatRecord, runner)
	updates, unsub := rt.Subscribe()
	defer unsub()

	rt.Enqueue(QueueItem{Kind: QueueKindUser, Text: "first prompt"})
	select {
	case <-runner.step0Started:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for first step")
	}

	rt.Enqueue(QueueItem{Kind: QueueKindSteer, Source: domain.UserMessageSourceSubchat, Text: "subchat is done"})
	rt.Enqueue(QueueItem{Kind: QueueKindSteer, Source: domain.UserMessageSourceSubchat, Text: "also mention the summary"})
	deadline := time.After(2 * time.Second)
	for len(rt.Snapshot().QueuedInputs) != 2 {
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for queued steer: %#v", rt.Snapshot().QueuedInputs)
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}
	for _, item := range rt.Snapshot().Timeline {
		if user, ok := item.Content.(domain.UserMessage); ok && strings.Contains(user.Text, "subchat is done") {
			t.Fatalf("queued steer rendered before boundary: %#v", item)
		}
	}
	close(runner.continueStep)

	var sawQueueRefresh bool
	deadline = time.After(2 * time.Second)
	for !sawQueueRefresh {
		select {
		case update := <-updates:
			if update.Event != nil && update.Event.Kind == domain.EventKindStatus && update.Event.Text == "Applying queued steer..." {
				sawQueueRefresh = true
			}
		case <-deadline:
			t.Fatal("timed out waiting for queued steer application")
		}
	}
	select {
	case <-runner.step1Done:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for second step")
	}
	if got := rt.Snapshot().QueuedInputs; len(got) != 0 {
		t.Fatalf("queued inputs = %#v", got)
	}

	runner.mu.Lock()
	timeline := runner.step1Timeline
	runner.mu.Unlock()
	if len(timeline) < 2 {
		t.Fatalf("step 1 timeline too short: %#v", timeline)
	}
	item := timeline[len(timeline)-1]
	user, ok := item.Content.(domain.UserMessage)
	if !ok {
		t.Fatalf("steer timeline item = %#v", item)
	}
	if want := "subchat is done\n\nalso mention the summary"; user.Text != want {
		t.Fatalf("queued steer text = %q, want %q", user.Text, want)
	}
	if user.Source != domain.UserMessageSourceSubchat {
		t.Fatalf("queued steer source = %q, want %q", user.Source, domain.UserMessageSourceSubchat)
	}
	if user.Delivery != domain.QueuedInputDeliveryTurnBoundary {
		t.Fatalf("queued steer delivery = %q, want %q", user.Delivery, domain.QueuedInputDeliveryTurnBoundary)
	}
}

func TestRuntimeQueuesSecondItemUntilFirstCompletes(t *testing.T) {
	st := openTestStore(t)
	session, chat, _ := createSessionWithPlan(t, st)
	first := make(chan domain.Event)
	second := make(chan domain.Event, 1)
	second <- domain.Event{Kind: domain.EventKindMessageDone}
	close(second)
	runner := &runtimeFakeRunner{events: []<-chan domain.Event{first, second}}
	rt := newTestChat(t, st, session, chat, runner)

	rt.Enqueue(QueueItem{Kind: QueueKindUser, Text: "first"})
	rt.Enqueue(QueueItem{Kind: QueueKindQueued, Text: "second"})

	time.Sleep(100 * time.Millisecond)
	if got := runner.promptCallCount(); got != 1 {
		t.Fatalf("expected one prompt call while first is active, got %d", got)
	}

	first <- domain.Event{Kind: domain.EventKindMessageDone}
	close(first)

	deadline := time.After(2 * time.Second)
	for runner.promptCallCount() < 2 {
		select {
		case <-deadline:
			t.Fatalf("expected second queued prompt to dispatch, got %d calls", runner.promptCallCount())
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}
	if got := runner.promptAt(1); got != "second" {
		t.Fatalf("second prompt = %q", got)
	}
}

func TestRuntimeSendQueueItemNowPromotesSelectedUserItem(t *testing.T) {
	st := openTestStore(t)
	session, chatRecord, _ := createSessionWithPlan(t, st)
	first := make(chan domain.Event)
	second := make(chan domain.Event, 1)
	second <- domain.Event{Kind: domain.EventKindMessageDone}
	close(second)
	runner := &runtimeFakeRunner{events: []<-chan domain.Event{first, second}}
	rt := newTestChat(t, st, session, chatRecord, runner)

	rt.Enqueue(QueueItem{Kind: QueueKindUser, Text: "first"})
	secondID := id.NewAt(time.Now().UTC())
	thirdID := id.NewAt(time.Now().UTC())
	rt.ReplaceQueue([]domain.QueuedInput{
		{
			ID:        secondID,
			Kind:      domain.QueuedInputKindQueued,
			Delivery:  domain.QueuedInputDeliveryNextTurn,
			Origin:    domain.QueuedInputOriginUser,
			Text:      "second",
			Source:    domain.UserMessageSourceUser,
			Held:      true,
			CreatedAt: time.Now().UTC(),
		},
		{
			ID:        thirdID,
			Kind:      domain.QueuedInputKindQueued,
			Delivery:  domain.QueuedInputDeliveryNextTurn,
			Origin:    domain.QueuedInputOriginUser,
			Text:      "third",
			Source:    domain.UserMessageSourceUser,
			Held:      true,
			CreatedAt: time.Now().UTC(),
		},
	})

	deadline := time.After(2 * time.Second)
	for len(rt.Snapshot().QueuedInputs) < 2 {
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for queued inputs: %#v", rt.Snapshot().QueuedInputs)
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}
	rt.SendQueueItemNow(thirdID)
	first <- domain.Event{Kind: domain.EventKindMessageDone}
	close(first)

	deadline = time.After(2 * time.Second)
	for runner.promptCallCount() < 2 {
		select {
		case <-deadline:
			t.Fatalf("expected promoted queue item to dispatch, got %d prompt calls", runner.promptCallCount())
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}
	if got := runner.promptAt(1); got != "third" {
		t.Fatalf("promoted prompt = %q", got)
	}
}

func TestRuntimeSendQueueItemNowDispatchesIdleChatWithoutLeavingQueue(t *testing.T) {
	st := openTestStore(t)
	session, chatRecord, _ := createSessionWithPlan(t, st)
	events := make(chan domain.Event)
	runner := &runtimeFakeRunner{events: []<-chan domain.Event{events}}
	rt := newTestChat(t, st, session, chatRecord, runner)

	queuedID := id.NewAt(time.Now().UTC())
	queuedAt := time.Now().UTC().Add(-time.Minute)
	rt.ReplaceQueue([]domain.QueuedInput{{
		ID:        queuedID,
		Kind:      domain.QueuedInputKindQueued,
		Delivery:  domain.QueuedInputDeliveryNextTurn,
		Origin:    domain.QueuedInputOriginUser,
		Text:      "run now",
		Source:    domain.UserMessageSourceUser,
		Held:      true,
		CreatedAt: queuedAt,
	}})

	rt.SendQueueItemNow(queuedID)
	deadline := time.After(2 * time.Second)
	for {
		snapshot := rt.Snapshot()
		if snapshot.Active && runner.promptCallCount() == 1 && len(snapshot.QueuedInputs) == 0 {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("expected active turn with empty queue, snapshot=%#v prompts=%d", snapshot, runner.promptCallCount())
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}
	if got := runner.promptAt(0); got != "run now" {
		t.Fatalf("prompt = %q", got)
	}
	events <- domain.Event{Kind: domain.EventKindMessageDone}
	close(events)
	deadline = time.After(2 * time.Second)
	for {
		snapshot := rt.Snapshot()
		if snapshot.Status == StatusIdle {
			if len(snapshot.Timeline) == 0 {
				t.Fatalf("expected transcript item, got %#v", snapshot.Timeline)
			}
			user, ok := snapshot.Timeline[0].Content.(domain.UserMessage)
			if !ok {
				t.Fatalf("expected user message, got %#v", snapshot.Timeline[0])
			}
			if user.QueueID != queuedID {
				t.Fatalf("queue id = %q, want %q", user.QueueID, queuedID)
			}
			if !user.QueuedAt.Equal(queuedAt) {
				t.Fatalf("queued_at = %s, want %s", user.QueuedAt, queuedAt)
			}
			return
		}
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for idle snapshot: %#v", snapshot)
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}
}

func TestDrainAndCloseDoesNotDispatchQueuedWork(t *testing.T) {
	st := openTestStore(t)
	session, chatRecord, _ := createSessionWithPlan(t, st)
	events := make(chan domain.Event)
	runner := &runtimeFakeRunner{events: []<-chan domain.Event{events}}
	rt := newTestChat(t, st, session, chatRecord, runner)
	updates, unsub := rt.Subscribe()
	defer unsub()

	rt.Enqueue(QueueItem{Kind: QueueKindUser, Text: "first"})
	rt.Enqueue(QueueItem{Kind: QueueKindQueued, Text: "second"})

	deadline := time.After(2 * time.Second)
	for runner.promptCallCount() == 0 {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for first prompt")
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}

	drained := make(chan error, 1)
	go func() {
		drained <- rt.DrainAndClose(context.Background())
	}()
	waitForDrainUpdate(t, updates)
	events <- domain.Event{Kind: domain.EventKindMessageDone}
	close(events)

	select {
	case err := <-drained:
		if err != nil {
			t.Fatalf("drain: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for drain")
	}
	if got := runner.promptCallCount(); got != 1 {
		t.Fatalf("expected drain to leave queued work undispatched, got %d prompt calls", got)
	}
	reloaded, err := GetChat(context.Background(), st, chatRecord.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(reloaded.QueuedInputs) != 1 || reloaded.QueuedInputs[0].Text != "second" {
		t.Fatalf("expected queued work to remain persisted, got %#v", reloaded.QueuedInputs)
	}
}

func waitForDrainUpdate(t *testing.T, updates <-chan Update) {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case update := <-updates:
			if update.StatusText == "Stopping after current turn" {
				return
			}
		case <-deadline:
			t.Fatal("timed out waiting for drain update")
		}
	}
}

func TestLoadResumesPendingToolCalls(t *testing.T) {
	st := openTestStore(t)
	session, chatRecord, _ := createSessionWithPlan(t, st)
	if _, err := AppendAssistantToolCalls(context.Background(), st, chatRecord.ID, []domain.ToolCall{{
		ToolCallID: "call_1",
		Tool:       domain.ToolKindFileRead,
		Args:       map[string]string{"path": "README.md"},
		Status:     domain.ToolStatusPending,
	}}, "", domain.Usage{}); err != nil {
		t.Fatal(err)
	}
	events := make(chan domain.Event)
	runner := &pendingToolFakeRunner{resumeEvents: []<-chan domain.Event{events}}
	rt := newTestChat(t, st, session, chatRecord, runner)
	updates, unsub := rt.Subscribe()
	defer unsub()

	deadline := time.After(2 * time.Second)
	for runner.resumeCallCount() == 0 {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for pending tool resume")
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}
	events <- domain.Event{Kind: domain.EventKindToolStart, Tool: domain.ToolKindFileRead, ToolCallID: "call_1", Text: "read README.md"}
	close(events)

	for {
		select {
		case update := <-updates:
			if update.Event != nil && update.Event.Kind == domain.EventKindToolStart {
				if update.Status != StatusRunningTools {
					t.Fatalf("expected running tools status, got %s", update.Status)
				}
				return
			}
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for resumed tool event")
		}
	}
}

func TestLoadDoesNotResumeWhenLatestAssistantHasNoPendingToolCalls(t *testing.T) {
	st := openTestStore(t)
	session, chatRecord, _ := createSessionWithPlan(t, st)
	if _, err := AppendAssistantToolCalls(context.Background(), st, chatRecord.ID, []domain.ToolCall{{
		ToolCallID: "old_pending",
		Tool:       domain.ToolKindFileRead,
		Args:       map[string]string{"path": "README.md"},
		Status:     domain.ToolStatusPending,
	}}, "", domain.Usage{}); err != nil {
		t.Fatal(err)
	}
	if _, err := AppendTimeline(context.Background(), st, chatRecord.ID, domain.AssistantMessage{
		Text: "done",
	}); err != nil {
		t.Fatal(err)
	}
	runner := &pendingToolFakeRunner{}
	rt := newTestChat(t, st, session, chatRecord, runner)

	time.Sleep(20 * time.Millisecond)
	if got := runner.resumeCallCount(); got != 0 {
		t.Fatalf("resume call count = %d, want 0", got)
	}
	snapshot := rt.Snapshot()
	if snapshot.Active || snapshot.Status != StatusIdle {
		t.Fatalf("snapshot = %#v, want idle inactive", snapshot)
	}
}

func TestTimelinePageForChatSlicesTailOlderAndAll(t *testing.T) {
	st := openTestStore(t)
	_, chatRecord, _ := createSessionWithPlan(t, st)
	ctx := context.Background()
	for i := 0; i < 6; i++ {
		if _, err := AppendTimeline(ctx, st, chatRecord.ID, domain.UserMessage{Text: fmt.Sprintf("message %d", i+1)}); err != nil {
			t.Fatal(err)
		}
	}

	tail, err := TimelinePageForChat(ctx, st, chatRecord.ID, "", 3, false)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := len(tail.Items), 3; got != want {
		t.Fatalf("tail length = %d, want %d", got, want)
	}
	if tail.Items[0].Seq != 4 || !tail.HasMore || tail.LoadedAll || tail.Before != tail.Items[0].ID || tail.Total != 6 {
		t.Fatalf("unexpected tail page: %#v", tail)
	}

	older, err := TimelinePageForChat(ctx, st, chatRecord.ID, tail.Before, 2, false)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := len(older.Items), 2; got != want {
		t.Fatalf("older length = %d, want %d", got, want)
	}
	if older.Items[0].Seq != 2 || !older.HasMore || older.LoadedAll {
		t.Fatalf("unexpected older page: %#v", older)
	}

	all, err := TimelinePageForChat(ctx, st, chatRecord.ID, "", 2, true)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := len(all.Items), 6; got != want {
		t.Fatalf("all length = %d, want %d", got, want)
	}
	if all.HasMore || !all.LoadedAll || all.Before != all.Items[0].ID {
		t.Fatalf("unexpected all page: %#v", all)
	}
}

func TestLoadMetadataDefersTimelineUntilNeeded(t *testing.T) {
	st := openTestStore(t)
	session, chatRecord, _ := createSessionWithPlan(t, st)
	ctx := context.Background()
	if _, err := AppendTimeline(ctx, st, chatRecord.ID, domain.UserMessage{Text: "loaded later"}); err != nil {
		t.Fatal(err)
	}
	rt, err := LoadMetadata(ctx, session, chatRecord, Deps{Store: st}, nil)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := rt.Snapshot()
	if len(snapshot.Timeline) != 0 {
		t.Fatalf("metadata load included timeline: %#v", snapshot.Timeline)
	}
	if !snapshot.TimelineHasMore || snapshot.TimelineLoadedAll {
		t.Fatalf("metadata snapshot should mark timeline unloaded, got has_more=%v loaded_all=%v", snapshot.TimelineHasMore, snapshot.TimelineLoadedAll)
	}
	if err := rt.EnsureTimeline(ctx); err != nil {
		t.Fatal(err)
	}
	snapshot = rt.Snapshot()
	if len(snapshot.Timeline) != 1 || snapshot.Timeline[0].Content.(domain.UserMessage).Text != "loaded later" {
		t.Fatalf("timeline was not loaded on demand: %#v", snapshot.Timeline)
	}
	if snapshot.TimelineHasMore || !snapshot.TimelineLoadedAll {
		t.Fatalf("loaded snapshot metadata = has_more:%v loaded_all:%v", snapshot.TimelineHasMore, snapshot.TimelineLoadedAll)
	}
}

func TestRewindLiveTimelineFromDeletesTailFromStore(t *testing.T) {
	st := openTestStore(t)
	session, chatRecord, _ := createSessionWithPlan(t, st)
	ctx := context.Background()
	keep, err := AppendTimeline(ctx, st, chatRecord.ID, domain.UserMessage{Text: "keep"})
	if err != nil {
		t.Fatal(err)
	}
	anchor, err := AppendTimeline(ctx, st, chatRecord.ID, domain.Compaction{Status: "failed", Trigger: "manual"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := AppendTimeline(ctx, st, chatRecord.ID, domain.UserMessage{Text: "remove"}); err != nil {
		t.Fatal(err)
	}
	chatRecord.LastKnownContextTokens = 1234
	chatRecord.ContextTokensKnown = true
	if err := UpdateChat(ctx, st, chatRecord); err != nil {
		t.Fatal(err)
	}
	rt := newTestChat(t, st, session, chatRecord, &runtimeFakeRunner{})

	result, err := rt.RewindLiveTimelineFrom(ctx, anchor.ID)
	if err != nil {
		t.Fatal(err)
	}
	if result.RemovedCount != 2 || result.RemainingCount != 1 {
		t.Fatalf("unexpected rewind result: %#v", result)
	}
	snapshot := rt.Snapshot()
	if len(snapshot.Timeline) != 1 || snapshot.Timeline[0].ID != keep.ID {
		t.Fatalf("live timeline was not truncated: %#v", snapshot.Timeline)
	}
	persisted, err := TimelineForChat(ctx, st, chatRecord.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(persisted) != 1 || persisted[0].ID != keep.ID {
		t.Fatalf("stored timeline was not truncated: %#v", persisted)
	}
	updated, err := GetChat(ctx, st, chatRecord.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.LastKnownContextTokens != 0 || updated.ContextTokensKnown {
		t.Fatalf("expected rewind to clear stale context counters, got %#v", updated)
	}
}

func TestRuntimeToolStartStatusUsesToolNameNotPreviewArgs(t *testing.T) {
	st := openTestStore(t)
	session, chatRecord, _ := createSessionWithPlan(t, st)
	events := make(chan domain.Event)
	runner := &runtimeFakeRunner{events: []<-chan domain.Event{events}}
	rt := newTestChat(t, st, session, chatRecord, runner)
	updates, unsub := rt.Subscribe()
	defer unsub()

	rt.Enqueue(QueueItem{Kind: QueueKindQueued, Text: "run it"})
	events <- domain.Event{
		Kind:       domain.EventKindToolStart,
		Tool:       domain.ToolKindExecCommand,
		ToolCallID: "call_exec",
		Text:       "go test ./...",
	}
	close(events)

	for {
		select {
		case update := <-updates:
			if update.Event == nil || update.Event.Kind != domain.EventKindToolStart {
				continue
			}
			if update.Status != StatusRunningTools {
				t.Fatalf("expected running tools status, got %s", update.Status)
			}
			if update.StatusText != "Running ExecCommand" {
				t.Fatalf("expected tool-name status text, got %q", update.StatusText)
			}
			if strings.Contains(update.StatusText, "go test") {
				t.Fatalf("status text leaked preview args: %q", update.StatusText)
			}
			return
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for tool start update")
		}
	}
}

func TestRuntimeToolResultReturnsStatusToWaitingLLM(t *testing.T) {
	st := openTestStore(t)
	session, chatRecord, _ := createSessionWithPlan(t, st)
	events := make(chan domain.Event)
	runner := &runtimeFakeRunner{events: []<-chan domain.Event{events}}
	rt := newTestChat(t, st, session, chatRecord, runner)
	updates, unsub := rt.Subscribe()
	defer unsub()

	rt.Enqueue(QueueItem{Kind: QueueKindQueued, Text: "read it"})
	events <- domain.Event{Kind: domain.EventKindToolStart, Tool: domain.ToolKindFileRead, ToolCallID: "call_read"}
	events <- domain.Event{Kind: domain.EventKindToolResult, Tool: domain.ToolKindFileRead, ToolCallID: "call_read", Text: "ok"}

	deadline := time.After(2 * time.Second)
	for {
		select {
		case update := <-updates:
			if update.Event == nil || update.Event.Kind != domain.EventKindToolResult {
				continue
			}
			if update.Status != StatusWaitingLLM {
				t.Fatalf("expected waiting LLM status after tool result, got %s", update.Status)
			}
			if update.StatusText != "Waiting for LLM response" {
				t.Fatalf("status text = %q", update.StatusText)
			}
			close(events)
			return
		case <-deadline:
			t.Fatalf("timed out waiting for tool result status: %#v", rt.Snapshot())
		}
	}
}

func TestRuntimeDispatchQueuedUsesSelectedItemAndPreservesNote(t *testing.T) {
	st := openTestStore(t)
	session, chat, _ := createSessionWithPlan(t, st)
	events := make(chan domain.Event)
	runner := &runtimeFakeRunner{events: []<-chan domain.Event{events}}
	rt := newTestChat(t, st, session, chat, runner)

	rt.DispatchQueued(
		domain.QueuedInput{ID: "queue-99", Kind: domain.QueuedInputKindQueued, Delivery: domain.QueuedInputDeliveryNextTurn, Origin: domain.QueuedInputOriginUser, Text: "selected", CreatedAt: time.Now().UTC()},
		[]domain.QueuedInput{{ID: "queue-1", Kind: domain.QueuedInputKindQueued, Delivery: domain.QueuedInputDeliveryNextTurn, Origin: domain.QueuedInputOriginUser, Text: "first", CreatedAt: time.Now().UTC()}},
	)

	deadline := time.After(2 * time.Second)
	for runner.promptCallCount() == 0 {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for dispatched prompt")
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}
	if got := runner.promptAt(0); got != "selected" {
		t.Fatalf("prompt = %q", got)
	}
	snapshot := rt.Snapshot()
	if len(snapshot.QueuedInputs) != 1 || snapshot.QueuedInputs[0].Text != "first" {
		t.Fatalf("queued inputs = %#v", snapshot.QueuedInputs)
	}
	events <- domain.Event{Kind: domain.EventKindMessageDone}
	close(events)
}

func TestRuntimeDispatchQueuedTurnPromptDoesNotDuplicateUserMessage(t *testing.T) {
	st := openTestStore(t)
	session, chatRecord, _ := createSessionWithPlan(t, st)
	runner := &turnPromptFakeRunner{}
	rt := newTestChat(t, st, session, chatRecord, runner)

	rt.DispatchQueued(
		domain.QueuedInput{ID: "queue-99", Kind: domain.QueuedInputKindQueued, Delivery: domain.QueuedInputDeliveryNextTurn, Origin: domain.QueuedInputOriginUser, Text: "selected", CreatedAt: time.Now().UTC()},
		nil,
	)

	deadline := time.After(2 * time.Second)
	for {
		snapshot := rt.Snapshot()
		if snapshot.Status == StatusIdle && runner.promptCallCount() == 1 {
			var userMessages []domain.UserMessage
			for _, item := range snapshot.Timeline {
				if user, ok := item.Content.(domain.UserMessage); ok {
					userMessages = append(userMessages, user)
				}
			}
			if len(userMessages) != 1 {
				t.Fatalf("user messages = %#v; timeline = %#v", userMessages, snapshot.Timeline)
			}
			if userMessages[0].Text != "selected" {
				t.Fatalf("user message text = %q", userMessages[0].Text)
			}
			return
		}
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for dispatched turn prompt: %#v", snapshot)
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}
}

func TestRuntimeShowsPromptProgressAsPreprocessingStatus(t *testing.T) {
	st := openTestStore(t)
	session, chat, _ := createSessionWithPlan(t, st)
	rt := newTestChat(t, st, session, chat, &runtimeFakeRunner{})
	updates, unsub := rt.Subscribe()
	defer unsub()

	rt.inbox <- streamEventCmd{
		event: domain.Event{
			Kind: domain.EventKindStatus,
			Text: "Processing prompt 4%",
			Meta: map[string]string{
				domain.EventMetaPromptProgress: "true",
				"processed":                    "4",
				"total":                        "100",
			},
		},
	}

	deadline := time.After(2 * time.Second)
	for {
		select {
		case update := <-updates:
			if update.Event == nil || update.Event.Meta[domain.EventMetaPromptProgress] != "true" {
				continue
			}
			if update.Status != StatusWaitingLLM {
				t.Fatalf("status = %q", update.Status)
			}
			if update.StatusText != "LLM preprocessing 4%" {
				t.Fatalf("status text = %q", update.StatusText)
			}
			return
		case <-deadline:
			t.Fatalf("timed out waiting for prompt progress status: %#v", rt.Snapshot())
		}
	}
}

func TestRuntimeShowsCompactionPromptProgressStatus(t *testing.T) {
	st := openTestStore(t)
	session, chat, _ := createSessionWithPlan(t, st)
	rt := newTestChat(t, st, session, chat, &runtimeFakeRunner{})
	updates, unsub := rt.Subscribe()
	defer unsub()

	rt.inbox <- streamEventCmd{
		event: domain.Event{
			Kind: domain.EventKindStatus,
			Meta: map[string]string{
				domain.EventMetaPromptProgress: "true",
				"compaction":                   "progress",
				"processed":                    "4",
				"total":                        "100",
			},
		},
	}

	deadline := time.After(2 * time.Second)
	for {
		select {
		case update := <-updates:
			if update.Event == nil || update.Event.Meta["compaction"] != "progress" {
				continue
			}
			if update.Status != StatusWaitingLLM {
				t.Fatalf("status = %q", update.Status)
			}
			if update.StatusText != "Compaction pre-processing 4%" {
				t.Fatalf("status text = %q", update.StatusText)
			}
			return
		case <-deadline:
			t.Fatalf("timed out waiting for compaction prompt progress status: %#v", rt.Snapshot())
		}
	}
}

func TestRuntimeShowsCompactionStreamingStatus(t *testing.T) {
	st := openTestStore(t)
	session, chat, _ := createSessionWithPlan(t, st)
	rt := newTestChat(t, st, session, chat, &runtimeFakeRunner{})
	updates, unsub := rt.Subscribe()
	defer unsub()

	rt.inbox <- streamEventCmd{
		event: domain.Event{
			Kind: domain.EventKindStatus,
			Text: "Streaming compacted results (1.5 KB)",
			Meta: map[string]string{"compaction": "streaming"},
		},
	}

	deadline := time.After(2 * time.Second)
	for {
		select {
		case update := <-updates:
			if update.Event == nil || update.Event.Meta["compaction"] != "streaming" {
				continue
			}
			if update.Status != StatusWaitingLLM {
				t.Fatalf("status = %q", update.Status)
			}
			if update.StatusText != "Streaming compacted results (1.5 KB)" {
				t.Fatalf("status text = %q", update.StatusText)
			}
			return
		case <-deadline:
			t.Fatalf("timed out waiting for compaction streaming status: %#v", rt.Snapshot())
		}
	}
}

func TestRuntimeShowsStreamedToolCallDeltaStatus(t *testing.T) {
	st := openTestStore(t)
	session, chat, _ := createSessionWithPlan(t, st)
	rt := newTestChat(t, st, session, chat, &runtimeFakeRunner{})
	updates, unsub := rt.Subscribe()
	defer unsub()

	rt.inbox <- streamEventCmd{
		event: domain.Event{
			Kind: domain.EventKindToolCallDelta,
			Tool: domain.ToolKindFileEdit,
			Meta: map[string]string{"arguments": strings.Repeat("x", 1536)},
		},
	}

	deadline := time.After(2 * time.Second)
	for {
		select {
		case update := <-updates:
			if update.Event == nil || update.Event.Kind != domain.EventKindToolCallDelta {
				continue
			}
			if update.Status != StatusWaitingLLM {
				t.Fatalf("status = %q", update.Status)
			}
			if update.StatusText != "Receiving FileEdit tool call (1.5 KB arguments)" {
				t.Fatalf("status text = %q", update.StatusText)
			}
			return
		case <-deadline:
			t.Fatalf("timed out waiting for tool call delta status: %#v", rt.Snapshot())
		}
	}
}

func TestRuntimePreservesPromptAndContinueNotes(t *testing.T) {
	st := openTestStore(t)
	session, chat, _ := createSessionWithPlan(t, st)
	promptEvents := make(chan domain.Event, 1)
	promptEvents <- domain.Event{Kind: domain.EventKindMessageDone}
	close(promptEvents)
	continueEvents := make(chan domain.Event, 1)
	continueEvents <- domain.Event{Kind: domain.EventKindMessageDone}
	close(continueEvents)
	runner := &runtimeFakeRunner{events: []<-chan domain.Event{promptEvents, continueEvents}}
	rt := newTestChat(t, st, session, chat, runner)

	rt.Enqueue(QueueItem{Kind: QueueKindUser, Text: "prompt", Note: "prompt-note"})
	rt.Enqueue(QueueItem{Kind: QueueKindContinue, Note: "continue-note"})

	deadline := time.After(2 * time.Second)
	for runner.continueCallCount() == 0 {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for continue")
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}
	if got := runner.promptNoteAt(0); got != "prompt-note" {
		t.Fatalf("prompt note = %q", got)
	}
	if got := runner.continueNoteAt(0); got != "continue-note" {
		t.Fatalf("continue note = %q", got)
	}
}

func TestRuntimeContinueTurnUsesLiveTimelineNotStorageSideChannel(t *testing.T) {
	st := openTestStore(t)
	session, chatRecord, _ := createSessionWithPlan(t, st)
	seed, err := AppendTimeline(context.Background(), st, chatRecord.ID, domain.UserMessage{Text: "loaded"})
	if err != nil {
		t.Fatal(err)
	}
	seed.Seal(time.Now().UTC())
	if err := PutTimelineItem(context.Background(), st, seed); err != nil {
		t.Fatal(err)
	}

	events := make(chan domain.Event, 1)
	events <- domain.Event{Kind: domain.EventKindMessageDone}
	close(events)
	runner := &runtimeFakeRunner{events: []<-chan domain.Event{events}}
	rt := newTestChat(t, st, session, chatRecord, runner)

	side, err := AppendTimeline(context.Background(), st, chatRecord.ID, domain.UserMessage{Text: "storage-only"})
	if err != nil {
		t.Fatal(err)
	}
	side.Seal(time.Now().UTC())
	if err := PutTimelineItem(context.Background(), st, side); err != nil {
		t.Fatal(err)
	}

	rt.Enqueue(QueueItem{Kind: QueueKindContinue})
	deadline := time.After(2 * time.Second)
	for runner.continueCallCount() == 0 {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for continue")
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}
	if got := runner.turnTimelineLenAt(0); got != 1 {
		t.Fatalf("expected live loaded timeline only, got %d items", got)
	}
}

func TestRuntimeApproveStartsApprovalStream(t *testing.T) {
	st := openTestStore(t)
	session, chat, _ := createSessionWithPlan(t, st)
	approvalEvents := make(chan domain.Event, 1)
	approvalEvents <- domain.Event{Kind: domain.EventKindApprovalReply, Text: "approved"}
	close(approvalEvents)
	runner := &runtimeFakeRunner{events: []<-chan domain.Event{approvalEvents}}
	rt := newTestChat(t, st, session, chat, runner)
	updates, unsub := rt.Subscribe()
	defer unsub()

	rt.ApproveTool("approval-42")

	deadline := time.After(2 * time.Second)
	for {
		select {
		case update := <-updates:
			if update.Event != nil && update.Event.Kind == domain.EventKindApprovalReply {
				if got := runner.approveCallCount(); got != 1 {
					t.Fatalf("approve calls = %d", got)
				}
				return
			}
		case <-deadline:
			t.Fatal("timed out waiting for approval reply")
		}
	}
}

func TestRuntimeApproveRemovesPendingApprovalImmediately(t *testing.T) {
	st := openTestStore(t)
	session, chatRecord, _ := createSessionWithPlan(t, st)
	if _, err := AppendAssistantToolCalls(context.Background(), st, chatRecord.ID, []domain.ToolCall{{
		ToolCallID: "call_approval",
		Tool:       domain.ToolKindBash,
		Args:       map[string]string{"command": "echo hi"},
		Status:     domain.ToolStatusAwaitingApproval,
	}}, "", domain.Usage{}); err != nil {
		t.Fatal(err)
	}
	runner := &runtimeFakeRunner{}
	rt, err := Load(context.Background(), session, chatRecord, depsForFake(st, runner), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(rt.Close)
	updates, unsub := rt.Subscribe()
	defer unsub()

	rt.ApproveTool("call_approval")

	deadline := time.After(2 * time.Second)
	for {
		select {
		case update := <-updates:
			if update.ApprovalsChanged && len(update.Snapshot.Approvals) == 0 {
				for runner.approveCallCount() == 0 {
					select {
					case <-deadline:
						t.Fatalf("approve calls = %d", runner.approveCallCount())
					default:
						time.Sleep(10 * time.Millisecond)
					}
				}
				return
			}
		case <-deadline:
			t.Fatalf("timed out waiting for approval removal; approvals=%#v", rt.Snapshot().Approvals)
		}
	}
}

func TestLoadWithPendingApprovalStartsWaitingForApproval(t *testing.T) {
	st := openTestStore(t)
	session, chatRecord, _ := createSessionWithPlan(t, st)
	if _, err := AppendAssistantToolCalls(context.Background(), st, chatRecord.ID, []domain.ToolCall{{
		ToolCallID: "call_approval",
		Tool:       domain.ToolKindBash,
		Args:       map[string]string{"command": "echo hi"},
		Status:     domain.ToolStatusAwaitingApproval,
	}}, "", domain.Usage{}); err != nil {
		t.Fatal(err)
	}
	runner := &pendingToolFakeRunner{}
	rt, err := Load(context.Background(), session, chatRecord, depsForFake(st, runner), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(rt.Close)

	snapshot := rt.Snapshot()
	if snapshot.Status != StatusWaitingApproval {
		t.Fatalf("status = %q, want %q", snapshot.Status, StatusWaitingApproval)
	}
	if snapshot.StatusText != "Waiting for approval" {
		t.Fatalf("status text = %q", snapshot.StatusText)
	}
	if got := len(snapshot.Approvals); got != 1 {
		t.Fatalf("approvals = %d, want 1", got)
	}
	if snapshot.Approvals[0].ToolCallID != "call_approval" {
		t.Fatalf("approval tool call = %q", snapshot.Approvals[0].ToolCallID)
	}
	time.Sleep(20 * time.Millisecond)
	if got := runner.resumeCallCount(); got != 0 {
		t.Fatalf("pending tools resumed while waiting for approval: %d calls", got)
	}
}

func TestRuntimeCancelReasonControlsHardOrSoftStop(t *testing.T) {
	session := domain.Session{ID: "session-1"}
	chat := domain.Chat{ID: "chat-2", SessionID: "session-1"}
	cancelled := false
	rt := &Chat{
		session: session,
		chat:    chat,
		state:   NewTimelineState(chat, nil, nil),
		status:  StatusRunningTools,
		active:  true,
		cancel:  func() { cancelled = true },
		running: map[string]struct{}{"call_1": {}},
	}

	rt.handleInterrupt(CancelReasonUserInterrupt)
	if cancelled {
		t.Fatal("expected soft cancel to let running tool finish")
	}
	if rt.cancelState != CancelStateNone {
		t.Fatalf("cancel state = %q", rt.cancelState)
	}
	if rt.statusText != "Stopping after current turn" {
		t.Fatalf("status text = %q", rt.statusText)
	}

	rt.draining = false
	rt.handleInterrupt(CancelReasonUserInterruptHard)
	if !cancelled {
		t.Fatal("expected hard cancel to cancel active work")
	}
	if rt.cancelState != CancelStateCancelling {
		t.Fatalf("cancel state = %q", rt.cancelState)
	}
	if rt.statusText != "Interrupting..." {
		t.Fatalf("status text = %q", rt.statusText)
	}
}

func TestRuntimeCancelCancelsStreamingContextImmediately(t *testing.T) {
	st := openTestStore(t)
	session, chat, _ := createSessionWithPlan(t, st)
	runner := &cancelAwareRunner{
		ctxSeen: make(chan context.Context, 1),
		events:  make(chan domain.Event),
	}
	rt := newTestChat(t, st, session, chat, runner)

	rt.Enqueue(QueueItem{Kind: QueueKindUser, Text: "stream"})

	var runCtx context.Context
	select {
	case runCtx = <-runner.ctxSeen:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for prompt context")
	}

	rt.Cancel(CancelReasonUserInterruptHard)

	select {
	case <-runCtx.Done():
	case <-time.After(250 * time.Millisecond):
		t.Fatal("expected cancel to cancel streaming context immediately")
	}
	close(runner.events)
	deadline := time.After(2 * time.Second)
	for rt.Snapshot().Active {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for runtime to stop")
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}
}

func TestRuntimeHardCancelAfterSoftStopCancelsStreamingContextImmediately(t *testing.T) {
	st := openTestStore(t)
	session, chat, _ := createSessionWithPlan(t, st)
	runner := &cancelAwareRunner{
		ctxSeen: make(chan context.Context, 1),
		events:  make(chan domain.Event),
	}
	rt := newTestChat(t, st, session, chat, runner)

	rt.Enqueue(QueueItem{Kind: QueueKindUser, Text: "stream"})

	var runCtx context.Context
	select {
	case runCtx = <-runner.ctxSeen:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for prompt context")
	}

	rt.StopAfterCurrentTurn()
	select {
	case <-runCtx.Done():
		t.Fatal("soft stop canceled streaming context")
	case <-time.After(100 * time.Millisecond):
	}

	rt.Cancel(CancelReasonUserInterruptHard)
	select {
	case <-runCtx.Done():
	case <-time.After(250 * time.Millisecond):
		t.Fatal("expected second cancel to cancel streaming context immediately")
	}
	close(runner.events)
}

func TestRuntimeHardCancelRemovesPartialAssistantResponse(t *testing.T) {
	st := openTestStore(t)
	session, chatRecord, _ := createSessionWithPlan(t, st)
	events := make(chan domain.Event)
	runner := &runtimeFakeRunner{events: []<-chan domain.Event{events}}
	rt := newTestChat(t, st, session, chatRecord, runner)

	rt.Enqueue(QueueItem{Kind: QueueKindUser, Text: "stream"})
	deadline := time.After(2 * time.Second)
	for runner.promptCallCount() == 0 {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for prompt start")
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}

	assistantID := NewTimelineID(time.Now().UTC())
	events <- domain.Event{
		Kind: domain.EventKindMessageDelta,
		Text: "partial answer",
		Item: domain.TimelineItem{
			ID:        assistantID,
			ChatID:    chatRecord.ID,
			Content:   domain.AssistantMessage{},
			CreatedAt: time.Now().UTC(),
			UpdatedAt: time.Now().UTC(),
		},
	}
	deadline = time.After(2 * time.Second)
	for {
		if assistantTextInSnapshot(rt.Snapshot(), "partial answer") {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for partial assistant: %#v", rt.Snapshot())
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}

	rt.StopAfterCurrentTurn()
	rt.Cancel(CancelReasonUserInterruptHard)
	events <- domain.Event{Kind: domain.EventKindMessageDelta, Text: " late text", Item: domain.TimelineItem{ID: assistantID, ChatID: chatRecord.ID, Content: domain.AssistantMessage{}}}
	close(events)

	deadline = time.After(2 * time.Second)
	for rt.Snapshot().Active {
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for hard cancel close: %#v", rt.Snapshot())
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}
	if assistantTextInSnapshot(rt.Snapshot(), "partial answer") || assistantTextInSnapshot(rt.Snapshot(), "late text") {
		t.Fatalf("partial assistant remained in snapshot: %#v", rt.Snapshot().Timeline)
	}
	timeline, err := TimelineForChat(context.Background(), st, chatRecord.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range timeline {
		if assistant, ok := item.Content.(domain.AssistantMessage); ok && strings.Contains(assistant.Text, "partial answer") {
			t.Fatalf("partial assistant persisted: %#v", item)
		}
	}
}

func assistantTextInSnapshot(snapshot Snapshot, needle string) bool {
	for _, item := range snapshot.Timeline {
		assistant, ok := item.Content.(domain.AssistantMessage)
		if !ok {
			continue
		}
		if strings.Contains(assistant.Text, needle) {
			return true
		}
	}
	return false
}

func TestRuntimeInterruptAndCloseDoesNotPersistNoticeForStreamingInterrupt(t *testing.T) {
	st := openTestStore(t)
	session, chatRecord, _ := createSessionWithPlan(t, st)
	runner := &cancelAwareRunner{
		ctxSeen: make(chan context.Context, 1),
		events:  make(chan domain.Event),
	}
	rt := newTestChat(t, st, session, chatRecord, runner)

	rt.Enqueue(QueueItem{Kind: QueueKindUser, Text: "stream"})

	var runCtx context.Context
	select {
	case runCtx = <-runner.ctxSeen:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for prompt context")
	}

	done := make(chan error, 1)
	go func() {
		done <- rt.InterruptAndClose(context.Background(), domain.NoticeReasonProcessRestart)
	}()

	select {
	case <-runCtx.Done():
	case <-time.After(250 * time.Millisecond):
		t.Fatal("expected restart interrupt to cancel streaming context")
	}
	close(runner.events)

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("interrupt and close: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for interrupt close")
	}

	timeline, err := TimelineForChat(context.Background(), st, chatRecord.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range timeline {
		notice, ok := item.Content.(domain.Notice)
		if ok && notice.Kind == domain.NoticeKindInterrupted {
			t.Fatalf("did not expect interruption notice for streaming interrupt, got %#v", notice)
		}
	}
}

func TestRuntimeDrainAndCloseWithRestartQueuesContinuationWithoutNotice(t *testing.T) {
	st := openTestStore(t)
	session, chatRecord, _ := createSessionWithPlan(t, st)
	runner := &cancelAwareRunner{
		ctxSeen: make(chan context.Context, 1),
		events:  make(chan domain.Event),
	}
	rt := newTestChat(t, st, session, chatRecord, runner)

	rt.Enqueue(QueueItem{Kind: QueueKindUser, Text: "stream"})

	var runCtx context.Context
	select {
	case runCtx = <-runner.ctxSeen:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for prompt context")
	}

	done := make(chan error, 1)
	go func() {
		done <- rt.DrainAndCloseWithInterruptReason(context.Background(), domain.NoticeReasonProcessRestart)
	}()

	select {
	case <-runCtx.Done():
		t.Fatal("expected restart drain to keep streaming context alive")
	case <-time.After(100 * time.Millisecond):
	}
	close(runner.events)

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("drain and close: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for drain close")
	}

	chatRecord, err := GetChat(context.Background(), st, chatRecord.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(chatRecord.QueuedInputs) != 1 ||
		domain.DeliveryForQueuedInput(chatRecord.QueuedInputs[0]) != domain.QueuedInputDeliveryContinue ||
		domain.UserMessageSourceForQueuedInput(chatRecord.QueuedInputs[0]) != domain.UserMessageSourceAutoResume {
		t.Fatalf("expected persisted auto-resume continuation, got %#v", chatRecord.QueuedInputs)
	}
	timeline, err := TimelineForChat(context.Background(), st, chatRecord.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range timeline {
		notice, ok := item.Content.(domain.Notice)
		if ok && notice.Kind == domain.NoticeKindInterrupted {
			t.Fatalf("did not expect interruption notice for graceful restart drain, got %#v", notice)
		}
	}
}

func TestRuntimeRestartShutdownMarksAutoRestart(t *testing.T) {
	st := openTestStore(t)
	session, chatRecord, _ := createSessionWithPlan(t, st)
	runner := &cancelAwareRunner{
		ctxSeen: make(chan context.Context, 1),
		events:  make(chan domain.Event),
	}
	rt := newTestChat(t, st, session, chatRecord, runner)

	rt.Enqueue(QueueItem{Kind: QueueKindUser, Text: "stream"})

	select {
	case <-runner.ctxSeen:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for prompt context")
	}

	done := make(chan error, 1)
	go func() {
		done <- rt.Shutdown(context.Background(), CancelReasonRestartInterrupt)
	}()

	deadline := time.After(2 * time.Second)
	for !rt.Snapshot().Chat.AutoRestart {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for auto-restart marker")
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}
	close(runner.events)

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("shutdown: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for shutdown")
	}

	chatRecord, err := GetChat(context.Background(), st, chatRecord.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !chatRecord.AutoRestart {
		t.Fatalf("expected chat to be marked auto-restart, got %#v", chatRecord)
	}
}

func TestRuntimeStopAfterCurrentTurnDoesNotCancelStreamingContext(t *testing.T) {
	st := openTestStore(t)
	session, chat, _ := createSessionWithPlan(t, st)
	runner := &cancelAwareRunner{
		ctxSeen: make(chan context.Context, 1),
		events:  make(chan domain.Event),
	}
	rt := newTestChat(t, st, session, chat, runner)

	rt.Enqueue(QueueItem{Kind: QueueKindUser, Text: "stream"})

	var runCtx context.Context
	select {
	case runCtx = <-runner.ctxSeen:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for prompt context")
	}

	rt.StopAfterCurrentTurn()

	select {
	case <-runCtx.Done():
		t.Fatal("expected stop-after-current-turn to keep streaming context alive")
	case <-time.After(100 * time.Millisecond):
	}
	if got := rt.Snapshot().StatusText; got != "Stopping after current turn" {
		t.Fatalf("status text = %q", got)
	}
	close(runner.events)
}

func TestRuntimeCompactionCompletionUpdatesContextImmediately(t *testing.T) {
	st := openTestStore(t)
	session, chat, _ := createSessionWithPlan(t, st)
	chat.LastKnownContextTokens = 1200
	chat.ContextTokensKnown = true
	chat.TokenUsage = domain.Usage{PromptTokens: 900, CompletionTokens: 100, CachedTokens: 500, TotalTokens: 1000}
	runner := &runtimeFakeRunner{}
	rt := newTestChat(t, st, session, chat, runner)
	updates, unsub := rt.Subscribe()
	defer unsub()

	item := domain.TimelineItem{
		ID:     "019aa000-0000-7000-8000-000000000999",
		ChatID: chat.ID,
		Seq:    1,
		Content: domain.Compaction{
			Summary:             "summary",
			Status:              "completed",
			BeforeContextTokens: 1200,
			AfterContextTokens:  400,
		},
		CreatedAt: time.Now().UTC(),
	}

	rt.inbox <- streamEventCmd{
		event: domain.Event{
			Kind: domain.EventKindStatus,
			Text: "Session compacted",
			Item: item,
			Meta: map[string]string{"compaction": "completed", "refresh": "details"},
		},
	}

	deadline := time.After(2 * time.Second)
	for {
		select {
		case update := <-updates:
			if update.Event == nil || update.Event.Meta["compaction"] != "completed" {
				continue
			}
			if !update.ContextChanged {
				t.Fatal("expected context change update")
			}
			if got := update.Snapshot.Context.AnchorTokens; got != 400 {
				t.Fatalf("anchor tokens = %d", got)
			}
			if got := update.Snapshot.Context.TotalTokens; got < 400 {
				t.Fatalf("total tokens = %d", got)
			}
			if got := update.Snapshot.Chat.LastKnownContextTokens; got != 400 {
				t.Fatalf("chat last known context = %d", got)
			}
			if update.Snapshot.TokenUsage.HasAnyTokens() || update.Snapshot.Chat.TokenUsage.HasAnyTokens() {
				t.Fatalf("expected token usage reset after compaction, got snapshot=%#v chat=%#v", update.Snapshot.TokenUsage, update.Snapshot.Chat.TokenUsage)
			}
			if !update.Snapshot.Context.Estimated {
				t.Fatal("expected compacted context to be marked estimated")
			}
			if len(update.Snapshot.Timeline) == 0 {
				t.Fatal("expected compaction item in snapshot")
			}
			return
		case <-deadline:
			t.Fatal("timed out waiting for compaction completion update")
		}
	}
}

func TestRuntimeAccumulatesTokenUsageSinceCompaction(t *testing.T) {
	st := openTestStore(t)
	session, chatRecord, _ := createSessionWithPlan(t, st)
	runner := &runtimeFakeRunner{}
	rt := newTestChat(t, st, session, chatRecord, runner)
	updates, unsub := rt.Subscribe()
	defer unsub()

	rt.inbox <- streamEventCmd{event: domain.Event{
		Kind:  domain.EventKindUsage,
		Usage: domain.Usage{PromptTokens: 100, CompletionTokens: 25, CachedTokens: 60, TotalTokens: 125},
	}}
	rt.inbox <- streamEventCmd{event: domain.Event{
		Kind:  domain.EventKindUsage,
		Usage: domain.Usage{PromptTokens: 80, CompletionTokens: 20, CachedTokens: 40, TotalTokens: 100},
	}}

	deadline := time.After(2 * time.Second)
	for {
		select {
		case update := <-updates:
			if update.Snapshot.TokenUsage.TotalTokens < 225 {
				continue
			}
			got := update.Snapshot.TokenUsage
			if got.PromptTokens != 180 || got.CompletionTokens != 45 || got.CachedTokens != 100 || got.TotalTokens != 225 {
				t.Fatalf("unexpected token usage: %#v", got)
			}
			stored, err := GetChat(context.Background(), st, chatRecord.ID)
			if err != nil {
				t.Fatal(err)
			}
			if stored.TokenUsage != got {
				t.Fatalf("expected persisted usage %#v, got %#v", got, stored.TokenUsage)
			}
			return
		case <-deadline:
			t.Fatal("timed out waiting for token usage accumulation")
		}
	}
}

func TestPersistRemapsOptimisticIDsAndReloadsWithoutDuplicates(t *testing.T) {
	st := openTestStore(t)
	session, chatRecord, _ := createSessionWithPlan(t, st)
	runner := &runtimeFakeRunner{}
	rt := newTestChat(t, st, session, chatRecord, runner)

	rt.appendOptimisticUserMessage(domain.QueuedInput{
		ID:        "queue-99",
		Kind:      domain.QueuedInputKindQueued,
		Delivery:  domain.QueuedInputDeliveryNextTurn,
		Origin:    domain.QueuedInputOriginUser,
		Text:      "persist me",
		CreatedAt: time.Now().UTC(),
	}, session, chatRecord)
	before := rt.Snapshot()
	if len(before.Timeline) != 1 || before.Timeline[0].ID == "" {
		t.Fatalf("unexpected optimistic snapshot: %#v", before.Timeline)
	}

	if err := rt.Persist(context.Background(), st); err != nil {
		t.Fatal(err)
	}
	after := rt.Snapshot()
	if len(after.Timeline) != 1 {
		t.Fatalf("timeline after persist = %#v", after.Timeline)
	}
	if after.Timeline[0].ID != before.Timeline[0].ID {
		t.Fatalf("timeline ID changed during persist")
	}
	if err := rt.Persist(context.Background(), st); err != nil {
		t.Fatal(err)
	}

	reloaded, err := Load(context.Background(), session, chatRecord, depsForFake(st, runner), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(reloaded.Close)
	snapshot := reloaded.Snapshot()
	if len(snapshot.Timeline) != 1 {
		t.Fatalf("reloaded timeline = %#v", snapshot.Timeline)
	}
	if user, ok := snapshot.Timeline[0].Content.(domain.UserMessage); !ok || user.Text != "persist me" {
		t.Fatalf("reloaded timeline item = %#v", snapshot.Timeline[0])
	}
}
