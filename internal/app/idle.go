package app

import (
	"context"
	"fmt"

	"github.com/lkarlslund/koder/internal/tools/chattool"
)

// BusyReason says why Koder is working, or returns "" when no loaded chat is
// running, queued or waiting for approval and no exec session is running.
func (c *Controller) BusyReason(ctx context.Context) string {
	if c == nil || c.agent == nil {
		return ""
	}
	for _, owner := range c.agent.LoadedSessions() {
		if owner == nil {
			continue
		}
		for _, chatRecord := range owner.Snapshot().Chats {
			status, err := owner.ChatStatus(ctx, chatRecord.ID)
			if err != nil {
				continue
			}
			working := status.Busy || status.State == chattool.RunStateRunning || status.State == chattool.RunStateWaitingApproval
			if working || status.QueuedInputs > 0 || status.PendingApprovals > 0 {
				return fmt.Sprintf("chat %s is %s", chatRecord.ID, status.State)
			}
		}
	}
	c.mu.Lock()
	manager := c.execManagerLocked()
	c.mu.Unlock()
	if manager != nil {
		if running := manager.Running(); running > 0 {
			return fmt.Sprintf("%d exec session(s) running", running)
		}
	}
	return ""
}
