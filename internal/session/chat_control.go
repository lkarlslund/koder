package session

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	chatpkg "github.com/lkarlslund/koder/internal/chat"
	"github.com/lkarlslund/koder/internal/chatrole"
	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/id"
	"github.com/lkarlslund/koder/internal/planning"
	"github.com/lkarlslund/koder/internal/tools/chattool"
)

// ChatToolControl returns chat orchestration operations scoped to ownerChatID.
func (s *Session) ChatToolControl(ownerChatID id.ID) chattool.Control {
	return chatControl{session: s, ownerChatID: ownerChatID}
}

type chatControl struct {
	session     *Session
	ownerChatID id.ID
}

func (c chatControl) ListChats(ctx context.Context, sessionID id.ID) ([]chattool.Status, error) {
	if err := c.session.requireSession(sessionID); err != nil {
		return nil, err
	}
	snapshot := c.session.Snapshot()
	statuses := make([]chattool.Status, 0, len(snapshot.Chats))
	for _, item := range snapshot.Chats {
		status, err := c.session.ChatStatus(ctx, item.ID)
		if err != nil {
			return nil, err
		}
		statuses = append(statuses, status)
	}
	return statuses, nil
}

func (c chatControl) StartChat(ctx context.Context, sessionID, parentChatID id.ID, req chattool.StartRequest) (chattool.Status, error) {
	if err := c.session.requireSession(sessionID); err != nil {
		return chattool.Status{}, err
	}
	snapshot := c.session.Snapshot()
	session := snapshot.Session
	parentChat, ok := chatByID(snapshot.Chats, parentChatID)
	if session.ID == "" || session.ID != sessionID {
		return chattool.Status{}, fmt.Errorf("session %s is not active", sessionID)
	}
	if !ok {
		return chattool.Status{}, fmt.Errorf("parent chat %s not found", parentChatID)
	}
	if parentChat.Archived {
		return chattool.Status{}, fmt.Errorf("cannot start a child chat from archived chat %s", parentChatID)
	}
	role := domain.WorkflowRole(strings.TrimSpace(string(req.Profile)))
	if _, ok := chatrole.DefaultRegistry().Lookup(role); !ok {
		return chattool.Status{}, fmt.Errorf("profile %q is not registered", role)
	}
	objective := strings.TrimSpace(req.Objective)
	if objective == "" {
		return chattool.Status{}, fmt.Errorf("objective is required")
	}
	plan, err := c.session.GetMilestonePlan(ctx, sessionID)
	if err != nil {
		return chattool.Status{}, err
	}
	milestoneRef := strings.TrimSpace(req.MilestoneRef)
	todoRef := id.ID(strings.TrimSpace(string(req.TodoRef)))
	var scopedTodo *planning.TodoItem
	if todoRef != "" {
		todo, err := sessionTodoByID(ctx, c.session, sessionID, plan, todoRef)
		if err != nil {
			return chattool.Status{}, err
		}
		scopedTodo = &todo
		if milestoneRef != "" && todo.MilestoneRef != milestoneRef {
			return chattool.Status{}, fmt.Errorf("task %s belongs to milestone %q, not %q", todoRef, todo.MilestoneRef, milestoneRef)
		}
		milestoneRef = todo.MilestoneRef
	}
	var milestone planning.Milestone
	if milestoneRef != "" {
		var ok bool
		milestone, ok = milestoneByRef(plan, milestoneRef)
		if !ok {
			return chattool.Status{}, fmt.Errorf("milestone %q not found", milestoneRef)
		}
		if milestone.OwnerChatID != nil {
			return chattool.Status{}, fmt.Errorf("milestone %q is owned by chat %s", milestoneRef, *milestone.OwnerChatID)
		}
	}
	if role == chatrole.Execution && milestoneRef == "" {
		return chattool.Status{}, fmt.Errorf("execution chat requires milestone_key or task_key")
	}
	if role == chatrole.Execution && milestone.Status != planning.MilestoneStatusReady {
		return chattool.Status{}, fmt.Errorf("milestone %q is %s, expected ready", milestoneRef, milestone.Status)
	}
	parentID := parentChat.ID
	chatTitle := strings.TrimSpace(req.Title)
	if chatTitle == "" {
		chatTitle = defaultChildChatTitle(role, milestone, scopedTodo)
	}
	now := time.Now().UTC()
	chatRecord := domain.Chat{
		ID:                    id.New(),
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
	if _, err := c.session.AddPreparedChat(ctx, chatRecord); err != nil {
		return chattool.Status{}, err
	}
	if status := roleMilestoneStatus(role); status != 0 {
		nextPlan, err := updateMilestoneStatus(plan, milestoneRef, status, chatRecord.ID)
		if err != nil {
			return chattool.Status{}, err
		}
		plan, err = c.session.SetMilestonePlan(ctx, sessionID, nextPlan.Summary, nextPlan.Milestones)
		if err != nil {
			return chattool.Status{}, err
		}
		milestone, _ = milestoneByRef(plan, milestoneRef)
	}
	if todoRef != "" && role == chatrole.Execution && scopedTodo != nil && scopedTodo.Status == planning.TodoStatusPending {
		todo, err := c.session.UpdateTodoItem(ctx, todoRef, planning.TodoStatusInProgress, scopedTodo.Content, "Started execution chat.")
		if err != nil {
			return chattool.Status{}, err
		}
		scopedTodo = &todo
	}
	return c.session.startPreparedChat(ctx, chatRecord.ID, milestone, scopedTodo, role, objective)
}

func (c chatControl) UpdateChat(ctx context.Context, sessionID, ownerChatID, chatID id.ID, update chattool.UpdateRequest) (chattool.Status, error) {
	if err := c.session.requireSession(sessionID); err != nil {
		return chattool.Status{}, err
	}
	if ownerChatID == "" {
		ownerChatID = c.ownerChatID
	}
	snapshot := c.session.Snapshot()
	target, ok := chatByID(snapshot.Chats, chatID)
	if !ok {
		return chattool.Status{}, fmt.Errorf("chat %s not found", chatID)
	}
	if err := ensureChatOperationOwner(ownerChatID, target); err != nil {
		return chattool.Status{}, err
	}
	if strings.TrimSpace(update.Message) != "" && target.ID == ownerChatID {
		return chattool.Status{}, fmt.Errorf("chat_send cannot send a message to its own chat; target a direct child chat instead")
	}
	if strings.TrimSpace(update.Message) != "" || update.Interrupt {
		rt, err := c.session.Chat(ctx, chatID)
		if err != nil {
			return chattool.Status{}, err
		}
		if strings.TrimSpace(update.Message) != "" {
			kind := chatpkg.QueueKindUser
			if update.Steer {
				kind = chatpkg.QueueKindSteer
			}
			rt.Enqueue(chatpkg.QueueItem{Kind: kind, Source: domain.UserMessageSourceSubchat, Text: update.Message})
		}
		if update.Interrupt {
			reason := chatpkg.CancelReasonUserInterrupt
			if update.Hard {
				reason = chatpkg.CancelReasonUserInterruptHard
			}
			rt.Cancel(reason)
		}
	}
	if update.Archived == nil && strings.TrimSpace(update.Title) == "" {
		return c.session.ChatStatus(ctx, chatID)
	}
	status, _, err := c.session.UpdateChat(ctx, chatID, update)
	return status, err
}

func (s *Session) startPreparedChat(ctx context.Context, chatID id.ID, milestone planning.Milestone, scopedTodo *planning.TodoItem, role domain.WorkflowRole, objective string) (chattool.Status, error) {
	if chatID == "" {
		return chattool.Status{}, fmt.Errorf("chat id is required")
	}
	objective = strings.TrimSpace(objective)
	if objective == "" {
		return chattool.Status{}, fmt.Errorf("objective is required")
	}
	snapshot := s.Snapshot()
	activeChat, err := s.Chat(ctx, chatID)
	if err != nil {
		return chattool.Status{}, err
	}
	updates, unsub := activeChat.Subscribe()
	go s.consumeChatUpdates(chatID, updates, unsub)
	activeChat.Enqueue(chatpkg.QueueItem{Kind: chatpkg.QueueKindSteer, Source: domain.UserMessageSourceAutoGenerated, Text: s.bootstrapPrompt(ctx, snapshot.Session.ID, milestone, scopedTodo, role, objective)})
	chatSnapshot := activeChat.Snapshot()
	return chattool.Status{
		ID:                 chatSnapshot.Chat.ID,
		Title:              chatSnapshot.Chat.Title,
		Role:               chatSnapshot.Chat.WorkflowRole,
		Archived:           chatSnapshot.Chat.Archived,
		ActiveMilestoneRef: chatSnapshot.Chat.ActiveMilestoneRef,
		AssignedTodoRef:    chatSnapshot.Chat.AssignedTodoRef,
		State:              chattool.RunStateRunning,
		Status:             string(chattool.RunStateRunning),
		Busy:               true,
		StatusText:         "Started; bootstrap prompt queued",
	}, nil
}

func (s *Session) consumeChatUpdates(chatID id.ID, updates <-chan chatpkg.Update, unsub func()) {
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
				s.notifyParentChat(context.Background(), update.Snapshot.Chat, fmt.Sprintf("Chat %s is waiting for approval: %s", chatID, strings.TrimSpace(update.StatusText)))
			}
		case chatpkg.StatusErrored:
			if !notifiedIdle {
				notifiedIdle = true
				s.notifyParentChat(context.Background(), update.Snapshot.Chat, fmt.Sprintf("Chat %s failed: %s", chatID, strings.TrimSpace(update.StatusText)))
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
			s.notifyParentChat(context.Background(), update.Snapshot.Chat, s.childIdleNotification(context.Background(), update.Snapshot.Chat, chatID, statusText))
		}
	}
}

