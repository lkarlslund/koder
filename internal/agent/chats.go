package agent

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	chatpkg "github.com/lkarlslund/koder/internal/chat"
	"github.com/lkarlslund/koder/internal/chatrole"
	"github.com/lkarlslund/koder/internal/domain"
	sessionpkg "github.com/lkarlslund/koder/internal/session"
	"github.com/lkarlslund/koder/internal/store"
	"github.com/lkarlslund/koder/internal/tools"
)

func (e *Engine) Chat(ctx context.Context, session domain.Session, chatRecord domain.Chat) (*chatpkg.Chat, error) {
	if chatRecord.ID == "" {
		return nil, fmt.Errorf("chat id is required")
	}
	return chatpkg.Load(ctx, session, chatRecord, e.ChatDeps(), nil)
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

func (e *Engine) ListChats(ctx context.Context, sessionID domain.ID) ([]tools.ChatStatus, error) {
	owner, err := e.LoadSession(ctx, sessionID)
	if err != nil {
		return nil, err
	}
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

func (e *Engine) PollChat(ctx context.Context, sessionID, chatID domain.ID) (tools.ChatStatus, error) {
	owner, err := e.LoadSession(ctx, sessionID)
	if err != nil {
		return tools.ChatStatus{}, err
	}
	return owner.PollChat(ctx, chatID)
}

func (e *Engine) StartChat(ctx context.Context, sessionID, parentChatID domain.ID, req tools.ChatStartRequest) (tools.ChatStatus, error) {
	owner, err := e.LoadSession(ctx, sessionID)
	if err != nil {
		return tools.ChatStatus{}, err
	}
	snapshot := owner.Snapshot()
	session := snapshot.Session
	parentChat, ok := chatByID(snapshot.Chats, parentChatID)
	if session.ID == "" || session.ID != sessionID {
		return tools.ChatStatus{}, fmt.Errorf("session %s is not active", sessionID)
	}
	if !ok {
		return tools.ChatStatus{}, fmt.Errorf("parent chat %s not found", parentChatID)
	}
	role := domain.WorkflowRole(strings.TrimSpace(string(req.Profile)))
	if _, ok := chatrole.DefaultRegistry().Lookup(role); !ok {
		return tools.ChatStatus{}, fmt.Errorf("profile %q is not registered", role)
	}
	objective := strings.TrimSpace(req.Objective)
	if objective == "" {
		return tools.ChatStatus{}, fmt.Errorf("objective is required")
	}
	plan, err := owner.GetMilestonePlan(ctx, sessionID)
	if err != nil {
		return tools.ChatStatus{}, err
	}
	milestoneRef := strings.TrimSpace(req.MilestoneRef)
	todoRef := domain.ID(strings.TrimSpace(string(req.TodoRef)))
	var scopedTodo *store.TodoItem
	if todoRef != "" {
		todo, err := sessionTodoByID(ctx, owner, sessionID, plan, todoRef)
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
	if role == chatrole.Execution && milestoneRef == "" {
		return tools.ChatStatus{}, fmt.Errorf("execution chat requires milestone_ref or todo_ref")
	}
	if role == chatrole.Execution && milestone.Status != domain.MilestoneStatusReady {
		return tools.ChatStatus{}, fmt.Errorf("milestone %q is %s, expected ready", milestoneRef, milestone.Status)
	}
	parentID := parentChat.ID
	chatTitle := strings.TrimSpace(req.Title)
	if chatTitle == "" {
		chatTitle = defaultChildChatTitle(role, milestone, scopedTodo)
	}
	now := time.Now().UTC()
	chatRecord := domain.Chat{
		ID:                    domain.NewID(),
		SessionID:             sessionID,
		ParentChatID:          &parentID,
		Title:                 chatTitle,
		WorkflowRole:          role,
		ProviderID:            strings.TrimSpace(parentChat.ProviderID),
		ModelID:               strings.TrimSpace(parentChat.ModelID),
		PermissionProfile:     strings.TrimSpace(parentChat.PermissionProfile),
		ToolStates:            cloneToolStateMap(parentChat.ToolStates),
		ActiveMilestoneRef:    milestoneRef,
		AssignedTodoBucketRef: milestoneRef,
		AssignedTodoRef:       todoRef,
		Position:              len(snapshot.Chats),
		CreatedAt:             now,
		UpdatedAt:             now,
	}
	if _, err := owner.AddPreparedChat(ctx, chatRecord); err != nil {
		return tools.ChatStatus{}, err
	}
	if status := roleMilestoneStatus(role); status != "" {
		nextPlan, err := updateMilestoneStatus(plan, milestoneRef, status, chatRecord.ID)
		if err != nil {
			return tools.ChatStatus{}, err
		}
		plan, err = owner.SetMilestonePlan(ctx, sessionID, nextPlan.Summary, nextPlan.Milestones)
		if err != nil {
			return tools.ChatStatus{}, err
		}
		milestone, _ = milestoneByRef(plan, milestoneRef)
	}
	if todoRef != "" && role == chatrole.Execution && scopedTodo != nil && scopedTodo.Status == domain.TodoStatusPending {
		todo, err := owner.UpdateTodoItem(ctx, todoRef, domain.TodoStatusInProgress, scopedTodo.Content)
		if err != nil {
			return tools.ChatStatus{}, err
		}
		scopedTodo = &todo
	}
	return e.startPreparedChat(ctx, owner, chatRecord.ID, milestone, scopedTodo, role, objective)
}

func (e *Engine) ArchiveChat(ctx context.Context, sessionID, chatID domain.ID) (tools.ChatStatus, error) {
	owner, err := e.LoadSession(ctx, sessionID)
	if err != nil {
		return tools.ChatStatus{}, err
	}
	status, _, err := owner.ArchiveChat(ctx, chatID)
	return status, err
}

func sessionTodoByID(ctx context.Context, owner interface {
	ListTodos(context.Context, domain.ID, string) ([]store.TodoItem, error)
}, sessionID domain.ID, plan store.MilestonePlan, todoID domain.ID) (store.TodoItem, error) {
	for _, milestone := range plan.Milestones {
		todos, err := owner.ListTodos(ctx, sessionID, milestone.Ref)
		if err != nil {
			return store.TodoItem{}, err
		}
		for _, todo := range todos {
			if todo.ID == todoID {
				return todo, nil
			}
		}
	}
	todos, err := owner.ListTodos(ctx, sessionID, "")
	if err != nil {
		return store.TodoItem{}, err
	}
	for _, todo := range todos {
		if todo.ID == todoID {
			return todo, nil
		}
	}
	return store.TodoItem{}, fmt.Errorf("todo %s not found", todoID)
}

func updateMilestoneStatus(plan store.MilestonePlan, ref string, status domain.MilestoneStatus, ownerChatID domain.ID) (store.MilestonePlan, error) {
	next := plan
	next.Milestones = slices.Clone(plan.Milestones)
	found := false
	for idx := range next.Milestones {
		if next.Milestones[idx].Ref != ref {
			continue
		}
		found = true
		if next.Milestones[idx].OwnerChatID != nil && *next.Milestones[idx].OwnerChatID != ownerChatID {
			return store.MilestonePlan{}, fmt.Errorf("milestone %q is owned by chat %s", ref, *next.Milestones[idx].OwnerChatID)
		}
		next.Milestones[idx].Status = status
		if status == domain.MilestoneStatusDecomposing || status == domain.MilestoneStatusExecuting {
			owner := ownerChatID
			next.Milestones[idx].OwnerChatID = &owner
		} else {
			next.Milestones[idx].OwnerChatID = nil
		}
	}
	if !found {
		return store.MilestonePlan{}, fmt.Errorf("milestone %q not found", ref)
	}
	return next, nil
}

func roleMilestoneStatus(role domain.WorkflowRole) domain.MilestoneStatus {
	switch role {
	case chatrole.Execution:
		return domain.MilestoneStatusExecuting
	default:
		return ""
	}
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
	for _, milestone := range plan.Milestones {
		if milestone.Ref == ref {
			return milestone, true
		}
	}
	return store.Milestone{}, false
}

func chatByID(chats []domain.Chat, chatID domain.ID) (domain.Chat, bool) {
	for _, item := range chats {
		if item.ID == chatID {
			return item, true
		}
	}
	return domain.Chat{}, false
}

func (e *Engine) startPreparedChat(ctx context.Context, owner *sessionpkg.Session, chatID domain.ID, milestone store.Milestone, scopedTodo *store.TodoItem, role domain.WorkflowRole, objective string) (tools.ChatStatus, error) {
	if owner == nil {
		return tools.ChatStatus{}, fmt.Errorf("session is required")
	}
	if chatID == "" {
		return tools.ChatStatus{}, fmt.Errorf("chat id is required")
	}
	objective = strings.TrimSpace(objective)
	if objective == "" {
		return tools.ChatStatus{}, fmt.Errorf("objective is required")
	}
	snapshot := owner.Snapshot()
	activeChat, err := owner.Chat(ctx, chatID)
	if err != nil {
		return tools.ChatStatus{}, err
	}
	updates, unsub := activeChat.Subscribe()
	go e.consumeChatUpdates(chatID, updates, unsub)
	activeChat.Enqueue(chatpkg.QueueItem{Kind: chatpkg.QueueKindSteer, Source: domain.UserMessageSourceAutoGenerated, Text: e.bootstrapPrompt(ctx, snapshot.Session.ID, milestone, scopedTodo, role, objective)})
	return owner.PollChat(ctx, chatID)
}

func (e *Engine) consumeChatUpdates(chatID domain.ID, updates <-chan chatpkg.Update, unsub func()) {
	defer func() {
		if unsub != nil {
			unsub()
		}
	}()
	statusText := "Running"
	notifiedIdle := false
	sawActive := false
	for update := range updates {
		if !update.Active && !sawActive && update.Status != chatpkg.StatusWaitingApproval && update.Status != chatpkg.StatusErrored {
			continue
		}
		switch update.Status {
		case chatpkg.StatusWaitingApproval:
			if !notifiedIdle {
				notifiedIdle = true
				e.notifyParentChat(context.Background(), chatID, fmt.Sprintf("Chat %s is waiting for approval: %s", chatID, strings.TrimSpace(update.StatusText)))
			}
		case chatpkg.StatusErrored:
			if !notifiedIdle {
				notifiedIdle = true
				e.notifyParentChat(context.Background(), chatID, fmt.Sprintf("Chat %s failed: %s", chatID, strings.TrimSpace(update.StatusText)))
			}
		default:
			if update.Active {
				sawActive = true
			}
		}
		if strings.TrimSpace(update.StatusText) != "" {
			statusText = strings.TrimSpace(update.StatusText)
		}
		if !update.Active && sawActive && !notifiedIdle {
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
	if owner := e.loadedSession(source.SessionID); owner != nil {
		parent, err := owner.Chat(ctx, *source.ParentChatID)
		if err == nil {
			for _, item := range parent.Snapshot().QueuedInputs {
				if strings.Contains(strings.ToLower(item.Text), needle) {
					return true
				}
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
	case chatrole.Execution:
		lines = append(lines, "", "Execute only this milestone using its todo bucket as the working queue.", "Update todo statuses as you make progress and keep the milestone status accurate.", "When finished, set the milestone status to completed or blocked and then go idle.")
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}
