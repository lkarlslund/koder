package agent

import (
	"context"
	"log/slog"

	"github.com/lkarlslund/koder/internal/browserapi"
	chatpkg "github.com/lkarlslund/koder/internal/chat"
	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/execruntime"
)

// deliverExecNotification queues an exec session's report in its chat as a
// steer: it starts a turn when the chat is idle, and joins the running turn
// at its next step otherwise.
func (e *Engine) deliverExecNotification(n execruntime.Notification) {
	if e == nil || e.registry == nil {
		return
	}
	rt, err := e.registry.Chat(context.Background(), n.SessionID, n.ChatID)
	if err != nil {
		slog.Warn("deliver exec notification", "process_id", n.ProcessID, "chat_id", n.ChatID, "error", err)
		return
	}
	source := domain.UserMessageSourceExec
	if n.Kind == execruntime.NotificationLines {
		source = domain.UserMessageSourceMonitor
	}
	rt.Enqueue(chatpkg.QueueItem{Kind: chatpkg.QueueKindSteer, Source: source, Text: n.Text})
}

func (e *Engine) deliverBrowserIdleNotification(chat browserapi.Chat, text string) {
	if e == nil || e.registry == nil {
		return
	}
	rt, err := e.registry.Chat(context.Background(), chat.SessionID, chat.ChatID)
	if err != nil {
		slog.Warn("deliver browser idle notification", "chat_id", chat.ChatID, "error", err)
		return
	}
	rt.Enqueue(chatpkg.QueueItem{Kind: chatpkg.QueueKindSteer, Source: domain.UserMessageSourceMonitor, Text: text})
}
