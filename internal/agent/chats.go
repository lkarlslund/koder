package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

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

	loaded, err := chatpkg.Load(ctx, e.store, session, chatRecord, e, e.detachChat)
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

func (e *Engine) detachChat(chatID domain.ID) {
	e.chatMu.Lock()
	delete(e.chats, chatID)
	e.chatMu.Unlock()
}

func (e *Engine) ListChats(ctx context.Context, sessionID domain.ID) ([]tools.ChatStatus, error) {
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

func (e *Engine) StartChat(ctx context.Context, sessionID, parentChatID domain.ID, req tools.ChatStartRequest) (tools.ChatStatus, error) {
	session, err := e.store.GetSession(ctx, sessionID)
	if err != nil {
		return tools.ChatStatus{}, err
	}
	parentChat, err := e.store.GetChat(ctx, parentChatID)
	if err != nil {
		return tools.ChatStatus{}, err
	}
	role := domain.WorkflowRole(strings.TrimSpace(string(req.Profile)))
	if _, ok := chatrole.DefaultRegistry().Lookup(role); !ok {
		return tools.ChatStatus{}, fmt.Errorf("profile %q is not registered", role)
	}
	objective := strings.TrimSpace(req.Objective)
	if objective == "" {
		return tools.ChatStatus{}, fmt.Errorf("objective is required")
	}
	plan, err := e.store.GetMilestonePlan(ctx, sessionID)
	if err != nil {
		return tools.ChatStatus{}, err
	}
	milestoneRef := strings.TrimSpace(req.MilestoneRef)
	todoRef := domain.ID(strings.TrimSpace(string(req.TodoRef)))
	var scopedTodo *store.TodoItem
	if todoRef != "" {
		todo, err := e.todoByID(ctx, sessionID, plan, todoRef)
		if err != nil {
			return tools.ChatStatus{}, err
		}
		scopedTodo = &todo
		if milestoneRef != "" && todo.MilestoneRef != milestoneRef {
			return tools.ChatStatus{}, fmt.Errorf("todo %s belongs to milestone %q, not %q", todoRef, todo.MilestoneRef, milestoneRef)
		}
		milestoneRef = todo.MilestoneRef
	}
	var milestone store.Milestone
	if milestoneRef != "" {
		var ok bool
		milestone, ok = milestoneByRef(plan, milestoneRef)
		if !ok {
			return tools.ChatStatus{}, fmt.Errorf("milestone %q not found", milestoneRef)
		}
		if milestone.OwnerChatID != nil {
			return tools.ChatStatus{}, fmt.Errorf("milestone %q is owned by chat %s", milestoneRef, *milestone.OwnerChatID)
		}
	}
	if role == chatrole.Decomposition && milestoneRef == "" {
		return tools.ChatStatus{}, fmt.Errorf("decomposition chat requires milestone_ref or todo_ref")
	}
	if role == chatrole.Execution && milestoneRef == "" {
		return tools.ChatStatus{}, fmt.Errorf("execution chat requires milestone_ref or todo_ref")
	}
	if role == chatrole.Decomposition && milestone.Status != domain.MilestoneStatusPending && milestone.Status != domain.MilestoneStatusReady {
		return tools.ChatStatus{}, fmt.Errorf("milestone %q is %s, expected pending or ready", milestoneRef, milestone.Status)
	}
	if role == chatrole.Execution && milestone.Status != domain.MilestoneStatusReady {
		return tools.ChatStatus{}, fmt.Errorf("milestone %q is %s, expected ready", milestoneRef, milestone.Status)
	}
	parentID := parentChat.ID
	chatTitle := strings.TrimSpace(req.Title)
	if chatTitle == "" {
		chatTitle = defaultChildChatTitle(role, milestone, scopedTodo)
	}
	chatRecord, err := e.store.CreateChat(ctx, sessionID, chatTitle, role, &parentID)
	if err != nil {
		return tools.ChatStatus{}, err
	}
	chatRecord.ProviderID = strings.TrimSpace(parentChat.ProviderID)
	chatRecord.ModelID = strings.TrimSpace(parentChat.ModelID)
	chatRecord.PermissionProfile = strings.TrimSpace(parentChat.PermissionProfile)
	chatRecord.ToolStates = cloneToolStateMap(parentChat.ToolStates)
	chatRecord.ActiveMilestoneRef = milestoneRef
	chatRecord.AssignedTodoBucketRef = milestoneRef
	chatRecord.AssignedTodoRef = todoRef
	if err := e.store.UpdateChat(ctx, chatRecord); err != nil {
		return tools.ChatStatus{}, err
	}
	if status := roleMilestoneStatus(role); status != "" {
		if err := e.updateMilestoneStatus(ctx, sessionID, milestoneRef, status, chatRecord.ID); err != nil {
			return tools.ChatStatus{}, err
		}
	}
	if todoRef != "" && role == chatrole.Execution && scopedTodo != nil && scopedTodo.Status == domain.TodoStatusPending {
		if _, err := e.store.UpdateTodoItem(ctx, todoRef, domain.TodoStatusInProgress, scopedTodo.Content); err != nil {
			return tools.ChatStatus{}, err
		}
	}
	if milestoneRef != "" {
		plan, err = e.store.GetMilestonePlan(ctx, sessionID)
		if err != nil {
			return tools.ChatStatus{}, err
		}
		milestone, _ = milestoneByRef(plan, milestoneRef)
	}
	if todoRef != "" {
		todo, err := e.todoByID(ctx, sessionID, plan, todoRef)
		if err != nil {
			return tools.ChatStatus{}, err
		}
		scopedTodo = &todo
	}
	activeChat, err := e.Chat(ctx, session, chatRecord)
	if err != nil {
		return tools.ChatStatus{}, err
	}
	e.setRunState(chatRecord.ID, chatRunState{state: tools.ChatRunStateRunning, statusText: "Starting background chat"})
	updates, unsub := activeChat.Subscribe()
	go e.consumeChatUpdates(chatRecord.ID, updates, unsub)
	activeChat.Enqueue(chatpkg.QueueItem{Kind: chatpkg.QueueKindSteer, Source: domain.UserMessageSourceAutoGenerated, Text: e.bootstrapPrompt(ctx, sessionID, milestone, scopedTodo, role, objective)})
	return e.PollChat(ctx, sessionID, chatRecord.ID)
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
			e.notifyParentChat(context.Background(), chatID, fmt.Sprintf("Chat %s is idle: %s", chatID, strings.TrimSpace(statusText)))
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
	chatRecord.QueuedInputs = append(chatRecord.QueuedInputs, domain.QueuedInput{
		ID:        domain.NewID(),
		Kind:      domain.QueuedInputKindSteer,
		Text:      text,
		Source:    domain.UserMessageSourceSubchat,
		CreatedAt: time.Now().UTC(),
	})
	_ = e.store.UpdateChat(ctx, chatRecord)
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

func (e *Engine) updateMilestoneStatus(ctx context.Context, sessionID domain.ID, ref string, status domain.MilestoneStatus, ownerChatID domain.ID) error {
	plan, err := e.store.GetMilestonePlan(ctx, sessionID)
	if err != nil {
		return err
	}
	found := false
	for idx := range plan.Milestones {
		if plan.Milestones[idx].Ref == ref {
			found = true
			if plan.Milestones[idx].OwnerChatID != nil && *plan.Milestones[idx].OwnerChatID != ownerChatID {
				return fmt.Errorf("milestone %q is owned by chat %s", ref, *plan.Milestones[idx].OwnerChatID)
			}
			plan.Milestones[idx].Status = status
			if status == domain.MilestoneStatusDecomposing || status == domain.MilestoneStatusExecuting {
				owner := ownerChatID
				plan.Milestones[idx].OwnerChatID = &owner
			} else {
				plan.Milestones[idx].OwnerChatID = nil
			}
		}
	}
	if !found {
		return fmt.Errorf("milestone %q not found", ref)
	}
	_, err = e.store.SetMilestonePlan(ctx, sessionID, plan.Summary, plan.Milestones)
	return err
}

func roleMilestoneStatus(role domain.WorkflowRole) domain.MilestoneStatus {
	switch role {
	case chatrole.Decomposition:
		return domain.MilestoneStatusDecomposing
	case chatrole.Execution:
		return domain.MilestoneStatusExecuting
	default:
		return ""
	}
}

func (e *Engine) todoByID(ctx context.Context, sessionID domain.ID, plan store.MilestonePlan, todoID domain.ID) (store.TodoItem, error) {
	for _, milestone := range plan.Milestones {
		todos, err := e.store.ListTodos(ctx, sessionID, milestone.Ref)
		if err != nil {
			return store.TodoItem{}, err
		}
		for _, todo := range todos {
			if todo.ID == todoID {
				return todo, nil
			}
		}
	}
	return store.TodoItem{}, fmt.Errorf("todo %s not found", todoID)
}

func defaultChildChatTitle(role domain.WorkflowRole, milestone store.Milestone, todo *store.TodoItem) string {
	prefix := chatrole.DisplayName(role)
	if todo != nil {
		return fmt.Sprintf("%s: %s", prefix, todo.Content)
	}
	if strings.TrimSpace(milestone.Title) != "" {
		return fmt.Sprintf("%s: %s", prefix, milestone.Title)
	}
	return prefix
}

func cloneToolStateMap(src map[domain.ToolKind]bool) map[domain.ToolKind]bool {
	if len(src) == 0 {
		return map[domain.ToolKind]bool{}
	}
	out := make(map[domain.ToolKind]bool, len(src))
	for key, value := range src {
		out[key] = value
	}
	return out
}

func milestoneByRef(plan store.MilestonePlan, ref string) (store.Milestone, bool) {
	for _, item := range plan.Milestones {
		if item.Ref == ref {
			return item, true
		}
	}
	return store.Milestone{}, false
}
