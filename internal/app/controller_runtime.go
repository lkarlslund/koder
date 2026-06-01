package app

import (
	"context"
	"slices"
	"strings"

	"github.com/lkarlslund/koder/internal/chat"
	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/execruntime"
	"github.com/lkarlslund/koder/internal/id"
	sessionpkg "github.com/lkarlslund/koder/internal/session"
)

type execRuntimeSubscription struct {
	chatID id.ID
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

func (c *Controller) forwardExecRuntime(chatID id.ID, events <-chan execruntime.Event) {
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
		c.broadcast("chat_delta", chat.Update{
			Snapshot:   snapshot,
			Status:     snapshot.Status,
			StatusText: snapshot.StatusText,
			Context:    snapshot.Context,
			Active:     snapshot.Active,
		})
	}
}

func (c *Controller) forwardSessionEvents(sessionID id.ID, events <-chan sessionpkg.Event) {
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
		case sessionpkg.EventChatAdded, sessionpkg.EventChatChanged, sessionpkg.EventChatArchived:
			c.applySessionChatEvent(event)
		case sessionpkg.EventPlanningChanged:
			c.applySessionPlanningEvent(event)
		case sessionpkg.EventTasksChanged:
			c.applySessionTasksEvent(event)
		case sessionpkg.EventSessionChanged:
			c.applySessionChangedEvent(event)
		}
	}
}

func (c *Controller) applySessionChatEvent(event sessionpkg.Event) {
	update := event.Update
	if update.Snapshot.Chat.ID == "" {
		update.Snapshot = event.Snapshot
	}
	if update.Snapshot.Chat.ID == "" {
		update.Snapshot.Chat = event.Chat
	}
	if update.Snapshot.Chat.ID == "" {
		return
	}
	if update.Status == "" {
		update.Status = update.Snapshot.Status
	}
	if update.StatusText == "" {
		update.StatusText = update.Snapshot.StatusText
	}
	update.Active = update.Active || update.Snapshot.Active
	chatID := update.Snapshot.Chat.ID
	if update.Event != nil && update.Event.Err != nil {
		c.mu.Lock()
		c.lastErr = update.Event.Err.Error()
		c.mu.Unlock()
	}
	c.mu.Lock()
	if c.session.ID != event.SessionID {
		c.mu.Unlock()
		return
	}
	if c.snapshots == nil {
		c.snapshots = map[id.ID]chat.Snapshot{}
	}
	if c.statuses == nil {
		c.statuses = map[id.ID]ChatSidebarStatus{}
	}
	if strings.TrimSpace(update.Snapshot.Chat.Title) == "" {
		if existing, ok := chatByID(c.chats, chatID); ok {
			update.Snapshot.Chat = existing
		}
	}
	c.snapshots[chatID] = update.Snapshot
	upsertChat(&c.chats, update.Snapshot.Chat)
	c.statuses[chatID] = sidebarStatusFromUpdate(update)
	if c.chat.ID == chatID {
		c.chat = update.Snapshot.Chat
	}
	if event.Kind == sessionpkg.EventChatArchived && c.chat.ID == chatID && event.NextChatID != "" {
		if next, ok := chatByID(c.chats, event.NextChatID); ok {
			c.chat = next
			if rt := c.runtimes[next.ID]; rt != nil {
				c.runtime = rt
			}
		}
	}
	c.mu.Unlock()
	c.broadcast("chat_delta", update)
	if event.Kind == sessionpkg.EventChatArchived {
		c.mu.RLock()
		activeChatID := c.chat.ID
		c.mu.RUnlock()
		c.broadcast("selection_delta", map[string]id.ID{"active_chat_id": activeChatID})
	}
}

func (c *Controller) applySessionPlanningEvent(event sessionpkg.Event) {
	c.mu.Lock()
	if c.session.ID == event.SessionID {
		c.milestone = event.Plan
		c.todos = slices.Clone(event.Todos)
		c.todosByRef = cloneTodosByRef(event.TodosByRef)
	}
	c.mu.Unlock()
	c.broadcast("planning_delta", map[string]any{
		"milestones":         event.Plan,
		"todos":              slices.Clone(event.Todos),
		"todos_by_milestone": cloneTodosByRef(event.TodosByRef),
	})
}

func (c *Controller) applySessionTasksEvent(event sessionpkg.Event) {
	c.broadcast("tasks_delta", map[string]any{"tasks": slices.Clone(event.Tasks)})
}

func (c *Controller) applySessionChangedEvent(event sessionpkg.Event) {
	c.mu.Lock()
	if c.session.ID == event.SessionID {
		c.session = event.Session
		for idx := range c.sessions {
			if c.sessions[idx].ID == event.SessionID {
				c.sessions[idx] = event.Session
			}
		}
	}
	c.mu.Unlock()
	c.broadcast("session_delta", map[string]any{"session": event.Session})
}
