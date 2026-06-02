package app

import (
	"context"

	"github.com/lkarlslund/koder/internal/chat"
	chatpkg "github.com/lkarlslund/koder/internal/chat"
	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/id"
	sessionpkg "github.com/lkarlslund/koder/internal/session"
)

const processRestartToolFailure = "Tool execution failed because koder restarted before the tool completed."
const processStartupRunningToolFailure = "Tool execution failed because koder restarted while the tool was running."

func (c *Controller) restartInterruptedSession(ctx context.Context) (domain.Session, bool, error) {
	sessions, err := c.workspaceSessions(ctx)
	if err != nil {
		return domain.Session{}, false, err
	}
	var matches []domain.Session
	for _, session := range sessions {
		chats, err := sessionpkg.ListChats(ctx, c.store, session.ID)
		if err != nil {
			return domain.Session{}, false, err
		}
		for _, chatRecord := range chats {
			if chatRecord.AutoRestart {
				matches = append(matches, session)
				break
			}
		}
	}
	session := newestSession(matches)
	return session, session.ID != "", nil
}

func (c *Controller) autoResumeRestartInterruptedChats(runtimes map[id.ID]*chat.Chat, snapshots map[id.ID]chat.Snapshot) {
	for id, snapshot := range snapshots {
		if !snapshot.Chat.AutoRestart {
			continue
		}
		rt := runtimes[id]
		if rt == nil {
			continue
		}
		_ = rt.ClearAutoRestart(context.Background())
		if !shouldAutoResumeRestartInterrupted(snapshot) {
			continue
		}
		if !hasContinueQueued(snapshot) {
			rt.Enqueue(chat.QueueItem{Kind: chat.QueueKindContinue, Source: domain.UserMessageSourceAutoResume})
		}
	}
}

func (c *Controller) failStartupRunningToolCallsOnce(ctx context.Context, chats []domain.Chat) error {
	c.mu.Lock()
	if c.clearedStartupRunningTools {
		c.mu.Unlock()
		return nil
	}
	c.clearedStartupRunningTools = true
	c.mu.Unlock()
	for _, chatRecord := range chats {
		if _, err := chatpkg.FailRunningToolCalls(ctx, c.store, chatRecord.ID, processStartupRunningToolFailure); err != nil {
			return err
		}
	}
	return nil
}

func (c *Controller) failProcessInterruptedToolCalls(ctx context.Context, chats []domain.Chat) error {
	for _, chatRecord := range chats {
		if chatRecord.AutoRestart {
			if _, err := chatpkg.FailInterruptedToolCalls(ctx, c.store, chatRecord.ID, processRestartToolFailure); err != nil {
				return err
			}
		}
	}
	return nil
}

func shouldAutoResumeRestartInterrupted(snapshot chat.Snapshot) bool {
	if snapshot.Active || snapshot.Status == chat.StatusWaitingApproval {
		return false
	}
	return snapshot.Chat.AutoRestart
}

func hasContinueQueued(snapshot chat.Snapshot) bool {
	for _, item := range allSnapshotQueuedInputs(snapshot) {
		if item.Kind == domain.QueuedInputKindContinue {
			return true
		}
	}
	return false
}

func allSnapshotQueuedInputs(snapshot chat.Snapshot) []domain.QueuedInput {
	seen := map[id.ID]struct{}{}
	out := make([]domain.QueuedInput, 0, len(snapshot.Chat.QueuedInputs)+len(snapshot.QueuedInputs))
	for _, item := range snapshot.Chat.QueuedInputs {
		if item.ID != "" {
			seen[item.ID] = struct{}{}
		}
		out = append(out, item)
	}
	for _, item := range snapshot.QueuedInputs {
		if item.ID != "" {
			if _, ok := seen[item.ID]; ok {
				continue
			}
		}
		out = append(out, item)
	}
	return out
}
