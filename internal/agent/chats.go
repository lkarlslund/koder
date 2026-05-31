package agent

import (
	"context"
	"fmt"
	"strings"

	chatpkg "github.com/lkarlslund/koder/internal/chat"
	"github.com/lkarlslund/koder/internal/chatrole"
	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/store"
	"github.com/lkarlslund/koder/internal/tools"
)

type chatRunState struct {
	state      tools.ChatRunState
	status     string
	statusText string
	lastError  string
}

func (e *Engine) Chat(ctx context.Context, session domain.Session, chatRecord domain.Chat) (*chatpkg.Chat, error) {
	if chatRecord.ID == "" {
		return nil, fmt.Errorf("chat id is required")
	}
	e.chatMu.RLock()
	if existing := e.chats[chatRecord.ID]; existing != nil {
		e.chatMu.RUnlock()
		return existing, nil
	}
	e.chatMu.RUnlock()

	loaded, err := chatpkg.Load(ctx, session, chatRecord, e.ChatDeps(), e.detachChat)
	if err != nil {
		return nil, err
	}

	e.chatMu.Lock()
	if existing := e.chats[chatRecord.ID]; existing != nil {
		e.chatMu.Unlock()
		loaded.Close()
		return existing, nil
	}
	e.chats[chatRecord.ID] = loaded
	e.chatMu.Unlock()
	return loaded, nil
}

func (e *Engine) ChatDeps() chatpkg.Deps {
	return chatpkg.Deps{
		Store:   e.store,
		Prompt:  e,
		Turns:   e,
		Tools:   e,
		Pending: e,
		Compact: e,
		Errors:  e,
	}
}

func (e *Engine) detachChat(chatID domain.ID) {
	e.chatMu.Lock()
	delete(e.chats, chatID)
	e.chatMu.Unlock()
}

