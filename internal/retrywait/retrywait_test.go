package retrywait

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestGateCutsAWaitShortOrGivesUp(t *testing.T) {
	var gate Gate
	ctx := WithGate(context.Background(), &gate)
	if FromContext(ctx) != &gate || gate.RetryNow() || gate.GiveUp() {
		t.Fatal("a gate with no wait has nothing to end")
	}
	for _, tc := range []struct {
		name string
		end  func() bool
		want error
	}{
		{"retry now", gate.RetryNow, nil},
		{"give up", gate.GiveUp, ErrGaveUp},
	} {
		done := make(chan error, 1)
		start := time.Now()
		go func() { done <- FromContext(ctx).Wait(ctx, time.Minute, nil) }()
		for !gate.Waiting() {
			time.Sleep(time.Millisecond)
		}
		if !tc.end() {
			t.Fatalf("%s: no wait to end", tc.name)
		}
		if err := <-done; !errors.Is(err, tc.want) || time.Since(start) > time.Second {
			t.Fatalf("%s: wait returned %v after %s", tc.name, err, time.Since(start))
		}
		if gate.Waiting() {
			t.Fatalf("%s: still waiting", tc.name)
		}
	}
}

func TestNilGateJustWaits(t *testing.T) {
	var ticks []time.Duration
	if err := (*Gate)(nil).Wait(context.Background(), 10*time.Millisecond, func(left time.Duration) { ticks = append(ticks, left) }); err != nil {
		t.Fatal(err)
	}
	if len(ticks) == 0 || ticks[0] != time.Second {
		t.Fatalf("ticks = %v", ticks)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := (*Gate)(nil).Wait(ctx, time.Minute, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled wait = %v", err)
	}
}