func (s *Session) notifyParentChat(ctx context.Context, source domain.Chat, text string) {
	if source.ParentChatID == nil || source.SessionID == "" || strings.TrimSpace(text) == "" {
		return
	}
	parent, err := s.Chat(ctx, *source.ParentChatID)
	if err != nil {
		return
	}
	parent.Enqueue(chatpkg.QueueItem{Kind: chatpkg.QueueKindSteer, Source: domain.UserMessageSourceSubchat, Text: strings.TrimSpace(text)})
}

func (s *Session) childIdleNotification(ctx context.Context, chatRecord domain.Chat, chatID id.ID, statusText string) string {
	if chatRecord.ID != "" {
		chatID = chatRecord.ID
	}
	text := fmt.Sprintf("Chat %s is now idle.", chatID)
	if chatRecord.ParentChatID == nil {
		return text
	}
	if chatRecord.AssignedTodoRef != "" {
		todos, err := s.ListTodos(ctx, chatRecord.SessionID, chatRecord.AssignedTodoBucketRef)
		if err == nil {
			for _, todo := range todos {
				if todo.ID == chatRecord.AssignedTodoRef || planning.TodoKey(todo) == string(chatRecord.AssignedTodoRef) {
					return fmt.Sprintf("%s Assigned task %s is %s.", text, planning.TodoKey(todo), todo.Status)
				}
			}
		}
	}
	if ref := strings.TrimSpace(chatRecord.ActiveMilestoneRef); ref != "" {
		todos, err := s.ListTodos(ctx, chatRecord.SessionID, ref)
		if err == nil && len(todos) > 0 {
			completed := 0
			for _, todo := range todos {
				if todo.Status == planning.TodoStatusCompleted {
					completed++
				}
			}
			if completed == len(todos) {
				return fmt.Sprintf("%s All %d tasks for milestone %s are done.", text, len(todos), ref)
			}
			return fmt.Sprintf("%s Chat completed %d out of %d tasks for milestone %s, but is now stopped.", text, completed, len(todos), ref)
		}
	}
	statusText = strings.TrimSpace(statusText)
	if statusText == "" || strings.EqualFold(statusText, "idle") {
		return text
	}
	return fmt.Sprintf("%s Last status: %s.", text, statusText)
}

