package store

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/lkarlslund/koder/internal/chatrole"
	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/planning"
	"github.com/lkarlslund/koder/internal/store/driver"
	"github.com/lkarlslund/koder/internal/store/driver/jsonfsdriver"
	"github.com/lkarlslund/koder/internal/store/driver/pebbledriver"
)

const (
	BackendPebble = "pebble"
	BackendJSONFS = "jsonfs"
)

type Options struct {
	Backend string
}

type Store struct {
	backend    driver.Backend
	toolCallMu sync.Mutex
}

type Approval struct {
	ID         domain.ID
	SessionID  domain.ID
	ChatID     domain.ID
	Tool       domain.ToolKind
	ToolCallID string
	Command    string
	Status     domain.ApprovalStatus
	CreatedAt  time.Time
}

type Task struct {
	ID        domain.ID
	SessionID domain.ID
	Body      string
	Status    domain.TaskStatus
	CreatedAt time.Time
}

type MilestonePlan = planning.Plan
type Milestone = planning.Milestone
type TodoItem = planning.TodoItem

type RuntimeState struct {
	ID          string    `json:"id"`
	LastWebBind string    `json:"last_web_bind"`
	UpdatedAt   time.Time `json:"updated_at"`
}

func cloneToolStates(src map[domain.ToolKind]bool) map[domain.ToolKind]bool {
	if len(src) == 0 {
		return map[domain.ToolKind]bool{}
	}
	dst := make(map[domain.ToolKind]bool, len(src))
	for kind, enabled := range src {
		dst[kind] = enabled
	}
	return dst
}

func cloneQueuedInputs(src []domain.QueuedInput) []domain.QueuedInput {
	if len(src) == 0 {
		return nil
	}
	dst := make([]domain.QueuedInput, 0, len(src))
	for _, item := range src {
		cloned := item
		cloned.Attachments = append([]domain.QueuedAttachment(nil), item.Attachments...)
		cloned.References = append([]domain.QueuedReference(nil), item.References...)
		dst = append(dst, cloned)
	}
	return dst
}

func appendPermissionRule(rules []domain.PermissionOverride, rule domain.PermissionOverride) []domain.PermissionOverride {
	rule.Pattern = strings.TrimSpace(rule.Pattern)
	if rule.Pattern == "" {
		rule.Pattern = "*"
	}
	next := make([]domain.PermissionOverride, 0, len(rules)+1)
	for _, existing := range rules {
		if existing.Tool == rule.Tool && strings.TrimSpace(existing.Pattern) == rule.Pattern {
			continue
		}
		next = append(next, existing)
	}
	return append(next, rule)
}

func Open(stateDir string) (*Store, error) {
	return OpenWithOptions(stateDir, Options{Backend: BackendPebble})
}

func OpenWithOptions(stateDir string, opts Options) (*Store, error) {
	backendName := opts.Backend
	if backendName == "" {
		backendName = BackendPebble
	}

	var impl driver.Backend
	var err error
	switch backendName {
	case BackendPebble:
		impl, err = pebbledriver.Open(stateDir)
	case BackendJSONFS:
		impl, err = jsonfsdriver.Open(stateDir)
	default:
		return nil, fmt.Errorf("unsupported store backend %q", backendName)
	}
	if err != nil {
		return nil, err
	}
	return &Store{backend: impl}, nil
}

func (s *Store) Close() error {
	return s.backend.Close()
}

func (s *Store) EnsureSession(ctx context.Context, providerID, modelID string) (domain.Session, error) {
	sessions, err := s.ListSessions(ctx)
	if err != nil {
		return domain.Session{}, err
	}
	if len(sessions) > 0 {
		return sessions[0], nil
	}
	return s.CreateSession(ctx, "New Session", providerID, modelID, nil)
}

