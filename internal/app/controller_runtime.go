package app

import (
	"context"
	"strings"

	"github.com/lkarlslund/koder/internal/chat"
	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/execruntime"
	sessionpkg "github.com/lkarlslund/koder/internal/session"
	"github.com/lkarlslund/koder/internal/tools"
)

type execRuntimeSubscription struct {
	chatID domain.ID
	events <-chan execruntime.Event
}

func (c *Controller) ensureExecSubscriptionsLocked(chats []domain.Chat) []execRuntimeSubscription {
	manager := c.execManagerLocked()
	if manager == nil {
		return nil
	}
	subscriptions := make([]execRuntimeSubscription, 0, len(chats))
	for _, item := range chats {
		if item.ID == "" || c.execUnsubs[item.ID] != nil {
			continue
		}
		events, unsub := manager.Subscribe(item.ID)
		c.execUnsubs[item.ID] = unsub
		subscriptions = append(subscriptions, execRuntimeSubscription{chatID: item.ID, events: events})
	}
	return subscriptions
}

func (c *Controller) execManagerLocked() *execruntime.Manager {
	if c == nil || c.agent == nil {
		return nil
	}
	return c.agent.ExecManager()
}

func (c *Controller) snapshotWithExecProcessesLocked(snapshot chat.Snapshot) chat.Snapshot {
	manager := c.execManagerLocked()
	if manager == nil || snapshot.Session.ID == "" || snapshot.Chat.ID == "" {
		return snapshot
	}
	processes, err := manager.List(context.Background(), execruntime.ListRequest{
		SessionID: snapshot.Session.ID,
		ChatID:    snapshot.Chat.ID,
		Scope:     execruntime.ScopeChat,
		MaxBytes:  16 * 1024,
	})
	if err != nil {
		return snapshot
	}
	snapshot.ExecProcesses = execProcessesFromSnapshots(processes)
	return snapshot
}

func execProcessesFromSnapshots(snapshots []execruntime.Snapshot) []domain.ExecProcess {
	out := make([]domain.ExecProcess, 0, len(snapshots))
	for _, snapshot := range snapshots {
		out = append(out, domain.ExecProcess{
			ProcessID:   snapshot.ProcessID,
			Command:     snapshot.Command,
			Workdir:     snapshot.Workdir,
			Shell:       snapshot.Shell,
			TTY:         snapshot.TTY,
			State:       string(snapshot.State),
			ExitCode:    snapshot.ExitCode,
			TimeoutMS:   snapshot.TimeoutMS,
			Output:      snapshot.Output,
			OutputBytes: snapshot.OutputBytes,
			StdinClosed: snapshot.StdinClosed,
			Lost:        snapshot.Lost,
		})
	}
	return out
}

func (c *Controller) forwardExecRuntime(chatID domain.ID, events <-chan execruntime.Event) {
	for range events {
		c.mu.Lock()
		snapshot, ok := c.snapshots[chatID]
		if !ok || snapshot.Chat.ID == "" {
			if rt := c.runtimes[chatID]; rt != nil {
				snapshot = rt.Snapshot()
				ok = true
			}
		}
		if !ok || snapshot.Chat.ID == "" {
			c.mu.Unlock()
			continue
		}
		snapshot = c.snapshotWithExecProcessesLocked(snapshot)
		c.snapshots[chatID] = snapshot
		c.mu.Unlock()
		c.broadcast("chat_update", chat.Update{
			Snapshot:   snapshot,
			Status:     snapshot.Status,
			StatusText: snapshot.StatusText,
			Context:    snapshot.Context,
			Active:     snapshot.Active,
		})
	}
}

