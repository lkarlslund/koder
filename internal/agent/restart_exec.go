package agent

import (
	"context"

	chatpkg "github.com/lkarlslund/koder/internal/chat"
	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/id"
)

// ExplainRestartedExecs tells each chat which of its running processes the
// previous Koder process stopped, once. A chat in resuming is about to
// continue its interrupted turn, so the notice is written into its history
// for that turn to read. Any other chat is idle, so the notice is queued the
// way an exec session's end is: it starts a turn and the model can react.
func (e *Engine) ExplainRestartedExecs(ctx context.Context, resuming map[id.ID]bool) error {
	if e == nil || e.toolsRuntime == nil || e.toolsRuntime.ExecManager() == nil {
		return nil
	}
	manager := e.toolsRuntime.ExecManager()
	journal, err := manager.PreviousJobs()
	if err != nil {
		return err
	}
	seen := map[id.ID]bool{}
	for _, job := range journal.Jobs {
		if seen[job.ChatID] {
			continue
		}
		seen[job.ChatID] = true
		rt, err := e.registry.Chat(ctx, job.SessionID, job.ChatID)
		if err != nil {
			return err
		}
		text := journal.ChatNotice(job.ChatID)
		told, err := chatHasNotice(ctx, rt, text)
		if err != nil {
			return err
		}
		if told {
			continue
		}
		if resuming[job.ChatID] {
			if _, err := rt.AppendUserMessage(ctx, domain.UserMessage{Source: domain.UserMessageSourceExec, Text: text}); err != nil {
				return err
			}
			continue
		}
		rt.Enqueue(chatpkg.QueueItem{Kind: chatpkg.QueueKindSteer, Source: domain.UserMessageSourceExec, Text: text})
		rt.Kick()
	}
	return manager.ClearPreviousJobs()
}

// chatHasNotice reports whether a chat already has text from exec, in its
// history or waiting in its queue, so a restart is explained only once.
func chatHasNotice(ctx context.Context, rt *chatpkg.Chat, text string) (bool, error) {
	timeline, err := rt.Timeline(ctx)
	if err != nil {
		return false, err
	}
	for _, item := range timeline {
		if user, ok := item.Content.(domain.UserMessage); ok && user.Source == domain.UserMessageSourceExec && user.Text == text {
			return true, nil
		}
	}
	snapshot := rt.Snapshot()
	for _, queued := range append(snapshot.Chat.QueuedInputs, snapshot.QueuedInputs...) {
		if queued.Source == domain.UserMessageSourceExec && queued.Text == text {
			return true, nil
		}
	}
	return false, nil
}
