package main

import (
	"context"
	"log/slog"
	"time"
)

// exitWhenIdlePoll is how often Koder checks for idle after SIGUSR2 asks it
// to exit once no chat or exec session is working.
const exitWhenIdlePoll = 2 * time.Second

// waitForIdle closes idle once busyReason reports nothing, logging each new
// reason it waits on.
func waitForIdle(ctx context.Context, busyReason func(context.Context) string, poll time.Duration, idle chan<- struct{}) {
	ticker := time.NewTicker(poll)
	defer ticker.Stop()
	last := ""
	for {
		reason := busyReason(ctx)
		if reason == "" {
			close(idle)
			return
		}
		if reason != last {
			slog.Info("koder waiting for idle before exiting", "reason", reason)
			last = reason
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
