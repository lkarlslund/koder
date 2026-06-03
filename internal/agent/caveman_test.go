package agent

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
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
