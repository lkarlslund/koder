package chat

import (
	"context"
	"testing"
	"time"

	"github.com/lkarlslund/koder/internal/appstate"
	"github.com/lkarlslund/koder/internal/attachment"
	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/reference"
	"github.com/lkarlslund/koder/internal/store"
)

type runtimeFakeRunner struct {
	promptCalls   int
	continueCalls int
	approveCalls  int
	denyCalls     int
	prompts       []string
	promptNotes   []string
	continueNotes []string
	events        []<-chan domain.Event
}

func (f *runtimeFakeRunner) RunPromptInChat(_ context.Context, _ domain.Session, _ domain.Chat, prompt string, _ []attachment.Draft, _ []reference.Draft, note string) (<-chan domain.Event, error) {
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

func (f *runtimeFakeRunner) ApproveInChat(_ context.Context, _ int64, _ int64, _ int64) (<-chan domain.Event, error) {
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

func (f *runtimeFakeRunner) ApproveInChatWithRule(_ context.Context, _ int64, _ int64, _ int64, _ domain.PermissionOverride) (<-chan domain.Event, error) {
	return f.ApproveInChat(context.Background(), 0, 0, 0)
}

func (f *runtimeFakeRunner) DenyInChat(_ context.Context, _ int64, _ int64, _ int64) (<-chan domain.Event, error) {
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
	messages, parts, err := st.PartsForChat(context.Background(), chatRecord.ID)
	if err != nil {
		t.Fatal(err)
	}
	approvals, err := st.PendingApprovalsForChat(context.Background(), chatRecord.ID)
	if err != nil {
		t.Fatal(err)
	}
	chat, err := New(session, chatRecord, messages, parts, approvals, runner, st, nil)
	if err != nil {
		t.Fatal(err)
	}
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
	for runner.promptCalls == 0 {
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
	if len(snapshot.Messages) == 0 {
		t.Fatal("expected optimistic user message")
	}
	last := snapshot.Messages[len(snapshot.Messages)-1]
	if last.Role != domain.MessageRoleUser || last.Summary != "first prompt" {
		t.Fatalf("last message = %#v", last)
	}

	events <- domain.Event{Kind: domain.EventKindMessageDone}
	close(events)

	deadline = time.After(2 * time.Second)
	for {
		select {
		case update := <-updates:
			if update.Event != nil && update.Event.Kind == domain.EventKindMessageDone {
				if runner.promptCalls != 1 {
					t.Fatalf("prompt calls = %d", runner.promptCalls)
				}
				if got := runner.prompts[0]; got != "first prompt" {
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
	if runner.promptCalls != 1 {
		t.Fatalf("expected one prompt call while first is active, got %d", runner.promptCalls)
	}

	first <- domain.Event{Kind: domain.EventKindMessageDone}
	close(first)

	deadline := time.After(2 * time.Second)
	for runner.promptCalls < 2 {
		select {
		case <-deadline:
			t.Fatalf("expected second queued prompt to dispatch, got %d calls", runner.promptCalls)
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}
	if got := runner.prompts[1]; got != "second" {
		t.Fatalf("second prompt = %q", got)
	}
}

func TestRuntimeDispatchQueuedUsesSelectedItemAndPreservesNote(t *testing.T) {
	st := openTestStore(t)
	session, chat, _ := createSessionWithPlan(t, st)
	events := make(chan domain.Event)
	runner := &runtimeFakeRunner{events: []<-chan domain.Event{events}}
	rt := newTestChat(t, st, session, chat, runner)

	rt.DispatchQueued(
		domain.QueuedInput{ID: 99, Kind: domain.QueuedInputKindSteer, Text: "selected", CreatedAt: time.Now().UTC()},
		[]domain.QueuedInput{{ID: 1, Kind: domain.QueuedInputKindQueued, Text: "first", CreatedAt: time.Now().UTC()}},
	)

	deadline := time.After(2 * time.Second)
	for runner.promptCalls == 0 {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for dispatched prompt")
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}
	if got := runner.prompts[0]; got != "selected" {
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
	for runner.continueCalls == 0 {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for continue")
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}
	if got := runner.promptNotes[0]; got != "prompt-note" {
		t.Fatalf("prompt note = %q", got)
	}
	if got := runner.continueNotes[0]; got != "continue-note" {
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

	rt.Approve(42)

	deadline := time.After(2 * time.Second)
	for {
		select {
		case update := <-updates:
			if update.Event != nil && update.Event.Kind == domain.EventKindApprovalReply {
				if runner.approveCalls != 1 {
					t.Fatalf("approve calls = %d", runner.approveCalls)
				}
				return
			}
		case <-deadline:
			t.Fatal("timed out waiting for approval reply")
		}
	}
}

func TestRuntimeCancelWhileToolsRunningStagesThenForcesCancel(t *testing.T) {
	session := domain.Session{ID: 1}
	chat := domain.Chat{ID: 2, SessionID: 1}
	cancelled := false
	rt := &Chat{
		session: session,
		chat:    chat,
		state:   appstate.NewChatState(chat, nil, nil, nil),
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
	if len(snapshot.Messages) != 1 {
		t.Fatalf("expected cancellation notice message, got %#v", snapshot.Messages)
	}
	parts := snapshot.Parts[snapshot.Messages[0].ID]
	if len(parts) != 1 || parts[0].Kind != domain.PartKindEventNotice {
		t.Fatalf("unexpected cancellation notice parts: %#v", parts)
	}
	if got := parts[0].Text(); got != "Cancelling. Tool calls running, waiting for completition. Press ESC again to cancel tool calls." {
		t.Fatalf("unexpected notice body: %q", got)
	}

	rt.handleInterrupt()
	if !cancelled {
		t.Fatal("expected second cancel to force tool cancellation")
	}
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

	msg := domain.Message{
		ID:        999,
		SessionID: session.ID,
		ChatID:    chat.ID,
		Role:      domain.MessageRoleAssistant,
		Summary:   "Compacted session summary",
		CreatedAt: time.Now().UTC(),
	}
	part := domain.Part{
		ID:        1001,
		MessageID: msg.ID,
		Kind:      domain.PartKindCompaction,
		Payload: domain.CompactionPayload{
			Summary:             "summary",
			Status:              "completed",
			BeforeContextTokens: 1200,
			AfterContextTokens:  400,
		},
		CreatedAt: time.Now().UTC(),
	}

	rt.inbox <- streamEventCmd{
		event: domain.Event{
			Kind:    domain.EventKindStatus,
			Text:    "Session compacted",
			Message: msg,
			Parts:   []domain.Part{part},
			Meta:    map[string]string{"compaction": "completed", "refresh": "details"},
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
			if len(update.Snapshot.Messages) == 0 {
				t.Fatal("expected compaction message in snapshot")
			}
			return
		case <-deadline:
			t.Fatal("timed out waiting for compaction completion update")
		}
	}
}
