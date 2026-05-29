package chat

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lkarlslund/koder/internal/attachment"
	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/provider"
	"github.com/lkarlslund/koder/internal/reference"
	"github.com/lkarlslund/koder/internal/store"
)

var errTestProviderFailure = errors.New("provider failed")

type runtimeFakeRunner struct {
	mu            sync.Mutex
	promptCalls   int
	continueCalls int
	approveCalls  int
	denyCalls     int
	prompts       []string
	promptNotes   []string
	continueNotes []string
	turnTimelines []int
	events        []<-chan domain.Event
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

func (f *runtimeFakeRunner) ApproveToolForTurn(_ context.Context, _ *TurnState, _ string, _ *domain.PermissionOverride, out chan<- domain.Event) (bool, error) {
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
		{Ref: "alpha", Title: "Alpha", Status: domain.MilestoneStatusExecuting, Position: 0},
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

	rt.Enqueue(QueueItem{Kind: QueueKindSteer, Text: "first prompt"})

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
	if user.Source != domain.UserMessageSourceSteer {
		t.Fatalf("user source = %q, want %q", user.Source, domain.UserMessageSourceSteer)
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

func TestRuntimeRendersUserQueuedWhileBusyBeforeActiveError(t *testing.T) {
	st := openTestStore(t)
	session, chatRecord, _ := createSessionWithPlan(t, st)
	firstEvents := make(chan domain.Event)
	secondEvents := make(chan domain.Event)
	runner := &controlledTurnPromptRunner{events: []<-chan domain.Event{firstEvents, secondEvents}}
	rt := newTestChat(t, st, session, chatRecord, runner)
	updates, unsub := rt.Subscribe()
	defer unsub()

	rt.Enqueue(QueueItem{Kind: QueueKindSteer, Source: domain.UserMessageSourceUser, Text: "first prompt"})
	deadline := time.After(2 * time.Second)
	for runner.promptCallCount() == 0 {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for first prompt start")
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}

	rt.Enqueue(QueueItem{Kind: QueueKindSteer, Source: domain.UserMessageSourceUser, Text: "queued while busy"})
	deadline = time.After(2 * time.Second)
	for {
		timeline := rt.Snapshot().Timeline
		if len(timeline) >= 2 {
			user, ok := timeline[1].Content.(domain.UserMessage)
			if ok && user.Text == "queued while busy" {
				if user.Source != domain.UserMessageSourceUser {
					t.Fatalf("queued user source = %q, want %q", user.Source, domain.UserMessageSourceUser)
				}
				if got := rt.Snapshot().QueuedInputs; len(got) != 0 {
					t.Fatalf("rendered user input should be hidden from visible queue, got %#v", got)
				}
				break
			}
		}
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for queued user render: %#v", timeline)
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}

	errorItem := domain.TimelineItem{
		ID:        domain.NewTimelineID(time.Now().UTC()),
		ChatID:    chatRecord.ID,
		Content:   domain.Notice{Text: "provider failed", Kind: "model_error", Level: "error"},
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
		SealedAt:  time.Now().UTC(),
	}
	firstEvents <- domain.Event{Kind: domain.EventKindError, Err: errTestProviderFailure, Item: errorItem}
	deadline = time.After(2 * time.Second)
	for {
		select {
		case update := <-updates:
			if update.Event == nil || update.Event.Kind != domain.EventKindError {
				continue
			}
			timeline := rt.Snapshot().Timeline
			if len(timeline) < 3 {
				t.Fatalf("expected user before error, got %#v", timeline)
			}
			user, ok := timeline[1].Content.(domain.UserMessage)
			if !ok || user.Text != "queued while busy" {
				t.Fatalf("expected queued user before error, got %#v", timeline)
			}
			if _, ok := timeline[2].Content.(domain.Notice); !ok {
				t.Fatalf("expected error notice after queued user, got %#v", timeline)
			}
			close(firstEvents)
			goto dispatched
		case <-deadline:
			t.Fatalf("timed out waiting for error: %#v", rt.Snapshot())
		}
	}

dispatched:
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
	var seenQueued int
	for _, item := range rt.Snapshot().Timeline {
		if user, ok := item.Content.(domain.UserMessage); ok && user.Text == "queued while busy" {
			seenQueued++
		}
	}
	if seenQueued != 1 {
		t.Fatalf("expected one queued user timeline item, got %d in %#v", seenQueued, rt.Snapshot().Timeline)
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

	rt.Enqueue(QueueItem{Kind: QueueKindSteer, Text: "first"})
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

func TestRuntimeSendQueueItemNowPromotesSelectedItemToSteer(t *testing.T) {
	st := openTestStore(t)
	session, chatRecord, _ := createSessionWithPlan(t, st)
	first := make(chan domain.Event)
	second := make(chan domain.Event, 1)
	second <- domain.Event{Kind: domain.EventKindMessageDone}
	close(second)
	runner := &runtimeFakeRunner{events: []<-chan domain.Event{first, second}}
	rt := newTestChat(t, st, session, chatRecord, runner)

	rt.Enqueue(QueueItem{Kind: QueueKindSteer, Text: "first"})
	rt.Enqueue(QueueItem{Kind: QueueKindQueued, Text: "second"})
	rt.Enqueue(QueueItem{Kind: QueueKindQueued, Text: "third"})

	deadline := time.After(2 * time.Second)
	for len(rt.Snapshot().QueuedInputs) < 2 {
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for queued inputs: %#v", rt.Snapshot().QueuedInputs)
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}
	var thirdID domain.ID
	for _, item := range rt.Snapshot().QueuedInputs {
		if item.Text == "third" {
			thirdID = item.ID
			break
		}
	}
	if thirdID == "" {
		t.Fatalf("third item not queued: %#v", rt.Snapshot().QueuedInputs)
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

func TestDrainAndCloseDoesNotDispatchQueuedWork(t *testing.T) {
	st := openTestStore(t)
	session, chatRecord, _ := createSessionWithPlan(t, st)
	events := make(chan domain.Event)
	runner := &runtimeFakeRunner{events: []<-chan domain.Event{events}}
	rt := newTestChat(t, st, session, chatRecord, runner)
	updates, unsub := rt.Subscribe()
	defer unsub()

	rt.Enqueue(QueueItem{Kind: QueueKindSteer, Text: "first"})
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
	reloaded, err := st.GetChat(context.Background(), chatRecord.ID)
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
	events <- domain.Event{Kind: domain.EventKindToolStart, Tool: domain.ToolKindRead, ToolCallID: "call_1", Text: "read README.md"}
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
			if update.StatusText != "Running exec_command" {
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
	events <- domain.Event{Kind: domain.EventKindToolStart, Tool: domain.ToolKindRead, ToolCallID: "call_read"}
	events <- domain.Event{Kind: domain.EventKindToolResult, Tool: domain.ToolKindRead, ToolCallID: "call_read", Text: "ok"}

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
		domain.QueuedInput{ID: "queue-99", Kind: domain.QueuedInputKindSteer, Text: "selected", CreatedAt: time.Now().UTC()},
		[]domain.QueuedInput{{ID: "queue-1", Kind: domain.QueuedInputKindQueued, Text: "first", CreatedAt: time.Now().UTC()}},
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
		domain.QueuedInput{ID: "queue-99", Kind: domain.QueuedInputKindSteer, Text: "selected", CreatedAt: time.Now().UTC()},
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

func TestRuntimeRefreshesQueueWhenRunnerConsumesQueuedSteer(t *testing.T) {
	st := openTestStore(t)
	session, chat, _ := createSessionWithPlan(t, st)
	events := make(chan domain.Event)
	runner := &runtimeFakeRunner{events: []<-chan domain.Event{events}}
	rt := newTestChat(t, st, session, chat, runner)

	rt.Enqueue(QueueItem{Kind: QueueKindSteer, Text: "active"})
	deadline := time.After(2 * time.Second)
	for runner.promptCallCount() == 0 {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for prompt start")
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}
	rt.Enqueue(QueueItem{Kind: QueueKindQueued, Text: "queued steer"})
	deadline = time.After(2 * time.Second)
	var queuedID domain.ID
	for queuedID == "" {
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for queued input: %#v", rt.Snapshot().QueuedInputs)
		default:
			snapshot := rt.Snapshot()
			if len(snapshot.QueuedInputs) == 1 {
				queuedID = snapshot.QueuedInputs[0].ID
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	rt.SendQueueItemNow(queuedID)
	deadline = time.After(2 * time.Second)
	for {
		snapshot := rt.Snapshot()
		if len(snapshot.QueuedInputs) == 1 && snapshot.QueuedInputs[0].Kind == domain.QueuedInputKindSteer {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for steer promotion: %#v", snapshot.QueuedInputs)
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}

	updates, unsub := rt.Subscribe()
	defer unsub()
	if err := st.SetChatQueuedInputs(context.Background(), chat.ID, nil); err != nil {
		t.Fatal(err)
	}
	events <- domain.Event{
		Kind: domain.EventKindStatus,
		Text: "Applying queued steer...",
		Meta: map[string]string{domain.EventMetaRefresh: domain.EventRefreshQueue},
	}

	deadline = time.After(2 * time.Second)
	for {
		select {
		case update := <-updates:
			if update.QueueChanged {
				if len(update.Queue) != 0 {
					t.Fatalf("queue update = %#v", update.Queue)
				}
				if got := rt.Snapshot().QueuedInputs; len(got) != 0 {
					t.Fatalf("snapshot queued inputs = %#v", got)
				}
				events <- domain.Event{Kind: domain.EventKindMessageDone}
				close(events)
				return
			}
		case <-deadline:
			t.Fatalf("timed out waiting for queue refresh: %#v", rt.Snapshot().QueuedInputs)
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

func TestRuntimeShowsStreamedToolCallDeltaStatus(t *testing.T) {
	st := openTestStore(t)
	session, chat, _ := createSessionWithPlan(t, st)
	rt := newTestChat(t, st, session, chat, &runtimeFakeRunner{})
	updates, unsub := rt.Subscribe()
	defer unsub()

	rt.inbox <- streamEventCmd{
		event: domain.Event{
			Kind: domain.EventKindToolCallDelta,
			Tool: domain.ToolKindEdit,
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
			if update.StatusText != "Receiving edit tool call (1.5 KB arguments)" {
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

	rt.Enqueue(QueueItem{Kind: QueueKindSteer, Text: "prompt", Note: "prompt-note"})
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
	seed, err := st.AppendTimeline(context.Background(), chatRecord.ID, domain.UserMessage{Text: "loaded"})
	if err != nil {
		t.Fatal(err)
	}
	seed.Seal(time.Now().UTC())
	if err := st.Timeline().Put(context.Background(), seed); err != nil {
		t.Fatal(err)
	}

	events := make(chan domain.Event, 1)
	events <- domain.Event{Kind: domain.EventKindMessageDone}
	close(events)
	runner := &runtimeFakeRunner{events: []<-chan domain.Event{events}}
	rt := newTestChat(t, st, session, chatRecord, runner)

	side, err := st.AppendTimeline(context.Background(), chatRecord.ID, domain.UserMessage{Text: "storage-only"})
	if err != nil {
		t.Fatal(err)
	}
	side.Seal(time.Now().UTC())
	if err := st.Timeline().Put(context.Background(), side); err != nil {
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

	rt.Approve("approval-42")

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
	if _, err := st.AppendAssistantToolCalls(context.Background(), chatRecord.ID, []domain.ToolCall{{
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
	if _, err := st.AppendAssistantToolCalls(context.Background(), chatRecord.ID, []domain.ToolCall{{
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

func TestRuntimeCancelWhileToolsRunningStagesThenForcesCancel(t *testing.T) {
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

	rt.handleInterrupt()
	if cancelled {
		t.Fatal("expected first cancel to wait for tool completion")
	}
	if rt.cancelState != CancelStateCancelling {
		t.Fatalf("cancel state = %q", rt.cancelState)
	}
	if rt.statusText != "Cancelling..." {
		t.Fatalf("status text = %q", rt.statusText)
	}
	snapshot := rt.Snapshot()
	if len(snapshot.Timeline) != 1 {
		t.Fatalf("expected cancellation notice item, got %#v", snapshot.Timeline)
	}
	notice, ok := snapshot.Timeline[0].Content.(domain.Notice)
	if !ok {
		t.Fatalf("unexpected cancellation notice item: %#v", snapshot.Timeline[0])
	}
	if got := notice.Text; got != "Cancelling. Tool calls running, waiting for completion. Press ESC again to cancel tool calls." {
		t.Fatalf("unexpected notice body: %q", got)
	}

	rt.handleInterrupt()
	if !cancelled {
		t.Fatal("expected second cancel to force tool cancellation")
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

	rt.Enqueue(QueueItem{Kind: QueueKindSteer, Text: "stream"})

	var runCtx context.Context
	select {
	case runCtx = <-runner.ctxSeen:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for prompt context")
	}

	rt.Cancel()

	select {
	case <-runCtx.Done():
	case <-time.After(250 * time.Millisecond):
		t.Fatal("expected cancel to cancel streaming context immediately")
	}
	close(runner.events)
}

func TestRuntimeInterruptAndClosePersistsRestartReason(t *testing.T) {
	st := openTestStore(t)
	session, chatRecord, _ := createSessionWithPlan(t, st)
	runner := &cancelAwareRunner{
		ctxSeen: make(chan context.Context, 1),
		events:  make(chan domain.Event),
	}
	rt := newTestChat(t, st, session, chatRecord, runner)

	rt.Enqueue(QueueItem{Kind: QueueKindSteer, Text: "stream"})

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

	timeline, err := st.TimelineForChat(context.Background(), chatRecord.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(timeline) == 0 {
		t.Fatal("expected persisted interruption notice")
	}
	notice, ok := timeline[len(timeline)-1].Content.(domain.Notice)
	if !ok || notice.Kind != domain.NoticeKindInterrupted || notice.Reason != domain.NoticeReasonProcessRestart {
		t.Fatalf("expected restart interruption notice, got %#v", timeline[len(timeline)-1].Content)
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

	rt.Enqueue(QueueItem{Kind: QueueKindSteer, Text: "stream"})

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

func TestPersistRemapsOptimisticIDsAndReloadsWithoutDuplicates(t *testing.T) {
	st := openTestStore(t)
	session, chatRecord, _ := createSessionWithPlan(t, st)
	runner := &runtimeFakeRunner{}
	rt := newTestChat(t, st, session, chatRecord, runner)

	rt.appendOptimisticUserMessage(domain.QueuedInput{
		ID:        "queue-99",
		Kind:      domain.QueuedInputKindSteer,
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
