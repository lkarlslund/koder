package main

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

func TestWaitForIdleClosesOnceNothingIsBusy(t *testing.T) {
	var checks atomic.Int32
	busy := func(context.Context) string {
		if checks.Add(1) < 3 {
			return "chat c1 is running"
		}
		return ""
	}
	idle := make(chan struct{})
	go waitForIdle(context.Background(), busy, time.Millisecond, idle)
	select {
	case <-idle:
	case <-time.After(5 * time.Second):
		t.Fatal("idle was not signalled")
	}
	if got := checks.Load(); got != 3 {
		t.Fatalf("checks = %d, want 3", got)
	}
}