func (c *Controller) forwardRuntime(chatID domain.ID, updates <-chan chat.Update) {
	for update := range updates {
		c.mu.RLock()
		sessionID := c.session.ID
		activeChatID := c.chat.ID
		_, subscribed := c.runtimes[chatID]
		_, hasSnapshot := c.snapshots[chatID]
		c.mu.RUnlock()
		if !subscribed && chatID != activeChatID && !hasSnapshot {
			return
		}
		if update.Event != nil && update.Event.Err != nil {
			c.mu.Lock()
			c.lastErr = update.Event.Err.Error()
			c.mu.Unlock()
		}
		if update.Snapshot.Chat.ID == "" {
			update.Snapshot.Chat.ID = chatID
		}
		if update.Snapshot.Chat.ID == chatID {
			c.mu.Lock()
			stalePassive := false
			if existing, ok := c.snapshots[chatID]; ok && runtimeUpdateIsPassive(update) && !update.Snapshot.Chat.UpdatedAt.After(existing.Chat.UpdatedAt) {
				stalePassive = true
			}
			if stalePassive {
				c.mu.Unlock()
			} else {
				if strings.TrimSpace(update.Snapshot.Chat.Title) == "" {
					if existing, ok := chatByID(c.chats, chatID); ok {
						update.Snapshot.Chat = existing
					} else if activeChatID == chatID {
						update.Snapshot.Chat = c.chat
					}
				}
				if activeChatID == chatID {
					c.chat = update.Snapshot.Chat
				}
				if c.statuses == nil {
					c.statuses = map[domain.ID]ChatSidebarStatus{}
				}
				if c.snapshots == nil {
					c.snapshots = map[domain.ID]chat.Snapshot{}
				}
				c.snapshots[chatID] = update.Snapshot
				c.statuses[chatID] = sidebarStatusFromUpdate(update)
				found := false
				for idx := range c.chats {
					if c.chats[idx].ID == update.Snapshot.Chat.ID {
						c.chats[idx] = update.Snapshot.Chat
						found = true
						break
					}
				}
				if !found {
					c.chats = append(c.chats, update.Snapshot.Chat)
				}
				c.mu.Unlock()
			}
		} else {
			c.mu.Lock()
			if c.statuses == nil {
				c.statuses = map[domain.ID]ChatSidebarStatus{}
			}
			c.statuses[chatID] = sidebarStatusFromUpdate(update)
			c.mu.Unlock()
		}
		c.refreshPlanningState(context.Background(), sessionID)
		c.broadcast("chat_update", update)
		if runtimeUpdateNeedsStateSnapshot(update) {
			c.broadcast("snapshot", c.State())
		}
	}
}

func (c *Controller) forwardSessionEvents(sessionID domain.ID, events <-chan sessionpkg.Event) {
	for event := range events {
		if event.SessionID != sessionID {
			continue
		}
		c.mu.RLock()
		currentSessionID := c.session.ID
		c.mu.RUnlock()
		if currentSessionID != sessionID {
			return
		}
		switch event.Kind {
		case sessionpkg.EventChatAdded:
			status, err := c.agent.PollChat(context.Background(), event.SessionID, event.Chat.ID)
			if err != nil {
				status = tools.ChatStatus{
					Chat:       event.Chat,
					State:      tools.ChatRunStateIdle,
					Status:     string(tools.ChatRunStateIdle),
					StatusText: string(chat.StatusIdle),
				}
			}
			if err := c.addStartedChat(context.Background(), status); err != nil {
				c.mu.Lock()
				c.lastErr = err.Error()
				c.mu.Unlock()
				c.broadcast("snapshot", c.State())
			}
		}
	}
}

func runtimeUpdateIsPassive(update chat.Update) bool {
	return update.Event == nil && !update.Active && !update.QueueChanged && !update.ApprovalsChanged
}

func runtimeUpdateNeedsStateSnapshot(update chat.Update) bool {
	if update.QueueChanged || update.ApprovalsChanged {
		return true
	}
	if update.Event == nil {
		return false
	}
	switch update.Event.Kind {
	case domain.EventKindToolResult, domain.EventKindApprovalAsk, domain.EventKindApprovalReply, domain.EventKindChatTitle, domain.EventKindSessionTitle, domain.EventKindError, domain.EventKindMessageDone:
		return true
	default:
		return false
	}
}
