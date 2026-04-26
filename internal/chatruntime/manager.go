package chatruntime

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/lkarlslund/koder/internal/agent"
	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/store"
	"github.com/lkarlslund/koder/internal/tools"
)

type Manager struct {
	engine *agent.Engine
	store  *store.Store

	mu   sync.RWMutex
	runs map[int64]runState
}

type runState struct {
	state      tools.ChatRunState
	statusText string
	lastError  string
	cancel     context.CancelFunc
}

func New(engine *agent.Engine, st *store.Store) *Manager {
	return &Manager{
		engine: engine,
		store:  st,
		runs:   map[int64]runState{},
	}
}

func (m *Manager) ListChats(ctx context.Context, sessionID int64) ([]tools.ChatStatus, error) {
	chats, err := m.store.ListChats(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	statuses := make([]tools.ChatStatus, 0, len(chats))
	for _, chat := range chats {
		status, err := m.chatStatus(ctx, chat)
		if err != nil {
			return nil, err
		}
		statuses = append(statuses, status)
	}
	return statuses, nil
}

func (m *Manager) PollChat(ctx context.Context, sessionID, chatID int64) (tools.ChatStatus, error) {
	chat, err := m.store.GetChat(ctx, chatID)
	if err != nil {
		return tools.ChatStatus{}, err
	}
	if sessionID > 0 && chat.SessionID != sessionID {
		return tools.ChatStatus{}, fmt.Errorf("chat %d does not belong to session %d", chatID, sessionID)
	}
	return m.chatStatus(ctx, chat)
}

func (m *Manager) StartDecomposition(ctx context.Context, sessionID, parentChatID int64, milestoneRef, title string) (tools.ChatStatus, error) {
	return m.startWorkflowChat(ctx, sessionID, parentChatID, domain.WorkflowRoleDecomposition, milestoneRef, title)
}

func (m *Manager) StartExecution(ctx context.Context, sessionID, parentChatID int64, milestoneRef, title string) (tools.ChatStatus, error) {
	return m.startWorkflowChat(ctx, sessionID, parentChatID, domain.WorkflowRoleExecution, milestoneRef, title)
}

func (m *Manager) startWorkflowChat(ctx context.Context, sessionID, parentChatID int64, role domain.WorkflowRole, milestoneRef, title string) (tools.ChatStatus, error) {
	session, err := m.store.GetSession(ctx, sessionID)
	if err != nil {
		return tools.ChatStatus{}, err
	}
	parentChat, err := m.store.GetChat(ctx, parentChatID)
	if err != nil {
		return tools.ChatStatus{}, err
	}
	plan, err := m.store.GetMilestonePlan(ctx, sessionID)
	if err != nil {
		return tools.ChatStatus{}, err
	}
	milestone, ok := milestoneByRef(plan, milestoneRef)
	if !ok {
		return tools.ChatStatus{}, fmt.Errorf("milestone %q not found", milestoneRef)
	}
	if err := m.updateMilestoneStatus(ctx, sessionID, milestoneRef, roleMilestoneStatus(role)); err != nil {
		return tools.ChatStatus{}, err
	}
	parentID := parentChat.ID
	chatTitle := strings.TrimSpace(title)
	if chatTitle == "" {
		chatTitle = fmt.Sprintf("%s: %s", roleDisplayName(role), milestone.Title)
	}
	chat, err := m.store.CreateChat(ctx, sessionID, chatTitle, role, &parentID)
	if err != nil {
		return tools.ChatStatus{}, err
	}
	chat.ActiveMilestoneRef = milestone.Ref
	chat.AssignedTodoBucketRef = milestone.Ref
	if err := m.store.UpdateChat(ctx, chat); err != nil {
		return tools.ChatStatus{}, err
	}
	prompt := m.bootstrapPrompt(ctx, sessionID, milestone, role)
	runCtx, cancel := context.WithCancel(context.Background())
	m.setRunState(chat.ID, runState{
		state:      tools.ChatRunStateRunning,
		statusText: "Starting background chat",
		cancel:     cancel,
	})
	events, err := m.engine.RunPromptInChat(runCtx, session, chat, prompt, nil, nil, "")
	if err != nil {
		cancel()
		m.setRunState(chat.ID, runState{
			state:      tools.ChatRunStateFailed,
			statusText: "Failed to start",
			lastError:  err.Error(),
		})
		return tools.ChatStatus{}, err
	}
	go m.consume(chat.ID, cancel, events)
	return m.chatStatus(ctx, chat)
}

func (m *Manager) bootstrapPrompt(ctx context.Context, sessionID int64, milestone store.Milestone, role domain.WorkflowRole) string {
	todos, _ := m.store.ListTodos(ctx, sessionID, milestone.Ref)
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
		lines = append(lines,
			"",
			"Decompose only this milestone into concrete todo items.",
			"Use milestone and todo tools only for this milestone.",
			"Do not edit code in this chat.",
		)
	case domain.WorkflowRoleExecution:
		lines = append(lines,
			"",
			"Execute only this milestone using its todo bucket as the working queue.",
			"Update todo statuses as you make progress and keep the milestone status accurate.",
		)
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func (m *Manager) consume(chatID int64, cancel context.CancelFunc, events <-chan domain.Event) {
	state := tools.ChatRunStateRunning
	statusText := "Running"
	lastError := ""
	for evt := range events {
		switch evt.Kind {
		case domain.EventKindStatus:
			if text := strings.TrimSpace(evt.Text); text != "" {
				statusText = text
			}
		case domain.EventKindApprovalAsk:
			state = tools.ChatRunStateWaitingApproval
			statusText = strings.TrimSpace(evt.Text)
		case domain.EventKindError:
			state = tools.ChatRunStateFailed
			if evt.Err != nil {
				lastError = evt.Err.Error()
			} else {
				lastError = strings.TrimSpace(evt.Text)
			}
			statusText = "Failed"
		case domain.EventKindMessageDone:
			if state != tools.ChatRunStateWaitingApproval && state != tools.ChatRunStateFailed && state != tools.ChatRunStateCancelled {
				state = tools.ChatRunStateCompleted
				statusText = "Completed"
			}
		}
		m.setRunState(chatID, runState{
			state:      state,
			statusText: statusText,
			lastError:  lastError,
			cancel:     cancel,
		})
	}
	if state == tools.ChatRunStateRunning {
		state = tools.ChatRunStateCompleted
		statusText = "Completed"
	}
	m.setRunState(chatID, runState{
		state:      state,
		statusText: statusText,
		lastError:  lastError,
	})
}

func (m *Manager) chatStatus(ctx context.Context, chat domain.Chat) (tools.ChatStatus, error) {
	pending, err := m.store.PendingApprovalsForChat(ctx, chat.ID)
	if err != nil {
		return tools.ChatStatus{}, err
	}
	m.mu.RLock()
	run := m.runs[chat.ID]
	m.mu.RUnlock()
	state := run.state
	if state == "" {
		state = tools.ChatRunStateIdle
	}
	status := tools.ChatStatus{
		Chat:             chat,
		State:            state,
		Busy:             state == tools.ChatRunStateRunning || state == tools.ChatRunStateWaitingApproval,
		PendingApprovals: len(pending),
		LastError:        run.lastError,
		StatusText:       run.statusText,
	}
	if len(pending) > 0 && state == tools.ChatRunStateIdle {
		status.State = tools.ChatRunStateWaitingApproval
		status.Busy = true
		if status.StatusText == "" {
			status.StatusText = "Waiting for approval"
		}
	}
	return status, nil
}

func (m *Manager) setRunState(chatID int64, state runState) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.runs[chatID] = state
}

func (m *Manager) updateMilestoneStatus(ctx context.Context, sessionID int64, ref string, status domain.MilestoneStatus) error {
	plan, err := m.store.GetMilestonePlan(ctx, sessionID)
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
	_, err = m.store.SetMilestonePlan(ctx, sessionID, plan.Summary, plan.Milestones)
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
