package chatruntime

import (
	"context"
	"testing"
	"time"

	"github.com/lkarlslund/koder/internal/attachment"
	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/reference"
)

type runtimeFakeRunner struct {
	promptCalls   int
	continueCalls int
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

func TestRuntimeEnqueueStartsPrompt(t *testing.T) {
	st := openTestStore(t)
	session, chat, _ := createSessionWithPlan(t, st)
	events := make(chan domain.Event)
	runner := &runtimeFakeRunner{events: []<-chan domain.Event{events}}
	mgr := New(nil, st)
	mgr.engine = runner

	rt, err := mgr.Runtime(context.Background(), session, chat)
	if err != nil {
		t.Fatal(err)
	}
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
	mgr := New(nil, st)
	mgr.engine = runner

	rt, err := mgr.Runtime(context.Background(), session, chat)
	if err != nil {
		t.Fatal(err)
	}

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
	mgr := New(nil, st)
	mgr.engine = runner

	rt, err := mgr.Runtime(context.Background(), session, chat)
	if err != nil {
		t.Fatal(err)
	}

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
	mgr := New(nil, st)
	mgr.engine = runner

	rt, err := mgr.Runtime(context.Background(), session, chat)
	if err != nil {
		t.Fatal(err)
	}

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
