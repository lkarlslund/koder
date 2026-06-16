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
	milestoneKey := strings.TrimSpace(req.MilestoneKey)
	var milestone planning.Milestone
	if milestoneKey != "" {
		var ok bool
		milestone, ok = milestoneByKey(plan, milestoneKey)
		if !ok {
			return chattool.Status{}, fmt.Errorf("milestone %q not found", milestoneKey)
		}
		if milestone.OwnerChatID != nil {
			return chattool.Status{}, fmt.Errorf("milestone %q is owned by chat %s; use chat_send to steer that child chat instead of starting another one", milestoneKey, *milestone.OwnerChatID)
		}
	}
	if role == chatrole.Execution && milestoneKey == "" {
		return chattool.Status{}, fmt.Errorf("execution chat requires milestone_key")
	}
	if role == chatrole.Execution && milestone.Status != planning.MilestoneStatusReady {
		return chattool.Status{}, fmt.Errorf("milestone %q is %s, expected ready", milestoneKey, milestone.Status)
	}
	if role == chatrole.Execution {
		if existing := directChildForMilestone(snapshot.Chats, parentChatID, milestoneKey); existing.ID != "" {
			return chattool.Status{}, fmt.Errorf("milestone %q already has child chat %s; use chat_send to steer it instead of starting another one", milestoneKey, existing.ID)
		}
	}
	if err := c.session.ensureCanStartChild(ctx, parentChatID, snapshot.Chats); err != nil {
		return chattool.Status{}, err
	}
	parentID := parentChat.ID
	chatTitle := strings.TrimSpace(req.Title)
	if chatTitle == "" {
		chatTitle = defaultChildChatTitle(role, milestone, nil)
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
		ActiveMilestoneKey:    milestoneKey,
		AssignedTaskBucketKey: milestoneKey,
		Position:              len(snapshot.Chats),
		CreatedAt:             now,
		UpdatedAt:             now,
	}
	if _, err := c.session.AddPreparedChat(ctx, chatRecord); err != nil {
		return chattool.Status{}, err
	}
	if status := roleMilestoneStatus(role); status != 0 {
		nextPlan, err := updateMilestoneStatus(plan, milestoneKey, status, chatRecord.ID)
		if err != nil {
			return chattool.Status{}, err
		}
		plan, err = c.session.SetMilestonePlan(ctx, sessionID, nextPlan.Summary, nextPlan.Milestones)
		if err != nil {
			return chattool.Status{}, err
		}
		milestone, _ = milestoneByKey(plan, milestoneKey)
	}
	return c.session.startPreparedChat(ctx, chatRecord.ID, milestone, nil, role, objective)
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

func (s *Session) startPreparedChat(ctx context.Context, chatID id.ID, milestone planning.Milestone, scopedTask *planning.Task, role domain.WorkflowRole, objective string) (chattool.Status, error) {
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
	activeChat.Enqueue(chatpkg.QueueItem{Kind: chatpkg.QueueKindSteer, Source: domain.UserMessageSourceAutoGenerated, Text: s.bootstrapPrompt(ctx, snapshot.Session.ID, milestone, scopedTask, role, objective)})
	chatSnapshot := waitForChatQueuedOrActive(ctx, activeChat)
	return chattool.Status{
		ID:                 chatSnapshot.Chat.ID,
		ParentChatID:       chatParentID(chatSnapshot.Chat),
		Title:              chatSnapshot.Chat.Title,
		Role:               chatSnapshot.Chat.WorkflowRole,
		Archived:           chatSnapshot.Chat.Archived,
		ActiveMilestoneKey: chatSnapshot.Chat.ActiveMilestoneKey,
		AssignedTaskRef:    chatSnapshot.Chat.AssignedTaskRef,
		State:              chattool.RunStateRunning,
		Status:             string(chattool.RunStateRunning),
		Busy:               true,
		StatusText:         "Started; bootstrap prompt queued",
	}, nil
}

func waitForChatQueuedOrActive(ctx context.Context, activeChat *chatpkg.Chat) chatpkg.Snapshot {
	if activeChat == nil {
		return chatpkg.Snapshot{}
	}
	deadline := time.NewTimer(100 * time.Millisecond)
	defer deadline.Stop()
	ticker := time.NewTicker(2 * time.Millisecond)
	defer ticker.Stop()
	for {
		snapshot := activeChat.Snapshot()
		if snapshot.Active || len(snapshot.QueuedInputs) > 0 {
			return snapshot
		}
		select {
		case <-ctx.Done():
			return snapshot
		case <-deadline.C:
			return snapshot
		case <-ticker.C:
		}
	}
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
	if chatRecord.AssignedTaskRef != "" {
		tasks, err := s.ListTasks(ctx, chatRecord.SessionID, chatRecord.AssignedTaskBucketKey)
		if err == nil {
			for _, task := range tasks {
				if planning.TaskKey(task) == chatRecord.AssignedTaskRef {
					return fmt.Sprintf("%s Assigned task %s is %s.", text, planning.TaskKey(task), task.Status)
				}
			}
		}
	}
	if ref := strings.TrimSpace(chatRecord.ActiveMilestoneKey); ref != "" {
		tasks, err := s.ListTasks(ctx, chatRecord.SessionID, ref)
		if err == nil && len(tasks) > 0 {
			completed := 0
			remaining := make([]string, 0)
			for _, task := range tasks {
				if task.Status == planning.TaskStatusCompleted {
					completed++
					continue
				}
				remaining = append(remaining, fmt.Sprintf("%s is %s", planning.TaskKey(task), task.Status))
			}
			if completed == len(tasks) {
				return fmt.Sprintf("%s All %d tasks for milestone %s are done.", text, len(tasks), ref)
			}
			return fmt.Sprintf("%s Chat completed %d out of %d tasks for milestone %s, but is now stopped. Remaining tasks: %s.", text, completed, len(tasks), ref, strings.Join(remaining, ", "))
		}
	}
	statusText = strings.TrimSpace(statusText)
	if statusText == "" || strings.EqualFold(statusText, "idle") {
		return text
	}
	return fmt.Sprintf("%s Last status: %s.", text, statusText)
}

