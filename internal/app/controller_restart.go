package app

import (
	"context"
	"strings"

	"github.com/lkarlslund/koder/internal/chat"
	chatpkg "github.com/lkarlslund/koder/internal/chat"
	"github.com/lkarlslund/koder/internal/domain"
	sessionpkg "github.com/lkarlslund/koder/internal/session"
)

const processRestartResumeNote = "The previous turn was interrupted because the koder process was restarting. Continue from the persisted transcript and pending tool state without restating the interruption."
const processRestartToolFailure = "Tool execution failed because koder restarted before the tool completed."
const processStartupRunningToolFailure = "Tool execution failed because koder restarted while the tool was running."
const processRestartToolFailureInstruction = "A tool call was interrupted by the process restart and has been marked failed. Continue from the persisted transcript and pending tool state without rerunning failed tools unless the user explicitly asks."

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
			if ok, err := c.chatEndsWithRestartInterrupt(ctx, chatRecord.ID); err != nil {
				return domain.Session{}, false, err
			} else if ok {
				matches = append(matches, session)
				break
			}
		}
	}
	session := newestSession(matches)
	return session, session.ID != "", nil
}

func (c *Controller) chatEndsWithRestartInterrupt(ctx context.Context, chatID domain.ID) (bool, error) {
	timeline, err := chatpkg.TimelineForChat(ctx, c.store, chatID)
	if err != nil {
		return false, err
	}
	if len(timeline) == 0 {
		return false, nil
	}
	notice, ok := timeline[len(timeline)-1].Content.(domain.Notice)
	return ok && notice.Kind == domain.NoticeKindInterrupted && notice.Reason == domain.NoticeReasonProcessRestart, nil
}

func (c *Controller) autoResumeRestartInterruptedChats(runtimes map[domain.ID]*chat.Chat, snapshots map[domain.ID]chat.Snapshot) {
	for id, snapshot := range snapshots {
		if !shouldAutoResumeRestartInterrupted(snapshot) {
			continue
		}
		rt := runtimes[id]
		if rt == nil {
			continue
		}
		note := processRestartResumeNote
		if hasErroredRestartTool(snapshot) {
			note = processRestartToolFailureInstruction
		}
		rt.Enqueue(chat.QueueItem{Kind: chat.QueueKindSteer, Source: domain.UserMessageSourceAutoResume, Text: note})
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
		if ok, err := c.chatEndsWithProcessInterrupt(ctx, chatRecord.ID); err != nil {
			return err
		} else if ok {
			if _, err := chatpkg.FailInterruptedToolCalls(ctx, c.store, chatRecord.ID, processRestartToolFailure); err != nil {
				return err
			}
		}
	}
	return nil
}

func (c *Controller) chatEndsWithProcessInterrupt(ctx context.Context, chatID domain.ID) (bool, error) {
	timeline, err := chatpkg.TimelineForChat(ctx, c.store, chatID)
	if err != nil {
		return false, err
	}
	if len(timeline) == 0 {
		return false, nil
	}
	notice, ok := timeline[len(timeline)-1].Content.(domain.Notice)
	if !ok || notice.Kind != domain.NoticeKindInterrupted {
		return false, nil
	}
	return notice.Reason == domain.NoticeReasonProcessRestart || notice.Reason == domain.NoticeReasonProcessTerminating, nil
}

func shouldAutoResumeRestartInterrupted(snapshot chat.Snapshot) bool {
	if snapshot.Active || snapshot.Status == chat.StatusWaitingApproval {
		return false
	}
	if hasUserQueuedInput(snapshot) {
		return false
	}
	for _, item := range allSnapshotQueuedInputs(snapshot) {
		if item.Kind == domain.QueuedInputKindContinue || isAutoResumeRestartMessage(item.Text) {
			return false
		}
	}
	if len(snapshot.Timeline) == 0 {
		return false
	}
	notice, ok := snapshot.Timeline[len(snapshot.Timeline)-1].Content.(domain.Notice)
	return ok && notice.Kind == domain.NoticeKindInterrupted && notice.Reason == domain.NoticeReasonProcessRestart
}

func hasUserQueuedInput(snapshot chat.Snapshot) bool {
	for _, item := range allSnapshotQueuedInputs(snapshot) {
		if strings.TrimSpace(item.Source) == domain.UserMessageSourceUser {
			return true
		}
	}
	return false
}

func allSnapshotQueuedInputs(snapshot chat.Snapshot) []domain.QueuedInput {
	seen := map[domain.ID]struct{}{}
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

func isAutoResumeRestartMessage(text string) bool {
	text = strings.TrimSpace(text)
	return text == processRestartResumeNote || text == processRestartToolFailureInstruction
}

func hasErroredRestartTool(snapshot chat.Snapshot) bool {
	for _, item := range snapshot.Timeline {
		assistant, ok := item.Content.(domain.AssistantMessage)
		if !ok {
			continue
		}
		for _, tool := range assistant.Tools {
			if tool.Status == domain.ToolStatusErrored && tool.Error != nil && tool.Error.Code == domain.NoticeReasonProcessRestart {
				return true
			}
		}
	}
	return false
}
