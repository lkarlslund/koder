// Package retrywait lets a person steer a pause before a model request is
// retried: retry now instead of waiting out the backoff, or give up so the
// turn ends with the error that caused the retry.
package retrywait

import (
	"context"
	"errors"
	"sync"
	"time"
)

// ErrGaveUp is returned by Wait when the person chose not to retry.
var ErrGaveUp = errors.New("retry canceled")

// Gate is a chat's handle on the retry wait in progress, if any. The zero
// value is ready to use and a nil Gate just waits.
type Gate struct {
	mu      sync.Mutex
	waiting *wait
}

type wait struct {
	now    chan struct{}
	giveUp chan struct{}
}

type gateKey struct{}

// WithGate returns a context whose retry waits gate controls.
func WithGate(ctx context.Context, gate *Gate) context.Context {
	return context.WithValue(ctx, gateKey{}, gate)
}

// FromContext returns the gate attached to ctx, or nil.
func FromContext(ctx context.Context) *Gate {
	gate, _ := ctx.Value(gateKey{}).(*Gate)
	return gate
}

// Wait pauses for delay, calling onTick with the remaining time, rounded to
// the second, when it starts and then about once a second. It returns early
// with nil when RetryNow is called, ErrGaveUp when GiveUp is called, and the
// context's error when ctx ends.
func (g *Gate) Wait(ctx context.Context, delay time.Duration, onTick func(time.Duration)) error {
	w := &wait{now: make(chan struct{}, 1), giveUp: make(chan struct{}, 1)}
	if g != nil {
		g.mu.Lock()
		g.waiting = w
		g.mu.Unlock()
		defer func() {
			g.mu.Lock()
			if g.waiting == w {
				g.waiting = nil
			}
			g.mu.Unlock()
		}()
	}
	deadline := time.Now().Add(delay)
	tick := func() {
		if onTick != nil {
			onTick(Round(time.Until(deadline)))
		}
	}
	tick()
	timer := time.NewTimer(delay)
	defer timer.Stop()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-w.giveUp:
			return ErrGaveUp
		case <-w.now:
			return nil
		case <-timer.C:
			return nil
		case <-ticker.C:
			tick()
		}
	}
}

// Waiting reports whether a retry wait is in progress.
func (g *Gate) Waiting() bool {
	if g == nil {
		return false
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.waiting != nil
}

// RetryNow ends the wait in progress so the request is retried at once. It
// reports whether there was a wait to end.
func (g *Gate) RetryNow() bool { return g.signal(func(w *wait) chan struct{} { return w.now }) }

// GiveUp ends the wait in progress without retrying. It reports whether
// there was a wait to end.
func (g *Gate) GiveUp() bool { return g.signal(func(w *wait) chan struct{} { return w.giveUp }) }

func (g *Gate) signal(pick func(*wait) chan struct{}) bool {
	if g == nil {
		return false
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.waiting == nil {
		return false
	}
	select {
	case pick(g.waiting) <- struct{}{}:
	default:
	}
	return true
}

// Round rounds a remaining wait to whole seconds, never down to zero while
// any of it is left.
func Round(delay time.Duration) time.Duration {
	if delay <= 0 {
		return 0
	}
	if rounded := delay.Round(time.Second); rounded > 0 {
		return rounded
	}
	return time.Second
}