func (s *Store) CreateSession(ctx context.Context, title, providerID, modelID string, parentID *domain.ID) (domain.Session, error) {
	now := time.Now().UTC()
	session := domain.Session{
		ID:                domain.NewIDAt(now),
		ParentID:          parentID,
		Title:             title,
		PermissionProfile: "",
		PermissionRules:   nil,
		ToolStates:        map[domain.ToolKind]bool{},
		CreatedAt:         now,
		UpdatedAt:         now,
	}
	chat := domain.Chat{
		ID:                domain.NewIDAt(now),
		SessionID:         session.ID,
		Title:             "Main",
		WorkflowRole:      chatrole.Orchestrator,
		ProviderID:        providerID,
		ModelID:           modelID,
		PermissionProfile: session.PermissionProfile,
		ToolStates:        map[domain.ToolKind]bool{},
		CreatedAt:         now,
		UpdatedAt:         now,
	}
	if err := s.Transaction(ctx, func(tx *Tx) error {
		if err := s.Sessions().PutTx(tx, ctx, session); err != nil {
			return err
		}
		return s.Chats().PutTx(tx, ctx, chat)
	}); err != nil {
		return domain.Session{}, err
	}
	return session, nil
}

func (s *Store) CreateChat(ctx context.Context, sessionID domain.ID, title string, role domain.WorkflowRole, parentChatID *domain.ID) (domain.Chat, error) {
	session, err := s.GetSession(ctx, sessionID)
	if err != nil {
		return domain.Chat{}, err
	}
	providerID := ""
	modelID := ""
	if parentChatID != nil && *parentChatID != "" {
		parent, err := s.GetChat(ctx, *parentChatID)
		if err != nil {
			return domain.Chat{}, err
		}
		if parent.SessionID != sessionID {
			return domain.Chat{}, fmt.Errorf("parent chat %s belongs to session %s, not %s", parent.ID, parent.SessionID, sessionID)
		}
		providerID = parent.ProviderID
		modelID = parent.ModelID
	} else if existing, err := s.ListChats(ctx, sessionID); err == nil && len(existing) > 0 {
		providerID = existing[0].ProviderID
		modelID = existing[0].ModelID
	}
	existing, err := s.ListChats(ctx, sessionID)
	if err != nil {
		return domain.Chat{}, err
	}
	if parentChatID == nil || *parentChatID == "" {
		if len(existing) == 0 {
			role = chatrole.Orchestrator
		}
	}
	now := time.Now().UTC()
	chat := domain.Chat{
		ID:                domain.NewIDAt(now),
		SessionID:         sessionID,
		ParentChatID:      parentChatID,
		Title:             title,
		WorkflowRole:      role,
		ProviderID:        providerID,
		ModelID:           modelID,
		PermissionProfile: session.PermissionProfile,
		ToolStates:        cloneToolStates(session.ToolStates),
		CreatedAt:         now,
		UpdatedAt:         now,
		Position:          len(existing),
	}
	if err := s.Chats().Put(ctx, chat); err != nil {
		return domain.Chat{}, err
	}
	return chat, nil
}

func (s *Store) ListSessions(ctx context.Context) ([]domain.Session, error) {
	sessions, err := s.Sessions().List(ctx, All[domain.Session]())
	if err != nil {
		return nil, err
	}
	slices.SortFunc(sessions, func(a, b domain.Session) int {
		switch {
		case a.UpdatedAt.After(b.UpdatedAt):
			return -1
		case a.UpdatedAt.Before(b.UpdatedAt):
			return 1
		case a.ID < b.ID:
			return -1
		case a.ID > b.ID:
			return 1
		default:
			return 0
		}
	})
	return sessions, nil
}

func (s *Store) GetSession(ctx context.Context, sessionID domain.ID) (domain.Session, error) {
	return s.Sessions().Get(ctx, sessionID)
}

// TouchSession marks a session as recently used and returns the updated record.
func (s *Store) TouchSession(ctx context.Context, sessionID domain.ID) (domain.Session, error) {
	session, err := s.GetSession(ctx, sessionID)
	if err != nil {
		return domain.Session{}, err
	}
	session.UpdatedAt = time.Now().UTC()
	if err := s.Sessions().Put(ctx, session); err != nil {
		return domain.Session{}, err
	}
	return session, nil
}

