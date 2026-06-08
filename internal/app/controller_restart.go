package app

import (
	"context"
	"fmt"

	"github.com/lkarlslund/koder/internal/chat"
	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/id"
)

const processRestartToolFailure = "Tool execution failed because koder restarted before the tool completed."
const processStartupRunningToolFailure = "Tool execution failed because koder restarted while the tool was running."

func (c *Controller) restartInterruptedSession(ctx context.Context) (domain.Session, bool, error) {
	if c.agent == nil {
		return domain.Session{}, false, fmt.Errorf("no chat agent")
	}
	sessions, err := c.workspaceSessions(ctx)
	if err != nil {
		return domain.Session{}, false, err
	}
	var matches []domain.Session
	for _, session := range sessions {
		owner, err := c.agent.LoadSession(ctx, session.ID)
		if err != nil {
			return domain.Session{}, false, err
		}
		chats := owner.Snapshot().Chats
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
	for sessionID, chatIDs := range groupChatIDsBySession(chats, false) {
		owner, err := c.agent.LoadSession(ctx, sessionID)
		if err != nil {
			return err
		}
		if _, err := owner.FailRunningToolCalls(ctx, chatIDs, processStartupRunningToolFailure); err != nil {
			return err
		}
	}
	return nil
}

func (c *Controller) failProcessInterruptedToolCalls(ctx context.Context, chats []domain.Chat) error {
	for sessionID, chatIDs := range groupChatIDsBySession(chats, true) {
		owner, err := c.agent.LoadSession(ctx, sessionID)
		if err != nil {
			return err
		}
		if _, err := owner.FailInterruptedToolCalls(ctx, chatIDs, processRestartToolFailure); err != nil {
			return err
		}
	}
	return nil
}

func groupChatIDsBySession(chats []domain.Chat, autoRestartOnly bool) map[id.ID][]id.ID {
	grouped := map[id.ID][]id.ID{}
	for _, chatRecord := range chats {
		if chatRecord.SessionID == "" || chatRecord.ID == "" {
			continue
		}
		if autoRestartOnly && !chatRecord.AutoRestart {
			continue
		}
		grouped[chatRecord.SessionID] = append(grouped[chatRecord.SessionID], chatRecord.ID)
	}
	return grouped
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
