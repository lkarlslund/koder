package agent

import (
	"context"
	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/id"
)

// ExplainRestartedExecs appends a durable, one-time explanation before startup
// resumes interrupted turns. Idle owners also see it on their next turn.
func (e *Engine) ExplainRestartedExecs(ctx context.Context) error {
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
		timeline, err := rt.Timeline(ctx)
		if err != nil {
			return err
		}
		exists := false
		for _, item := range timeline {
			if user, ok := item.Content.(domain.UserMessage); ok && user.Source == domain.UserMessageSourceExec && user.Text == text {
				exists = true
				break
			}
		}
		if !exists {
			if _, err := rt.AppendUserMessage(ctx, domain.UserMessage{Source: domain.UserMessageSourceExec, Text: text}); err != nil {
				return err
			}
		}
	}
	return manager.ClearPreviousJobs()
}