func (e *Engine) ListChats(ctx context.Context, sessionID domain.ID) ([]tools.ChatStatus, error) {
	if owner, err := e.LoadSession(ctx, sessionID); err == nil {
		snapshot := owner.Snapshot()
		statuses := make([]tools.ChatStatus, 0, len(snapshot.Chats))
		for _, item := range snapshot.Chats {
			status, err := owner.PollChat(ctx, item.ID)
			if err != nil {
				return nil, err
			}
			statuses = append(statuses, status)
		}
		return statuses, nil
	}
	chats, err := e.store.ListChats(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	statuses := make([]tools.ChatStatus, 0, len(chats))
	for _, item := range chats {
		status, err := e.PollChat(ctx, sessionID, item.ID)
		if err != nil {
			return nil, err
		}
		statuses = append(statuses, status)
	}
	return statuses, nil
}

func (e *Engine) PollChat(ctx context.Context, sessionID, chatID domain.ID) (tools.ChatStatus, error) {
	if owner, err := e.LoadSession(ctx, sessionID); err == nil {
		return owner.PollChat(ctx, chatID)
	}
	chatRecord, err := e.store.GetChat(ctx, chatID)
	if err != nil {
		return tools.ChatStatus{}, err
	}
	if sessionID != "" && chatRecord.SessionID != sessionID {
		return tools.ChatStatus{}, fmt.Errorf("chat %s does not belong to session %s", chatID, sessionID)
	}
	pending, err := e.store.PendingApprovalsForChat(ctx, chatID)
	if err != nil {
		return tools.ChatStatus{}, err
	}

	e.chatMu.RLock()
	activeChat := e.chats[chatID]
	run := e.runs[chatID]
	e.chatMu.RUnlock()

	state := run.state
	status := strings.TrimSpace(run.status)
	statusText := run.statusText
	lastError := run.lastError
	busy := false

	if activeChat != nil {
		chatStatus, text, active := activeChat.Status()
		status = string(chatStatus)
		statusText = text
		switch chatStatus {
		case chatpkg.StatusWaitingApproval:
			state = tools.ChatRunStateWaitingApproval
			busy = true
		case chatpkg.StatusErrored:
			state = tools.ChatRunStateFailed
			lastError = text
		default:
			if active {
				state = tools.ChatRunStateRunning
				busy = true
			}
		}
	}
	if state == "" {
		state = tools.ChatRunStateIdle
	}
	if status == "" {
		status = string(state)
	}
	if len(pending) > 0 && state == tools.ChatRunStateIdle {
		state = tools.ChatRunStateWaitingApproval
		status = string(chatpkg.StatusWaitingApproval)
		busy = true
		if strings.TrimSpace(statusText) == "" {
			statusText = "Waiting for approval"
		}
	}
	return tools.ChatStatus{
		Chat:             chatRecord,
		State:            state,
		Status:           status,
		Busy:             busy,
		PendingApprovals: len(pending),
		LastError:        lastError,
		StatusText:       statusText,
	}, nil
}

func (e *Engine) StartPreparedChat(ctx context.Context, session domain.Session, chatRecord domain.Chat, milestone store.Milestone, scopedTodo *store.TodoItem, role domain.WorkflowRole, objective string) (tools.ChatStatus, error) {
	if session.ID == "" {
		return tools.ChatStatus{}, fmt.Errorf("session id is required")
	}
	if chatRecord.ID == "" {
		return tools.ChatStatus{}, fmt.Errorf("chat id is required")
	}
	if chatRecord.SessionID != session.ID {
		return tools.ChatStatus{}, fmt.Errorf("chat %s does not belong to session %s", chatRecord.ID, session.ID)
	}
	objective = strings.TrimSpace(objective)
	if objective == "" {
		return tools.ChatStatus{}, fmt.Errorf("objective is required")
	}
	activeChat, err := e.Chat(ctx, session, chatRecord)
	if err != nil {
		return tools.ChatStatus{}, err
	}
	if owner := e.loadedSession(session.ID); owner != nil {
		owner.AdoptChat(chatRecord, activeChat)
	}
	e.setRunState(chatRecord.ID, chatRunState{state: tools.ChatRunStateRunning, statusText: "Starting background chat"})
	updates, unsub := activeChat.Subscribe()
	go e.consumeChatUpdates(chatRecord.ID, updates, unsub)
	activeChat.Enqueue(chatpkg.QueueItem{Kind: chatpkg.QueueKindSteer, Source: domain.UserMessageSourceAutoGenerated, Text: e.bootstrapPrompt(ctx, session.ID, milestone, scopedTodo, role, objective)})
	return e.PollChat(ctx, session.ID, chatRecord.ID)
}

func (e *Engine) consumeChatUpdates(chatID domain.ID, updates <-chan chatpkg.Update, unsub func()) {
	defer func() {
		if unsub != nil {
			unsub()
		}
	}()
	state := tools.ChatRunStateRunning
	status := string(chatpkg.StatusWaitingLLM)
	statusText := "Running"
	lastError := ""
	notifiedIdle := false
	sawActive := false
	for update := range updates {
		if !update.Active && !sawActive && update.Status != chatpkg.StatusWaitingApproval && update.Status != chatpkg.StatusErrored {
			continue
		}
		if update.Status != "" {
			status = string(update.Status)
		}
		switch update.Status {
		case chatpkg.StatusWaitingApproval:
			state = tools.ChatRunStateWaitingApproval
			if !notifiedIdle {
				notifiedIdle = true
				e.notifyParentChat(context.Background(), chatID, fmt.Sprintf("Chat %s is waiting for approval: %s", chatID, strings.TrimSpace(update.StatusText)))
			}
		case chatpkg.StatusErrored:
			state = tools.ChatRunStateFailed
			lastError = strings.TrimSpace(update.StatusText)
			if !notifiedIdle {
				notifiedIdle = true
				e.notifyParentChat(context.Background(), chatID, fmt.Sprintf("Chat %s failed: %s", chatID, strings.TrimSpace(update.StatusText)))
			}
		default:
			if update.Active {
				sawActive = true
				state = tools.ChatRunStateRunning
			} else if sawActive {
				state = tools.ChatRunStateIdle
				status = string(chatpkg.StatusIdle)
			}
		}
		if strings.TrimSpace(update.StatusText) != "" {
			statusText = strings.TrimSpace(update.StatusText)
		}
		e.setRunState(chatID, chatRunState{state: state, status: status, statusText: statusText, lastError: lastError})
		if !update.Active && sawActive && !notifiedIdle && state == tools.ChatRunStateIdle {
			notifiedIdle = true
			if e.parentAlreadyHasDoneNotification(context.Background(), chatID) {
				continue
			}
			if text := e.completedChildChatNotification(context.Background(), chatID); text != "" {
				e.notifyParentChat(context.Background(), chatID, text)
			} else {
				e.notifyParentChat(context.Background(), chatID, fmt.Sprintf("Chat %s is idle: %s", chatID, strings.TrimSpace(statusText)))
			}
		}
	}
	e.setRunState(chatID, chatRunState{state: state, status: status, statusText: statusText, lastError: lastError})
}

func (e *Engine) setRunState(chatID domain.ID, state chatRunState) {
	e.chatMu.Lock()
	defer e.chatMu.Unlock()
	e.runs[chatID] = state
}

func (e *Engine) notifyParentChat(ctx context.Context, sourceChatID domain.ID, text string) {
	source, err := e.store.GetChat(ctx, sourceChatID)
	if err != nil || source.ParentChatID == nil || strings.TrimSpace(text) == "" {
		return
	}
	e.enqueueSteer(ctx, *source.ParentChatID, text)
}

func (e *Engine) completedChildChatNotification(ctx context.Context, chatID domain.ID) string {
	chatRecord, err := e.store.GetChat(ctx, chatID)
	if err != nil || chatRecord.ParentChatID == nil {
		return ""
	}
	if chatRecord.AssignedTodoRef != "" {
		todos, err := e.store.ListTodos(ctx, chatRecord.SessionID, chatRecord.AssignedTodoBucketRef)
		if err == nil {
			for _, todo := range todos {
				if todo.ID == chatRecord.AssignedTodoRef && todo.Status == domain.TodoStatusCompleted {
					return fmt.Sprintf("Chat %s is done: todo #%s is completed.", chatID, todo.ID)
				}
			}
		}
	}
	if ref := strings.TrimSpace(chatRecord.ActiveMilestoneRef); ref != "" {
		plan, err := e.store.GetMilestonePlan(ctx, chatRecord.SessionID)
		if err == nil {
			for _, milestone := range plan.Milestones {
				if milestone.Ref != ref {
					continue
				}
				switch milestone.Status {
				case domain.MilestoneStatusCompleted, domain.MilestoneStatusBlocked, domain.MilestoneStatusCancelled, domain.MilestoneStatusReady:
					return fmt.Sprintf("Chat %s is done: milestone %s is %s.", chatID, ref, milestone.Status)
				}
			}
		}
	}
	return ""
}

func (e *Engine) parentAlreadyHasDoneNotification(ctx context.Context, sourceChatID domain.ID) bool {
	source, err := e.store.GetChat(ctx, sourceChatID)
	if err != nil || source.ParentChatID == nil {
		return false
	}
	needle := "chat " + strings.ToLower(string(sourceChatID)) + " is done"
	e.chatMu.RLock()
	parent := e.chats[*source.ParentChatID]
	e.chatMu.RUnlock()
	if parent != nil {
		for _, item := range parent.Snapshot().QueuedInputs {
			if strings.Contains(strings.ToLower(item.Text), needle) {
				return true
			}
		}
	}
	parentRecord, err := e.store.GetChat(ctx, *source.ParentChatID)
	if err != nil {
		return false
	}
	for _, item := range parentRecord.QueuedInputs {
		if strings.Contains(strings.ToLower(item.Text), needle) {
			return true
		}
	}
	return false
}

func (e *Engine) enqueueSteer(ctx context.Context, chatID domain.ID, text string) {
	text = strings.TrimSpace(text)
	if chatID == "" || text == "" {
		return
	}
	e.chatMu.RLock()
	activeChat := e.chats[chatID]
	e.chatMu.RUnlock()
	if activeChat != nil {
		activeChat.Enqueue(chatpkg.QueueItem{Kind: chatpkg.QueueKindSteer, Source: domain.UserMessageSourceSubchat, Text: text})
		return
	}
	chatRecord, err := e.store.GetChat(ctx, chatID)
	if err != nil {
		return
	}
	owner, err := e.LoadSession(ctx, chatRecord.SessionID)
	if err != nil {
		return
	}
	parent, err := owner.Chat(ctx, chatID)
	if err != nil {
		return
	}
	parent.Enqueue(chatpkg.QueueItem{Kind: chatpkg.QueueKindSteer, Source: domain.UserMessageSourceSubchat, Text: text})
}

func (e *Engine) bootstrapPrompt(ctx context.Context, sessionID domain.ID, milestone store.Milestone, scopedTodo *store.TodoItem, role domain.WorkflowRole, objective string) string {
	lines := []string{
		fmt.Sprintf("Profile: %s", role),
		"Objective:",
		strings.TrimSpace(objective),
	}
	if milestone.Ref != "" {
		todos, _ := e.store.ListTodos(ctx, sessionID, milestone.Ref)
		if scopedTodo != nil {
			todos = []store.TodoItem{*scopedTodo}
		}
		lines = append(lines,
			"",
			fmt.Sprintf("Milestone ref: %s", milestone.Ref),
			fmt.Sprintf("Milestone title: %s", milestone.Title),
			fmt.Sprintf("Milestone status: %s", milestone.Status),
		)
		if scopedTodo != nil {
			lines = append(lines, fmt.Sprintf("Todo scope: %s", scopedTodo.ID))
		}
		if notes := strings.TrimSpace(milestone.Notes); notes != "" {
			lines = append(lines, "Milestone notes:", notes)
		}
		if len(todos) == 0 {
			lines = append(lines, "Current todos: none")
		} else {
			lines = append(lines, "Current todos:")
			for _, item := range todos {
				lines = append(lines, fmt.Sprintf("- [%s] #%s %s", item.Status, item.ID, item.Content))
			}
		}
	}
	switch role {
	case chatrole.Decomposition:
		lines = append(lines, "", "Decompose only this milestone into concrete todo items.", "Use milestone and todo tools only for this milestone.", "When decomposition is complete, set the milestone status to ready and then go idle.", "Do not edit code in this chat.")
	case chatrole.Execution:
		lines = append(lines, "", "Execute only this milestone using its todo bucket as the working queue.", "Update todo statuses as you make progress and keep the milestone status accurate.", "When finished, set the milestone status to completed or blocked and then go idle.")
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}