// DeleteSession removes a session and all session-owned persisted data.
func (s *Store) DeleteSession(ctx context.Context, sessionID domain.ID) error {
	if sessionID == "" {
		return fmt.Errorf("delete session: session id is required")
	}
	chats, err := s.ListChats(ctx, sessionID)
	if err != nil {
		return err
	}
	for _, chat := range chats {
		timeline, err := s.TimelineForChat(ctx, chat.ID)
		if err != nil {
			return err
		}
		for _, item := range timeline {
			if err := s.Timeline().Delete(ctx, item.ID); err != nil {
				return err
			}
		}
		approvals, err := s.Approvals().List(ctx, ByIndex[Approval]("chat", fmt.Sprint(chat.ID)))
		if err != nil {
			return err
		}
		for _, approval := range approvals {
			if err := s.Approvals().Delete(ctx, approval.ID); err != nil {
				return err
			}
		}
	}
	if tasks, err := s.ListTasks(ctx, sessionID); err != nil {
		return err
	} else {
		for _, task := range tasks {
			if err := s.Tasks().Delete(ctx, task.ID); err != nil {
				return err
			}
		}
	}
	if todos, err := s.ListTodos(ctx, sessionID, ""); err != nil {
		return err
	} else {
		for _, todo := range todos {
			if err := s.Todos().Delete(ctx, todo.ID); err != nil {
				return err
			}
		}
	}
	_ = s.MilestonePlans().Delete(ctx, sessionID)
	for _, chat := range chats {
		if err := s.Chats().Delete(ctx, chat.ID); err != nil {
			return err
		}
	}
	return s.Sessions().Delete(ctx, sessionID)
}

func (s *Store) ListChats(ctx context.Context, sessionID domain.ID) ([]domain.Chat, error) {
	chats, err := s.Chats().List(ctx, ByIndex[domain.Chat]("session", fmt.Sprint(sessionID)))
	if err != nil {
		return nil, err
	}
	sortChatsForSidebar(chats)
	return chats, nil
}

func sortChatsForSidebar(chats []domain.Chat) {
	slices.SortFunc(chats, func(a, b domain.Chat) int {
		switch {
		case a.Position < b.Position:
			return -1
		case a.Position > b.Position:
			return 1
		case a.CreatedAt.Before(b.CreatedAt):
			return -1
		case a.CreatedAt.After(b.CreatedAt):
			return 1
		case a.ID < b.ID:
			return -1
		case a.ID > b.ID:
			return 1
		default:
			return 0
		}
	})
}

func (s *Store) GetChat(ctx context.Context, chatID domain.ID) (domain.Chat, error) {
	return s.Chats().Get(ctx, chatID)
}

func (s *Store) DefaultChat(ctx context.Context, sessionID domain.ID) (domain.Chat, error) {
	chats, err := s.ListChats(ctx, sessionID)
	if err != nil {
		return domain.Chat{}, err
	}
	for _, chat := range chats {
		if chat.ParentChatID == nil {
			return chat, nil
		}
	}
	if len(chats) > 0 {
		return chats[0], nil
	}
	return domain.Chat{}, fmt.Errorf("session %s has no chats", sessionID)
}

func (s *Store) PutChat(ctx context.Context, chat domain.Chat) error {
	if chat.ID == "" {
		return fmt.Errorf("put chat: id is required")
	}
	if chat.SessionID == "" {
		return fmt.Errorf("put chat: session id is required")
	}
	return s.Chats().Put(ctx, chat)
}

func (s *Store) UpdateChat(ctx context.Context, chat domain.Chat) error {
	existing, err := s.GetChat(ctx, chat.ID)
	if err != nil {
		return err
	}
	if chat.Position == 0 && existing.Position != 0 && chat.UpdatedAt.After(existing.UpdatedAt) {
		chat.Position = existing.Position
	}
	return s.Chats().Put(ctx, chat)
}

