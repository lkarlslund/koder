package app

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/lkarlslund/koder/internal/chat"
	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/execruntime"
	"github.com/lkarlslund/koder/internal/id"
	sessionpkg "github.com/lkarlslund/koder/internal/session"
	"github.com/lkarlslund/koder/internal/tools"
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

func execProcessesFromSnapshots(snapshots []execruntime.Snapshot) []tools.ExecProcess {
	out := make([]tools.ExecProcess, 0, len(snapshots))
	for _, snapshot := range snapshots {
		out = append(out, tools.ExecProcess{
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

// TerminateExecProcessForSelection stops a running exec process owned by the selected chat.
func (c *Controller) TerminateExecProcessForSelection(ctx context.Context, selection Selection, processID string) (tools.ExecProcess, error) {
	if c == nil {
		return tools.ExecProcess{}, fmt.Errorf("controller is nil")
	}
	processID = strings.TrimSpace(processID)
	if processID == "" {
		return tools.ExecProcess{}, fmt.Errorf("process id is required")
	}
	if selection.SessionID == "" {
		return tools.ExecProcess{}, fmt.Errorf("session id is required")
	}
	if selection.ChatID == "" {
		return tools.ExecProcess{}, fmt.Errorf("chat id is required")
	}
	c.mu.Lock()
	manager := c.execManagerLocked()
	c.mu.Unlock()
	if manager == nil {
		return tools.ExecProcess{}, fmt.Errorf("exec manager is unavailable")
	}
	snap, err := manager.Terminate(ctx, execruntime.TerminateRequest{
		SessionID: selection.SessionID,
		ChatID:    selection.ChatID,
		ProcessID: processID,
		MaxBytes:  16 * 1024,
	})
	if err != nil {
		return tools.ExecProcess{}, err
	}
	processes := execProcessesFromSnapshots([]execruntime.Snapshot{snap})
	process := tools.ExecProcess{}
	if len(processes) > 0 {
		process = processes[0]
	}
	c.mu.Lock()
	snapshot, ok := c.snapshots[selection.ChatID]
	if !ok || snapshot.Chat.ID == "" {
		if rt := c.runtimes[selection.ChatID]; rt != nil {
			snapshot = rt.Snapshot()
			ok = true
		}
	}
	if ok && snapshot.Chat.ID != "" {
		snapshot = c.snapshotWithExecProcessesLocked(snapshot)
		c.snapshots[selection.ChatID] = snapshot
	}
	c.mu.Unlock()
	if ok && snapshot.Chat.ID != "" {
		c.broadcast("chat_delta", chat.Update{
			Snapshot:   snapshot,
			Status:     snapshot.Status,
			StatusText: snapshot.StatusText,
			Context:    snapshot.Context,
			Active:     snapshot.Active,
		})
	}
	return process, nil
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
