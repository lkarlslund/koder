package agent

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/id"
)

func TestCavemanServiceLimitsParallelWork(t *testing.T) {
	t.Parallel()

	service := newCavemanService(1)
	firstStarted := make(chan struct{})
	release := make(chan struct{})
	var running int32
	first := service.Submit(context.Background(), func(context.Context) (string, error) {
		atomic.AddInt32(&running, 1)
		close(firstStarted)
		<-release
		atomic.AddInt32(&running, -1)
		return "first", nil
	})
	select {
	case <-firstStarted:
	case <-time.After(time.Second):
		t.Fatal("expected first job to start")
	}
	secondStarted := make(chan struct{}, 1)
	second := service.Submit(context.Background(), func(context.Context) (string, error) {
		secondStarted <- struct{}{}
		return "second", nil
	})

	if atomic.LoadInt32(&running) != 1 {
		t.Fatalf("expected first job to run, running=%d", running)
	}
	select {
	case <-secondStarted:
		t.Fatal("expected second job to wait for parallelism slot")
	default:
	}
	close(release)
	if got, err := first.Await(context.Background()); err != nil || got != "first" {
		t.Fatalf("first result = %q, %v", got, err)
	}
	if got, err := second.Await(context.Background()); err != nil || got != "second" {
		t.Fatalf("second result = %q, %v", got, err)
	}
}

func TestCavemanJobCanBeAwaitedMoreThanOnce(t *testing.T) {
	t.Parallel()

	service := newCavemanService(1)
	job := service.Submit(context.Background(), func(context.Context) (string, error) {
		return "me done", nil
	})

	for i := 0; i < 2; i++ {
		got, err := job.Await(context.Background())
		if err != nil || got != "me done" {
			t.Fatalf("await %d result = %q, %v", i+1, got, err)
		}
	}
}

func TestEngineWaitsForOutstandingCaveman(t *testing.T) {
	t.Parallel()

	chatID := id.ID("chat-1")
	release := make(chan struct{})
	service := newCavemanService(1)
	job := service.Submit(context.Background(), func(context.Context) (string, error) {
		<-release
		return "me finish", nil
	})
	engine := &Engine{cavemanJobs: map[id.ID]cavemanJob{chatID: job}}
	events := make(chan domain.Event, 1)
	done := make(chan error, 1)

	go func() {
		done <- engine.awaitOutstandingCaveman(context.Background(), chatID, events)
	}()

	select {
	case err := <-done:
		t.Fatalf("expected wait before caveman completes, got %v", err)
	case evt := <-events:
		if evt.Kind != domain.EventKindStatus || !strings.Contains(evt.Text, "Waiting for caveman") {
			t.Fatalf("expected waiting status event, got %#v", evt)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for caveman wait status")
	}

	close(release)
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("await outstanding caveman: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for outstanding caveman to finish")
	}
	if len(engine.cavemanJobs) != 0 {
		t.Fatalf("expected outstanding caveman to be cleared, got %#v", engine.cavemanJobs)
	}
}
