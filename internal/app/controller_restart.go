package app

import (
	"context"

	"github.com/lkarlslund/koder/internal/chat"
	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/id"
)

const processRestartToolFailure = "Tool execution failed because koder restarted before the tool completed."

// resumeRestartInterruptedChats resumes chats that a process restart
// interrupted. Sessions stay lazily activated: only sessions owning an
// interrupted chat are loaded. Tool calls that were in flight are failed so
// the resumed turn starts from a settled transcript.
func (c *Controller) resumeRestartInterruptedChats(ctx context.Context) error {
	chats, err := c.agent.AutoRestartChats(ctx)
	if err != nil {
		return err
	}
	for sessionID, chatIDs := range groupChatIDsBySession(chats) {
		owner, err := c.agent.LoadSession(ctx, sessionID)
		if err != nil {
			return err
		}
		if _, err := owner.FailInterruptedToolCalls(ctx, chatIDs, processRestartToolFailure); err != nil {
			return err
		}
		for _, chatID := range chatIDs {
			rt, err := owner.Chat(ctx, chatID)
			if err != nil {
				return err
			}
			resumeRestartInterruptedChat(ctx, rt)
		}
	}
	return nil
}

func resumeRestartInterruptedChat(ctx context.Context, rt *chat.Chat) {
	snapshot := rt.Snapshot()
	_ = rt.ClearAutoRestart(ctx)
	if !snapshot.Active && snapshot.Status != chat.StatusWaitingApproval && !hasContinueQueued(snapshot) {
		rt.Enqueue(chat.QueueItem{Kind: chat.QueueKindContinue, Source: domain.UserMessageSourceAutoResume})
	}
	rt.Kick()
}

func groupChatIDsBySession(chats []domain.Chat) map[id.ID][]id.ID {
	grouped := map[id.ID][]id.ID{}
	for _, chatRecord := range chats {
		if chatRecord.SessionID == "" || chatRecord.ID == "" {
			continue
		}
		grouped[chatRecord.SessionID] = append(grouped[chatRecord.SessionID], chatRecord.ID)
	}
	return grouped
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