// SetChatModel persists the provider/model used by one chat.
func (s *Store) SetChatModel(ctx context.Context, chatID domain.ID, providerID, modelID string) error {
	providerID = strings.TrimSpace(providerID)
	modelID = strings.TrimSpace(modelID)
	if chatID == "" {
		return fmt.Errorf("set chat model: chat id is required")
	}
	if providerID == "" {
		return fmt.Errorf("set chat model: provider id is required")
	}
	if modelID == "" {
		return fmt.Errorf("set chat model: model id is required")
	}
	chat, err := s.GetChat(ctx, chatID)
	if err != nil {
		return err
	}
	chat.ProviderID = providerID
	chat.ModelID = modelID
	chat.UpdatedAt = time.Now().UTC()
	return s.Chats().Put(ctx, chat)
}

// ReorderChats persists the complete sidebar order for a session.
func (s *Store) ReorderChats(ctx context.Context, sessionID domain.ID, orderedIDs []domain.ID) ([]domain.Chat, error) {
	if sessionID == "" {
		return nil, fmt.Errorf("reorder chats: session id is required")
	}
	chats, err := s.ListChats(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	if len(orderedIDs) != len(chats) {
		return nil, fmt.Errorf("reorder chats: expected %d chat ids, got %d", len(chats), len(orderedIDs))
	}
	byID := make(map[domain.ID]domain.Chat, len(chats))
	for _, chat := range chats {
		byID[chat.ID] = chat
	}
	seen := make(map[domain.ID]bool, len(orderedIDs))
	ordered := make([]domain.Chat, 0, len(orderedIDs))
	for idx, chatID := range orderedIDs {
		if chatID == "" {
			return nil, fmt.Errorf("reorder chats: empty chat id at position %d", idx)
		}
		if seen[chatID] {
			return nil, fmt.Errorf("reorder chats: duplicate chat id %s", chatID)
		}
		chat, ok := byID[chatID]
		if !ok {
			return nil, fmt.Errorf("reorder chats: chat %s not found in session %s", chatID, sessionID)
		}
		seen[chatID] = true
		chat.Position = idx
		ordered = append(ordered, chat)
	}
	for _, chat := range ordered {
		if err := s.UpdateChat(ctx, chat); err != nil {
			return nil, err
		}
	}
	return ordered, nil
}

// DeleteChat removes a chat and its direct chat-owned persisted data.
func (s *Store) DeleteChat(ctx context.Context, chatID domain.ID) error {
	if chatID == "" {
		return fmt.Errorf("delete chat: chat id is required")
	}
	chat, err := s.GetChat(ctx, chatID)
	if err != nil {
		return err
	}
	chats, err := s.ListChats(ctx, chat.SessionID)
	if err != nil {
		return err
	}
	if len(chats) <= 1 {
		return fmt.Errorf("cannot delete the only chat in a session")
	}
	timeline, err := s.TimelineForChat(ctx, chatID)
	if err != nil {
		return err
	}
	for _, item := range timeline {
		if err := s.Timeline().Delete(ctx, item.ID); err != nil {
			return err
		}
	}
	approvals, err := s.Approvals().List(ctx, ByIndex[Approval]("chat", fmt.Sprint(chatID)))
	if err != nil {
		return err
	}
	for _, approval := range approvals {
		if err := s.Approvals().Delete(ctx, approval.ID); err != nil {
			return err
		}
	}
	return s.Chats().Delete(ctx, chatID)
}

func (s *Store) SetChatQueuedInputs(ctx context.Context, chatID domain.ID, items []domain.QueuedInput) error {
	chat, err := s.GetChat(ctx, chatID)
	if err != nil {
		return err
	}
	chat.QueuedInputs = cloneQueuedInputs(items)
	chat.UpdatedAt = time.Now().UTC()
	return s.Chats().Put(ctx, chat)
}

func (s *Store) SetSessionProjectRoot(ctx context.Context, sessionID domain.ID, projectRoot string) error {
	return s.updateSession(ctx, sessionID, func(session *domain.Session) {
		session.ProjectRoot = projectRoot
	})
}

func (s *Store) SetSessionPermissionProfile(ctx context.Context, sessionID domain.ID, profile string) error {
	return s.updateSession(ctx, sessionID, func(session *domain.Session) {
		session.PermissionProfile = strings.TrimSpace(profile)
	})
}

func (s *Store) AddSessionPermissionRule(ctx context.Context, sessionID domain.ID, rule domain.PermissionOverride) error {
	return s.updateSession(ctx, sessionID, func(session *domain.Session) {
		session.PermissionRules = appendPermissionRule(session.PermissionRules, rule)
	})
}

func (s *Store) SetSessionToolStates(ctx context.Context, sessionID domain.ID, states map[domain.ToolKind]bool) error {
	return s.updateSession(ctx, sessionID, func(session *domain.Session) {
		session.ToolStates = cloneToolStates(states)
	})
}

func (s *Store) UpdateSessionTitle(ctx context.Context, sessionID domain.ID, title string, generatedAt time.Time, refreshCount int) error {
	return s.updateSession(ctx, sessionID, func(session *domain.Session) {
		session.Title = strings.TrimSpace(title)
		session.TitleGeneratedAt = generatedAt
		session.TitleRefreshCount = refreshCount
	})
}

func (s *Store) UpdateSessionAgents(
	ctx context.Context,
	sessionID domain.ID,
	projectRoot string,
	projectChecksum string,
	resolved string,
	summary string,
	files []domain.AgentsFile,
	generatedAt time.Time,
) error {
	return s.updateSession(ctx, sessionID, func(session *domain.Session) {
		session.ProjectRoot = projectRoot
		session.ProjectChecksum = projectChecksum
		session.AgentsResolved = resolved
		session.AgentsSummary = summary
		session.AgentsFiles = append([]domain.AgentsFile(nil), files...)
		session.AgentsGeneratedAt = generatedAt
	})
}

// TimelineForChat returns persisted timeline items for a chat ordered by sequence.
func (s *Store) TimelineForChat(ctx context.Context, chatID domain.ID) ([]domain.TimelineItem, error) {
	items, err := s.Timeline().List(ctx, ByIndex[domain.TimelineItem]("chat", fmt.Sprint(chatID)))
	if err != nil {
		return nil, err
	}
	slices.SortFunc(items, func(a, b domain.TimelineItem) int {
		switch {
		case a.Seq < b.Seq:
			return -1
		case a.Seq > b.Seq:
			return 1
		case a.ID < b.ID:
			return -1
		case a.ID > b.ID:
			return 1
		default:
			return 0
		}
	})
	return items, nil
}

// PutTimelineItem upserts one timeline item.
func (s *Store) PutTimelineItem(ctx context.Context, item domain.TimelineItem) error {
	return s.Timeline().Put(ctx, item)
}

// InsertTimelineItem inserts one timeline item.
func (s *Store) InsertTimelineItem(ctx context.Context, item domain.TimelineItem) (domain.TimelineItem, error) {
	return s.Timeline().Insert(ctx, item)
}

func (s *Store) CreateApproval(ctx context.Context, sessionID domain.ID, tool domain.ToolKind, command string) (Approval, error) {
	now := time.Now().UTC()
	approval := Approval{
		ID:        domain.NewIDAt(now),
		SessionID: sessionID,
		Tool:      tool,
		Command:   command,
		Status:    domain.ApprovalStatusPending,
		CreatedAt: now,
	}
	if err := s.Approvals().Put(ctx, approval); err != nil {
		return Approval{}, err
	}
	return approval, nil
}

func (s *Store) CreateChatApproval(ctx context.Context, chatID domain.ID, tool domain.ToolKind, command string) (Approval, error) {
	chat, err := s.GetChat(ctx, chatID)
	if err != nil {
		return Approval{}, err
	}
	now := time.Now().UTC()
	approval := Approval{
		ID:        domain.NewIDAt(now),
		SessionID: chat.SessionID,
		ChatID:    chatID,
		Tool:      tool,
		Command:   command,
		Status:    domain.ApprovalStatusPending,
		CreatedAt: now,
	}
	if err := s.Approvals().Put(ctx, approval); err != nil {
		return Approval{}, err
	}
	return approval, nil
}

func (s *Store) UpdateApproval(ctx context.Context, approvalID domain.ID, status domain.ApprovalStatus) error {
	approval, err := s.Approvals().Get(ctx, approvalID)
	if err != nil {
		return err
	}
	approval.Status = status
	return s.Approvals().Put(ctx, approval)
}

func (s *Store) PendingApprovals(ctx context.Context, sessionID domain.ID) ([]Approval, error) {
	chats, err := s.ListChats(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	var approvals []Approval
	for _, chat := range chats {
		next, err := s.PendingApprovalsForChat(ctx, chat.ID)
		if err != nil {
			return nil, err
		}
		approvals = append(approvals, next...)
	}
	return approvals, nil
}

func (s *Store) PendingApprovalsForChat(ctx context.Context, chatID domain.ID) ([]Approval, error) {
	chat, err := s.GetChat(ctx, chatID)
	if err != nil {
		return nil, nil
	}
	items, err := s.TimelineForChat(ctx, chatID)
	if err != nil {
		return nil, err
	}
	var approvals []Approval
	for _, item := range items {
		assistant, ok := item.Content.(domain.AssistantMessage)
		if !ok {
			continue
		}
		for _, call := range assistant.Tools {
			if call.Status != domain.ToolStatusAwaitingApproval {
				continue
			}
			approvals = append(approvals, Approval{
				ID:         SyntheticApprovalID(string(call.ToolCallID)),
				SessionID:  chat.SessionID,
				ChatID:     chatID,
				Tool:       call.Tool,
				ToolCallID: string(call.ToolCallID),
				Command:    toolCallPreview(call),
				Status:     domain.ApprovalStatusPending,
				CreatedAt:  item.UpdatedAt,
			})
		}
	}
	return approvals, nil
}

func SyntheticApprovalID(toolCallID string) domain.ID {
	return strings.TrimSpace(toolCallID)
}

func toolCallPreview(call domain.ToolCall) string {
	if command := strings.TrimSpace(call.Args["command"]); command != "" {
		return command
	}
	if path := strings.TrimSpace(call.Args["path"]); path != "" {
		return path
	}
	if pattern := strings.TrimSpace(call.Args["pattern"]); pattern != "" {
		return pattern
	}
	return strings.TrimSpace(string(call.Tool))
}

func (s *Store) AddTask(ctx context.Context, sessionID domain.ID, body string, status domain.TaskStatus) (Task, error) {
	now := time.Now().UTC()
	task := Task{
		ID:        domain.NewIDAt(now),
		SessionID: sessionID,
		Body:      strings.TrimSpace(body),
		Status:    status,
		CreatedAt: now,
	}
	if err := s.Tasks().Put(ctx, task); err != nil {
		return Task{}, err
	}
	return task, nil
}

func (s *Store) PutTask(ctx context.Context, task Task) error {
	if task.ID == "" {
		return fmt.Errorf("put task: id is required")
	}
	if task.SessionID == "" {
		return fmt.Errorf("put task: session id is required")
	}
	return s.Tasks().Put(ctx, task)
}

func (s *Store) UpdateTask(ctx context.Context, taskID domain.ID, status domain.TaskStatus) error {
	task, err := s.Tasks().Get(ctx, taskID)
	if err != nil {
		return err
	}
	task.Status = status
	return s.Tasks().Put(ctx, task)
}

func (s *Store) ListTasks(ctx context.Context, sessionID domain.ID) ([]Task, error) {
	items, err := s.Tasks().List(ctx, ByIndex[Task]("session", fmt.Sprint(sessionID)))
	if err != nil {
		return nil, err
	}
	slices.SortFunc(items, func(a, b Task) int {
		switch {
		case a.CreatedAt.Before(b.CreatedAt):
			return -1
		case a.CreatedAt.After(b.CreatedAt):
			return 1
		case a.ID < b.ID:
			return -1
		case a.ID > b.ID:
			return 1
		default:
			return 0
		}
	})
	return items, nil
}

func (s *Store) SetMilestonePlan(ctx context.Context, sessionID domain.ID, summary string, milestones []Milestone) (MilestonePlan, error) {
	plan := MilestonePlan{
		SessionID:  sessionID,
		Summary:    strings.TrimSpace(summary),
		Milestones: append([]Milestone(nil), milestones...),
		UpdatedAt:  time.Now().UTC(),
	}
	if err := s.PutMilestonePlan(ctx, plan); err != nil {
		return MilestonePlan{}, err
	}
	return plan, nil
}

func (s *Store) PutMilestonePlan(ctx context.Context, plan MilestonePlan) error {
	if plan.SessionID == "" {
		return fmt.Errorf("put milestone plan: session id is required")
	}
	if plan.UpdatedAt.IsZero() {
		plan.UpdatedAt = time.Now().UTC()
	}
	return s.MilestonePlans().Put(ctx, plan)
}

func (s *Store) GetMilestonePlan(ctx context.Context, sessionID domain.ID) (MilestonePlan, error) {
	plan, err := s.MilestonePlans().Get(ctx, sessionID)
	if err != nil {
		return MilestonePlan{SessionID: sessionID}, nil
	}
	return plan, nil
}

func (s *Store) AddTodoItems(ctx context.Context, sessionID domain.ID, milestoneRef string, contents []string) ([]TodoItem, error) {
	now := time.Now().UTC()
	milestoneRef = strings.TrimSpace(milestoneRef)
	existing, err := s.ListTodos(ctx, sessionID, milestoneRef)
	if err != nil {
		return nil, err
	}
	items := make([]TodoItem, 0, len(contents))
	for _, content := range contents {
		content = strings.TrimSpace(content)
		if content == "" {
			continue
		}
		items = append(items, TodoItem{
			ID:           domain.NewIDAt(now),
			SessionID:    sessionID,
			MilestoneRef: milestoneRef,
			Content:      content,
			Status:       domain.TodoStatusPending,
			Position:     len(existing) + len(items),
			CreatedAt:    now,
			UpdatedAt:    now,
		})
	}
	for _, item := range items {
		if err := s.Todos().Put(ctx, item); err != nil {
			return nil, err
		}
	}
	return items, nil
}

func (s *Store) PutTodoItem(ctx context.Context, item TodoItem) error {
	if item.ID == "" {
		return fmt.Errorf("put todo item: id is required")
	}
	if item.SessionID == "" {
		return fmt.Errorf("put todo item: session id is required")
	}
	if item.UpdatedAt.IsZero() {
		item.UpdatedAt = time.Now().UTC()
	}
	return s.Todos().Put(ctx, item)
}

func (s *Store) UpdateTodoItem(ctx context.Context, todoID domain.ID, status domain.TodoStatus, content string) (TodoItem, error) {
	item, err := s.Todos().Get(ctx, todoID)
	if err != nil {
		return TodoItem{}, err
	}
	item.Status = status
	if strings.TrimSpace(content) != "" {
		item.Content = strings.TrimSpace(content)
	}
	item.UpdatedAt = time.Now().UTC()
	if err := s.Todos().Put(ctx, item); err != nil {
		return TodoItem{}, err
	}
	return item, nil
}

func (s *Store) ListTodos(ctx context.Context, sessionID domain.ID, milestoneRef string) ([]TodoItem, error) {
	query := ByIndex[TodoItem]("session", fmt.Sprint(sessionID))
	milestoneRef = strings.TrimSpace(milestoneRef)
	if milestoneRef != "" {
		query = ByIndex[TodoItem]("milestone", fmt.Sprint(sessionID)+"/"+milestoneRef)
	}
	items, err := s.Todos().List(ctx, query)
	if err != nil {
		return nil, err
	}
	slices.SortFunc(items, func(a, b TodoItem) int {
		switch {
		case a.Position < b.Position:
			return -1
		case a.Position > b.Position:
			return 1
		case a.CreatedAt.Before(b.CreatedAt):
			return -1
		case a.CreatedAt.After(b.CreatedAt):
			return 1
		case a.ID < b.ID:
			return -1
		case a.ID > b.ID:
			return 1
		default:
			return 0
		}
	})
	return items, nil
}

func (s *Store) GetApproval(ctx context.Context, approvalID domain.ID) (Approval, error) {
	return s.Approvals().Get(ctx, approvalID)
}

func (s *Store) updateSession(ctx context.Context, sessionID domain.ID, update func(*domain.Session)) error {
	session, err := s.GetSession(ctx, sessionID)
	if err != nil {
		return err
	}
	update(&session)
	session.UpdatedAt = time.Now().UTC()
	return s.Sessions().Put(ctx, session)
}

func (s *Store) ForkSession(ctx context.Context, sourceSessionID domain.ID) (domain.Session, error) {
	source, err := s.GetSession(ctx, sourceSessionID)
	if err != nil {
		return domain.Session{}, err
	}
	sourceChat, err := s.DefaultChat(ctx, sourceSessionID)
	if err != nil {
		return domain.Session{}, err
	}
	forked, err := s.CreateSession(ctx, source.Title, sourceChat.ProviderID, sourceChat.ModelID, &source.ID)
	if err != nil {
		return domain.Session{}, err
	}
	if err := s.SetSessionProjectRoot(ctx, forked.ID, source.ProjectRoot); err != nil {
		return domain.Session{}, err
	}
	if source.PermissionProfile != "" {
		if err := s.SetSessionPermissionProfile(ctx, forked.ID, source.PermissionProfile); err != nil {
			return domain.Session{}, err
		}
	}
	if len(source.ToolStates) != 0 {
		if err := s.SetSessionToolStates(ctx, forked.ID, source.ToolStates); err != nil {
			return domain.Session{}, err
		}
	}
	plan, err := s.GetMilestonePlan(ctx, sourceSessionID)
	if err != nil {
		return domain.Session{}, err
	}
	if len(plan.Milestones) > 0 || strings.TrimSpace(plan.Summary) != "" {
		if _, err := s.SetMilestonePlan(ctx, forked.ID, plan.Summary, plan.Milestones); err != nil {
			return domain.Session{}, err
		}
		for _, milestone := range plan.Milestones {
			todos, err := s.ListTodos(ctx, sourceSessionID, milestone.Ref)
			if err != nil {
				return domain.Session{}, err
			}
			if len(todos) == 0 {
				continue
			}
			contents := make([]string, 0, len(todos))
			for _, todo := range todos {
				contents = append(contents, todo.Content)
			}
			created, err := s.AddTodoItems(ctx, forked.ID, milestone.Ref, contents)
			if err != nil {
				return domain.Session{}, err
			}
			for idx, todo := range todos {
				if idx >= len(created) {
					break
				}
				if _, err := s.UpdateTodoItem(ctx, created[idx].ID, todo.Status, todo.Content); err != nil {
					return domain.Session{}, err
				}
			}
		}
	}
	if err := s.ForkTimeline(ctx, sourceSessionID, forked.ID); err != nil {
		return domain.Session{}, err
	}
	return s.GetSession(ctx, forked.ID)
}
