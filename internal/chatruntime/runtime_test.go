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
	events        []<-chan domain.Event
}

func (f *runtimeFakeRunner) RunPromptInChat(_ context.Context, _ domain.Session, _ domain.Chat, prompt string, _ []attachment.Draft, _ []reference.Draft, _ string) (<-chan domain.Event, error) {
	f.promptCalls++
	f.prompts = append(f.prompts, prompt)
	if len(f.events) == 0 {
		ch := make(chan domain.Event)
		close(ch)
		return ch, nil
	}
	evt := f.events[0]
	f.events = f.events[1:]
	return evt, nil
}

func (f *runtimeFakeRunner) RunContinueInChat(_ context.Context, _ domain.Session, _ domain.Chat, _ string) (<-chan domain.Event, error) {
	f.continueCalls++
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
	events := make(chan domain.Event, 2)
	events <- domain.Event{Kind: domain.EventKindMessageDone}
	close(events)
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
