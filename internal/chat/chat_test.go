package chat

import (
	"context"
	"github.com/lkarlslund/koder/internal/attachment"
	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/reference"
	"github.com/lkarlslund/koder/internal/store"
	"sync"
	"testing"
	"time"
)

type runtimeFakeRunner struct {
	mu            sync.Mutex
	promptCalls   int
	continueCalls int
	approveCalls  int
	denyCalls     int
	prompts       []string
	promptNotes   []string
	continueNotes []string
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

func (f *cancelAwareRunner) RunPromptInChat(ctx context.Context, _ domain.Session, _ domain.Chat, _ string, _ []attachment.Draft, _ []reference.Draft, _ string) (<-chan domain.Event, error) {
	f.ctxSeen <- ctx
	return f.events, nil
}

func (f *cancelAwareRunner) RunContinueInChat(ctx context.Context, _ domain.Session, _ domain.Chat, _ string) (<-chan domain.Event, error) {
	f.ctxSeen <- ctx
	return f.events, nil
}

func (f *cancelAwareRunner) ApproveToolInChat(context.Context, domain.ID, domain.ID, string) (<-chan domain.Event, error) {
	ch := make(chan domain.Event)
	close(ch)
	return ch, nil
}

func (f *cancelAwareRunner) ApproveToolInChatWithRule(context.Context, domain.ID, domain.ID, string, domain.PermissionOverride) (<-chan domain.Event, error) {
	ch := make(chan domain.Event)
	close(ch)
	return ch, nil
}

func (f *cancelAwareRunner) DenyToolInChat(context.Context, domain.ID, domain.ID, string) (<-chan domain.Event, error) {
	ch := make(chan domain.Event)
	close(ch)
	return ch, nil
}

func (f *runtimeFakeRunner) RunPromptInChat(_ context.Context, _ domain.Session, _ domain.Chat, prompt string, _ []attachment.Draft, _ []reference.Draft, note string) (<-chan domain.Event, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.promptCalls++
	f.prompts = append(f.prompts, prompt)
	f.promptNotes = append(f.promptNotes, note)
	if len(f.events) == 0 {
		ch := make(chan domain.Event)
		close(ch)
		return ch, nil
	}
	evt := f.events[0]
	f.events = f.events[1:]
	return evt, nil
}

func (f *runtimeFakeRunner) RunContinueInChat(_ context.Context, _ domain.Session, _ domain.Chat, note string) (<-chan domain.Event, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.continueCalls++
	f.continueNotes = append(f.continueNotes, note)
	if len(f.events) == 0 {
		ch := make(chan domain.Event)
		close(ch)
		return ch, nil
	}
	evt := f.events[0]
	f.events = f.events[1:]
	return evt, nil
}

func (f *runtimeFakeRunner) ApproveToolInChat(_ context.Context, _ domain.ID, _ domain.ID, _ string) (<-chan domain.Event, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.approveCalls++
	if len(f.events) == 0 {
		ch := make(chan domain.Event)
		close(ch)
		return ch, nil
	}
	evt := f.events[0]
	f.events = f.events[1:]
	return evt, nil
}

func (f *runtimeFakeRunner) ApproveToolInChatWithRule(_ context.Context, _ domain.ID, _ domain.ID, toolCallID string, _ domain.PermissionOverride) (<-chan domain.Event, error) {
	return f.ApproveToolInChat(context.Background(), "", "", toolCallID)
}

func (f *runtimeFakeRunner) DenyToolInChat(_ context.Context, _ domain.ID, _ domain.ID, _ string) (<-chan domain.Event, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.denyCalls++
	if len(f.events) == 0 {
		ch := make(chan domain.Event)
		close(ch)
		return ch, nil
	}
	evt := f.events[0]
	f.events = f.events[1:]
	return evt, nil
}

func (f *pendingToolFakeRunner) ResumePendingToolCallsInChat(_ context.Context, _ domain.Session, _ domain.Chat) (<-chan domain.Event, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.resumeCalls++
	if len(f.resumeEvents) == 0 {
		return nil, nil
	}
	events := f.resumeEvents[0]
	f.resumeEvents = f.resumeEvents[1:]
	return events, nil
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

func newTestChat(t *testing.T, st *store.Store, session domain.Session, chatRecord domain.Chat, runner Runner) *Chat {
	t.Helper()
	chat, err := Load(context.Background(), st, session, chatRecord, runner, nil)
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
	rt, err := Load(context.Background(), st, session, chatRecord, runner, nil)
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
				if got := runner.approveCallCount(); got != 1 {
					t.Fatalf("approve calls = %d", got)
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
	rt, err := Load(context.Background(), st, session, chatRecord, runner, nil)
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
	if got := notice.Text; got != "Cancelling. Tool calls running, waiting for completition. Press ESC again to cancel tool calls." {
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

	reloaded, err := Load(context.Background(), st, session, chatRecord, runner, nil)
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