func (s *Session) bootstrapPrompt(ctx context.Context, sessionID id.ID, milestone planning.Milestone, scopedTodo *planning.TodoItem, role domain.WorkflowRole, objective string) string {
	lines := []string{
		fmt.Sprintf("Profile: %s", role),
		"Objective:",
		strings.TrimSpace(objective),
	}
	if planning.MilestoneKey(milestone) != "" {
		todos, _ := s.ListTodos(ctx, sessionID, planning.MilestoneKey(milestone))
		if scopedTodo != nil {
			todos = []planning.TodoItem{*scopedTodo}
		}
		lines = append(lines,
			"",
			fmt.Sprintf("Milestone key: %s", planning.MilestoneKey(milestone)),
			fmt.Sprintf("Milestone title: %s", milestone.Title),
			fmt.Sprintf("Milestone status: %s", milestone.Status),
		)
		if scopedTodo != nil {
			lines = append(lines, fmt.Sprintf("Task scope: %s", planning.TodoKey(*scopedTodo)))
		}
		if notes := strings.TrimSpace(milestone.Notes); notes != "" {
			lines = append(lines, "Milestone notes:", notes)
		}
		if len(todos) == 0 {
			lines = append(lines, "Current tasks: none")
		} else {
			lines = append(lines, "Current tasks:")
			for _, item := range todos {
				lines = append(lines, fmt.Sprintf("- [%s] %s %s", item.Status, planning.TodoKey(item), item.Content))
			}
		}
	}
	switch role {
	case chatrole.Execution:
		lines = append(lines, "", "Execute only this milestone using its task list as the working queue.", "Update task statuses as you make progress and keep the milestone status accurate.", "When finished, set the milestone status to completed or blocked and then go idle.")
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func ensureChatOperationOwner(ownerChatID id.ID, target domain.Chat) error {
	if ownerChatID == "" {
		return fmt.Errorf("owner chat id is required")
	}
	if target.ID == ownerChatID {
		return nil
	}
	if target.ParentChatID != nil && *target.ParentChatID == ownerChatID {
		return nil
	}
	return fmt.Errorf("chat %s is not owned by chat %s", target.ID, ownerChatID)
}

func sessionTodoByID(ctx context.Context, owner interface {
	ListTodos(context.Context, id.ID, string) ([]planning.TodoItem, error)
}, sessionID id.ID, plan planning.Plan, todoID id.ID) (planning.TodoItem, error) {
	for _, milestone := range plan.Milestones {
		todos, err := owner.ListTodos(ctx, sessionID, planning.MilestoneKey(milestone))
		if err != nil {
			return planning.TodoItem{}, err
		}
		for _, todo := range todos {
			if todo.ID == todoID || planning.TodoKey(todo) == string(todoID) {
				return todo, nil
			}
		}
	}
	todos, err := owner.ListTodos(ctx, sessionID, "")
	if err != nil {
		return planning.TodoItem{}, err
	}
	for _, todo := range todos {
		if todo.ID == todoID || planning.TodoKey(todo) == string(todoID) {
			return todo, nil
		}
	}
	return planning.TodoItem{}, fmt.Errorf("task %s not found", todoID)
}

func updateMilestoneStatus(plan planning.Plan, ref string, status planning.MilestoneStatus, ownerChatID id.ID) (planning.Plan, error) {
	next := plan
	next.Milestones = slices.Clone(plan.Milestones)
	found := false
	for idx := range next.Milestones {
		if planning.MilestoneKey(next.Milestones[idx]) != ref {
			continue
		}
		found = true
		if next.Milestones[idx].OwnerChatID != nil && *next.Milestones[idx].OwnerChatID != ownerChatID {
			return planning.Plan{}, fmt.Errorf("milestone %q is owned by chat %s", ref, *next.Milestones[idx].OwnerChatID)
		}
		next.Milestones[idx].Status = status
		if status == planning.MilestoneStatusDecomposing || status == planning.MilestoneStatusExecuting {
			owner := ownerChatID
			next.Milestones[idx].OwnerChatID = &owner
		} else {
			next.Milestones[idx].OwnerChatID = nil
		}
	}
	if !found {
		return planning.Plan{}, fmt.Errorf("milestone %q not found", ref)
	}
	return next, nil
}

func roleMilestoneStatus(role domain.WorkflowRole) planning.MilestoneStatus {
	switch role {
	case chatrole.Execution:
		return planning.MilestoneStatusExecuting
	default:
		return 0
	}
}

func defaultChildChatTitle(role domain.WorkflowRole, milestone planning.Milestone, todo *planning.TodoItem) string {
	prefix := chatrole.DisplayName(role)
	if todo != nil {
		return fmt.Sprintf("%s: %s", prefix, todo.Content)
	}
	if strings.TrimSpace(milestone.Title) != "" {
		return fmt.Sprintf("%s: %s", prefix, milestone.Title)
	}
	return prefix
}

func milestoneByRef(plan planning.Plan, ref string) (planning.Milestone, bool) {
	for _, milestone := range plan.Milestones {
		if planning.MilestoneKey(milestone) == ref {
			return milestone, true
		}
	}
	return planning.Milestone{}, false
}