func (s *Session) bootstrapPrompt(ctx context.Context, sessionID id.ID, milestone planning.Milestone, scopedTask *planning.Task, role domain.WorkflowRole, objective string) string {
	lines := []string{
		fmt.Sprintf("Profile: %s", role),
		"Objective:",
		strings.TrimSpace(objective),
	}
	if planning.MilestoneKey(milestone) != "" {
		tasks, _ := s.ListTasks(ctx, sessionID, planning.MilestoneKey(milestone))
		if scopedTask != nil {
			tasks = []planning.Task{*scopedTask}
		}
		lines = append(lines,
			"",
			fmt.Sprintf("Milestone key: %s", planning.MilestoneKey(milestone)),
			fmt.Sprintf("Milestone title: %s", milestone.Title),
			fmt.Sprintf("Milestone status: %s", milestone.Status),
		)
		if scopedTask != nil {
			lines = append(lines, fmt.Sprintf("Task scope: %s", planning.TaskKey(*scopedTask)))
		}
		if notes := strings.TrimSpace(milestone.Notes); notes != "" {
			lines = append(lines, "Milestone notes:", notes)
		}
		if len(tasks) == 0 {
			lines = append(lines, "Current tasks: none")
		} else {
			lines = append(lines, "Current tasks:")
			for _, item := range tasks {
				lines = append(lines, fmt.Sprintf("- [%s] %s %s", item.Status, planning.TaskKey(item), item.Content))
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

func (s *Session) ensureCanStartChild(ctx context.Context, parentChatID id.ID, chats []domain.Chat) error {
	limit := s.maxChildChats()
	active := make([]chattool.Status, 0)
	for _, chatRecord := range chats {
		if chatRecord.Archived || chatRecord.ParentChatID == nil || *chatRecord.ParentChatID != parentChatID {
			continue
		}
		status, err := s.ChatStatus(ctx, chatRecord.ID)
		if err != nil {
			return err
		}
		if childChatCountsAgainstLimit(status) {
			active = append(active, status)
		}
	}
	if len(active) < limit {
		return nil
	}
	parts := make([]string, 0, len(active))
	for _, status := range active {
		ref := strings.TrimSpace(status.ActiveMilestoneKey)
		if ref == "" {
			ref = strings.TrimSpace(status.Title)
		}
		if ref == "" {
			ref = string(status.ID)
		}
		parts = append(parts, fmt.Sprintf("%s (%s)", status.ID, ref))
	}
	return fmt.Errorf("cannot start child chat: %d non-idle child chat(s) already active, limit is %d; use chat_send to steer existing child chat(s): %s", len(active), limit, strings.Join(parts, ", "))
}

func (s *Session) maxChildChats() int {
	if s == nil {
		return 1
	}
	s.mu.RLock()
	limit := s.config.MaxChildChats
	s.mu.RUnlock()
	if limit <= 0 {
		return 1
	}
	return limit
}

func childChatCountsAgainstLimit(status chattool.Status) bool {
	return status.State != chattool.RunStateIdle || status.Busy || status.QueuedInputs > 0 || status.PendingApprovals > 0
}

func directChildForMilestone(chats []domain.Chat, parentChatID id.ID, milestoneKey string) domain.Chat {
	milestoneKey = strings.TrimSpace(milestoneKey)
	if milestoneKey == "" {
		return domain.Chat{}
	}
	for _, chatRecord := range chats {
		if chatRecord.Archived || chatRecord.ParentChatID == nil || *chatRecord.ParentChatID != parentChatID {
			continue
		}
		if strings.TrimSpace(chatRecord.ActiveMilestoneKey) == milestoneKey {
			return chatRecord
		}
	}
	return domain.Chat{}
}

func chatParentID(chatRecord domain.Chat) id.ID {
	if chatRecord.ParentChatID == nil {
		return ""
	}
	return *chatRecord.ParentChatID
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

func defaultChildChatTitle(role domain.WorkflowRole, milestone planning.Milestone, task *planning.Task) string {
	prefix := chatrole.DisplayName(role)
	if task != nil {
		return fmt.Sprintf("%s: %s", prefix, task.Content)
	}
	if strings.TrimSpace(milestone.Title) != "" {
		return fmt.Sprintf("%s: %s", prefix, milestone.Title)
	}
	return prefix
}

func milestoneByKey(plan planning.Plan, ref string) (planning.Milestone, bool) {
	for _, milestone := range plan.Milestones {
		if planning.MilestoneKey(milestone) == ref {
			return milestone, true
		}
	}
	return planning.Milestone{}, false
}
