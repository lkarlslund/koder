package agent

import (
	"context"
	"fmt"
	"strings"

	chatpkg "github.com/lkarlslund/koder/internal/chat"
	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/store"
	"github.com/lkarlslund/koder/internal/tools"
)

type chatRunState struct {
	state      tools.ChatRunState
	statusText string
	lastError  string
}

func (e *Engine) Chat(ctx context.Context, session domain.Session, chatRecord domain.Chat) (*chatpkg.Chat, error) {
	if chatRecord.ID == 0 {
		return nil, fmt.Errorf("chat id is required")
	}
	e.chatMu.RLock()
	if existing := e.chats[chatRecord.ID]; existing != nil {
		e.chatMu.RUnlock()
		return existing, nil
	}
	e.chatMu.RUnlock()

	messages, parts, err := e.store.PartsForChat(ctx, chatRecord.ID)
	if err != nil {
		return nil, err
	}
	approvals, err := e.store.PendingApprovalsForChat(ctx, chatRecord.ID)
	if err != nil {
		return nil, err
	}
	loaded, err := chatpkg.New(session, chatRecord, messages, parts, approvals, e, e.store, e.detachChat)
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

func (e *Engine) detachChat(chatID int64) {
	e.chatMu.Lock()
	delete(e.chats, chatID)
	e.chatMu.Unlock()
}

func (e *Engine) ListChats(ctx context.Context, sessionID int64) ([]tools.ChatStatus, error) {
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

func (e *Engine) PollChat(ctx context.Context, sessionID, chatID int64) (tools.ChatStatus, error) {
	chatRecord, err := e.store.GetChat(ctx, chatID)
	if err != nil {
		return tools.ChatStatus{}, err
	}
	if sessionID > 0 && chatRecord.SessionID != sessionID {
		return tools.ChatStatus{}, fmt.Errorf("chat %d does not belong to session %d", chatID, sessionID)
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
	statusText := run.statusText
	lastError := run.lastError
	busy := false

	if activeChat != nil {
		chatStatus, text, active := activeChat.Status()
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
	if len(pending) > 0 && state == tools.ChatRunStateIdle {
		state = tools.ChatRunStateWaitingApproval
		busy = true
		if strings.TrimSpace(statusText) == "" {
			statusText = "Waiting for approval"
		}
	}
	return tools.ChatStatus{
		Chat:             chatRecord,
		State:            state,
		Busy:             busy,
		PendingApprovals: len(pending),
		LastError:        lastError,
		StatusText:       statusText,
	}, nil
}

func (e *Engine) StartDecomposition(ctx context.Context, sessionID, parentChatID int64, milestoneRef, title string) (tools.ChatStatus, error) {
	return e.startWorkflowChat(ctx, sessionID, parentChatID, domain.WorkflowRoleDecomposition, milestoneRef, title)
}

func (e *Engine) StartExecution(ctx context.Context, sessionID, parentChatID int64, milestoneRef, title string) (tools.ChatStatus, error) {
	return e.startWorkflowChat(ctx, sessionID, parentChatID, domain.WorkflowRoleExecution, milestoneRef, title)
}

func (e *Engine) startWorkflowChat(ctx context.Context, sessionID, parentChatID int64, role domain.WorkflowRole, milestoneRef, title string) (tools.ChatStatus, error) {
	session, err := e.store.GetSession(ctx, sessionID)
	if err != nil {
		return tools.ChatStatus{}, err
	}
	parentChat, err := e.store.GetChat(ctx, parentChatID)
	if err != nil {
		return tools.ChatStatus{}, err
	}
	plan, err := e.store.GetMilestonePlan(ctx, sessionID)
	if err != nil {
		return tools.ChatStatus{}, err
	}
	milestone, ok := milestoneByRef(plan, milestoneRef)
	if !ok {
		return tools.ChatStatus{}, fmt.Errorf("milestone %q not found", milestoneRef)
	}
	if err := e.updateMilestoneStatus(ctx, sessionID, milestoneRef, roleMilestoneStatus(role)); err != nil {
		return tools.ChatStatus{}, err
	}
	parentID := parentChat.ID
	chatTitle := strings.TrimSpace(title)
	if chatTitle == "" {
		chatTitle = fmt.Sprintf("%s: %s", roleDisplayName(role), milestone.Title)
	}
	chatRecord, err := e.store.CreateChat(ctx, sessionID, chatTitle, role, &parentID)
	if err != nil {
		return tools.ChatStatus{}, err
	}
	chatRecord.ActiveMilestoneRef = milestone.Ref
	chatRecord.AssignedTodoBucketRef = milestone.Ref
	if err := e.store.UpdateChat(ctx, chatRecord); err != nil {
		return tools.ChatStatus{}, err
	}
	activeChat, err := e.Chat(ctx, session, chatRecord)
	if err != nil {
		return tools.ChatStatus{}, err
	}
	e.setRunState(chatRecord.ID, chatRunState{state: tools.ChatRunStateRunning, statusText: "Starting background chat"})
	updates, unsub := activeChat.Subscribe()
	go e.consumeChatUpdates(chatRecord.ID, updates, unsub)
	activeChat.Enqueue(chatpkg.QueueItem{Kind: chatpkg.QueueKindSteer, Text: e.bootstrapPrompt(ctx, sessionID, milestone, role)})
	return e.PollChat(ctx, sessionID, chatRecord.ID)
}

func (e *Engine) consumeChatUpdates(chatID int64, updates <-chan chatpkg.Update, unsub func()) {
	defer func() {
		if unsub != nil {
			unsub()
		}
	}()
	state := tools.ChatRunStateRunning
	statusText := "Running"
	lastError := ""
	for update := range updates {
		switch update.Status {
		case chatpkg.StatusWaitingApproval:
			state = tools.ChatRunStateWaitingApproval
		case chatpkg.StatusErrored:
			state = tools.ChatRunStateFailed
			lastError = strings.TrimSpace(update.StatusText)
		default:
			if update.Active {
				state = tools.ChatRunStateRunning
			} else if state == tools.ChatRunStateRunning {
				state = tools.ChatRunStateCompleted
			}
		}
		if strings.TrimSpace(update.StatusText) != "" {
			statusText = strings.TrimSpace(update.StatusText)
		}
		e.setRunState(chatID, chatRunState{state: state, statusText: statusText, lastError: lastError})
	}
	if state == tools.ChatRunStateRunning {
		state = tools.ChatRunStateCompleted
		statusText = "Completed"
	}
	e.setRunState(chatID, chatRunState{state: state, statusText: statusText, lastError: lastError})
}

func (e *Engine) setRunState(chatID int64, state chatRunState) {
	e.chatMu.Lock()
	defer e.chatMu.Unlock()
	e.runs[chatID] = state
}

func (e *Engine) bootstrapPrompt(ctx context.Context, sessionID int64, milestone store.Milestone, role domain.WorkflowRole) string {
	todos, _ := e.store.ListTodos(ctx, sessionID, milestone.Ref)
	lines := []string{
		fmt.Sprintf("Milestone ref: %s", milestone.Ref),
		fmt.Sprintf("Milestone title: %s", milestone.Title),
		fmt.Sprintf("Milestone status: %s", milestone.Status),
	}
	if notes := strings.TrimSpace(milestone.Notes); notes != "" {
		lines = append(lines, "Milestone notes:", notes)
	}
	if len(todos) == 0 {
		lines = append(lines, "Current todos: none")
	} else {
		lines = append(lines, "Current todos:")
		for _, item := range todos {
			lines = append(lines, fmt.Sprintf("- [%s] #%d %s", item.Status, item.ID, item.Content))
		}
	}
	switch role {
	case domain.WorkflowRoleDecomposition:
		lines = append(lines, "", "Decompose only this milestone into concrete todo items.", "Use milestone and todo tools only for this milestone.", "Do not edit code in this chat.")
	case domain.WorkflowRoleExecution:
		lines = append(lines, "", "Execute only this milestone using its todo bucket as the working queue.", "Update todo statuses as you make progress and keep the milestone status accurate.")
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func (e *Engine) updateMilestoneStatus(ctx context.Context, sessionID int64, ref string, status domain.MilestoneStatus) error {
	plan, err := e.store.GetMilestonePlan(ctx, sessionID)
	if err != nil {
		return err
	}
	for idx := range plan.Milestones {
		switch {
		case plan.Milestones[idx].Ref != ref:
			switch plan.Milestones[idx].Status {
			case domain.MilestoneStatusInProgress, domain.MilestoneStatusDecomposing, domain.MilestoneStatusExecuting:
				plan.Milestones[idx].Status = domain.MilestoneStatusPending
			}
		default:
			plan.Milestones[idx].Status = status
		}
	}
	_, err = e.store.SetMilestonePlan(ctx, sessionID, plan.Summary, plan.Milestones)
	return err
}

func roleMilestoneStatus(role domain.WorkflowRole) domain.MilestoneStatus {
	switch role {
	case domain.WorkflowRoleDecomposition:
		return domain.MilestoneStatusDecomposing
	case domain.WorkflowRoleExecution:
		return domain.MilestoneStatusExecuting
	default:
		return domain.MilestoneStatusInProgress
	}
}

func roleDisplayName(role domain.WorkflowRole) string {
	switch role {
	case domain.WorkflowRoleDecomposition:
		return "Decompose"
	case domain.WorkflowRoleExecution:
		return "Execute"
	case domain.WorkflowRoleOrchestrator:
		return "Orchestrate"
	default:
		return "Chat"
	}
}

func milestoneByRef(plan store.MilestonePlan, ref string) (store.Milestone, bool) {
	for _, item := range plan.Milestones {
		if item.Ref == ref {
			return item, true
		}
	}
	return store.Milestone{}, false
}
