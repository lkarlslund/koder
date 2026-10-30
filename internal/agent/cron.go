package agent

import (
	"context"
	"fmt"
	"strings"

	chatpkg "github.com/lkarlslund/koder/internal/chat"
	"github.com/lkarlslund/koder/internal/cron"
	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/id"
	"github.com/lkarlslund/koder/internal/store"
)

// StartCron delivers scheduled cron jobs until Shutdown. Only the server
// calls it, so one-off commands that build an engine never run jobs.
func (e *Engine) StartCron() {
	if e == nil || e.cron == nil || e.stopCron != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	e.stopCron = cancel
	go e.cron.Run(ctx)
}

// CronJobs returns a chat's cron jobs, next run first.
func (e *Engine) CronJobs(ctx context.Context, chatID id.ID) ([]cron.Job, error) {
	if e == nil || e.cron == nil {
		return nil, fmt.Errorf("cron is unavailable")
	}
	return e.cron.List(ctx, chatID)
}

// CronJobCounts returns how many cron jobs each session and chat has.
func (e *Engine) CronJobCounts(ctx context.Context) (cron.Counts, error) {
	if e == nil || e.cron == nil {
		return cron.Counts{}, fmt.Errorf("cron is unavailable")
	}
	return e.cron.Counts(ctx)
}

// OnCronChange sets a function called after a cron job is added or removed.
func (e *Engine) OnCronChange(fn func()) {
	if e == nil || e.cron == nil {
		return
	}
	e.cron.OnChange(fn)
}

// DeleteCronJob deletes one of a chat's cron jobs.
func (e *Engine) DeleteCronJob(ctx context.Context, chatID id.ID, jobID string) error {
	if e == nil || e.cron == nil {
		return fmt.Errorf("cron is unavailable")
	}
	_, err := e.cron.Delete(ctx, chatID, jobID)
	return err
}

// deliverCronJob queues a due job's prompt in its chat, to run once any
// turn in progress has finished, or tells the chat a one-shot job was
// missed.
func (e *Engine) deliverCronJob(ctx context.Context, job cron.Job, delivery cron.Delivery) error {
	if e == nil || e.registry == nil {
		return fmt.Errorf("session registry is required")
	}
	owner, err := e.registry.Load(ctx, job.SessionID)
	if store.IsNotFound(err) {
		return cron.ErrChatGone
	}
	if err != nil {
		return err
	}
	if !chatIsOpen(owner.Snapshot().Chats, job) {
		return cron.ErrChatGone
	}
	rt, err := owner.Chat(ctx, job.ChatID)
	if err != nil {
		return err
	}
	rt.Enqueue(chatpkg.QueueItem{Kind: chatpkg.QueueKindQueued, Source: domain.UserMessageSourceCron, Text: cronJobText(job, delivery)})
	return nil
}

func chatIsOpen(chats []domain.Chat, job cron.Job) bool {
	for _, chatRecord := range chats {
		if chatRecord.ID == job.ChatID {
			return !chatRecord.Archived
		}
	}
	return false
}

func cronJobText(job cron.Job, delivery cron.Delivery) string {
	if delivery == cron.Missed {
		return fmt.Sprintf("One-shot cron job %s (%s) was due at %s while Koder was not running. It has been deleted without running. Do not carry it out unless the user asks. Its prompt was:\n\n%s",
			job.ID, job.Cron, job.NextRunAt.Local().Format("Mon 2006-01-02 15:04 MST"), job.Prompt)
	}
	kind := "One-shot"
	if job.Recurring {
		kind = "Recurring"
	}
	return fmt.Sprintf("%s cron job %s (%s), which this chat scheduled with the cron tool, is due. Carry out its prompt:\n\n%s", kind, job.ID, job.Cron, strings.TrimSpace(job.Prompt))
}
