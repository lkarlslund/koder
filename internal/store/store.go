package store

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/lkarlslund/koder/internal/domain"
)

const (
	BackendPebble = "pebble"
	BackendJSONFS = "jsonfs"
)

type Options struct {
	Backend string
}

type Store struct {
	backend    backend
	toolCallMu sync.Mutex
}

type backend interface {
	Close() error
	EnsureSession(context.Context, string, string) (domain.Session, error)
	CreateSession(context.Context, string, string, string, *domain.ID) (domain.Session, error)
	ListSessions(context.Context) ([]domain.Session, error)
	GetSession(context.Context, domain.ID) (domain.Session, error)
	TouchSession(context.Context, domain.ID) (domain.Session, error)
	DeleteSession(context.Context, domain.ID) error
	CreateChat(context.Context, domain.ID, string, domain.WorkflowRole, *domain.ID) (domain.Chat, error)
	ListChats(context.Context, domain.ID) ([]domain.Chat, error)
	GetChat(context.Context, domain.ID) (domain.Chat, error)
	DefaultChat(context.Context, domain.ID) (domain.Chat, error)
	UpdateChat(context.Context, domain.Chat) error
	SetChatModel(context.Context, domain.ID, string, string) error
	DeleteChat(context.Context, domain.ID) error
	SetChatQueuedInputs(context.Context, domain.ID, []domain.QueuedInput) error
	SetSessionProjectRoot(context.Context, domain.ID, string) error
	SetSessionPermissionProfile(context.Context, domain.ID, string) error
	AddSessionPermissionRule(context.Context, domain.ID, domain.PermissionOverride) error
	SetSessionToolStates(context.Context, domain.ID, map[domain.ToolKind]bool) error
	UpdateSessionTitle(context.Context, domain.ID, string, time.Time, int) error
	UpdateSessionAgents(context.Context, domain.ID, string, string, string, string, []domain.AgentsFile, time.Time) error
	CreateApproval(context.Context, domain.ID, domain.ToolKind, string) (Approval, error)
	CreateChatApproval(context.Context, domain.ID, domain.ToolKind, string) (Approval, error)
	UpdateApproval(context.Context, domain.ID, domain.ApprovalStatus) error
	PendingApprovals(context.Context, domain.ID) ([]Approval, error)
	PendingApprovalsForChat(context.Context, domain.ID) ([]Approval, error)
	AddTask(context.Context, domain.ID, string, domain.TaskStatus) (Task, error)
	PutTask(context.Context, Task) error
	UpdateTask(context.Context, domain.ID, domain.TaskStatus) error
	ListTasks(context.Context, domain.ID) ([]Task, error)
	SetMilestonePlan(context.Context, domain.ID, string, []Milestone) (MilestonePlan, error)
	PutMilestonePlan(context.Context, MilestonePlan) error
	GetMilestonePlan(context.Context, domain.ID) (MilestonePlan, error)
	AddTodoItems(context.Context, domain.ID, string, []string) ([]TodoItem, error)
	PutTodoItem(context.Context, TodoItem) error
	UpdateTodoItem(context.Context, domain.ID, domain.TodoStatus, string) (TodoItem, error)
	ListTodos(context.Context, domain.ID, string) ([]TodoItem, error)
	GetApproval(context.Context, domain.ID) (Approval, error)
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

type MilestonePlan struct {
	SessionID  domain.ID
	Summary    string
	Milestones []Milestone
	UpdatedAt  time.Time
}

type Milestone struct {
	Ref         string
	Title       string
	Status      domain.MilestoneStatus
	Notes       string
	Position    int
	OwnerChatID *domain.ID
}

type TodoItem struct {
	ID           domain.ID
	SessionID    domain.ID
	MilestoneRef string
	Content      string
	Status       domain.TodoStatus
	Position     int
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

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

	var impl backend
	var err error
	switch backendName {
	case BackendPebble:
		impl, err = openPebbleBackend(stateDir)
	case BackendJSONFS:
		impl, err = openJSONFSBackend(stateDir)
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
	return s.backend.EnsureSession(ctx, providerID, modelID)
}

func (s *Store) CreateSession(ctx context.Context, title, providerID, modelID string, parentID *domain.ID) (domain.Session, error) {
	return s.backend.CreateSession(ctx, title, providerID, modelID, parentID)
}

func (s *Store) CreateChat(ctx context.Context, sessionID domain.ID, title string, role domain.WorkflowRole, parentChatID *domain.ID) (domain.Chat, error) {
	return s.backend.CreateChat(ctx, sessionID, title, role, parentChatID)
}

func (s *Store) ListSessions(ctx context.Context) ([]domain.Session, error) {
	return s.backend.ListSessions(ctx)
}

func (s *Store) GetSession(ctx context.Context, sessionID domain.ID) (domain.Session, error) {
	return s.backend.GetSession(ctx, sessionID)
}

// TouchSession marks a session as recently used and returns the updated record.
func (s *Store) TouchSession(ctx context.Context, sessionID domain.ID) (domain.Session, error) {
	return s.backend.TouchSession(ctx, sessionID)
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
	return s.backend.DeleteSession(ctx, sessionID)
}

func (s *Store) ListChats(ctx context.Context, sessionID domain.ID) ([]domain.Chat, error) {
	return s.backend.ListChats(ctx, sessionID)
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
	return s.backend.GetChat(ctx, chatID)
}

func (s *Store) DefaultChat(ctx context.Context, sessionID domain.ID) (domain.Chat, error) {
	return s.backend.DefaultChat(ctx, sessionID)
}

func (s *Store) UpdateChat(ctx context.Context, chat domain.Chat) error {
	existing, err := s.backend.GetChat(ctx, chat.ID)
	if err != nil {
		return err
	}
	if chat.Position == 0 && existing.Position != 0 && chat.UpdatedAt.After(existing.UpdatedAt) {
		chat.Position = existing.Position
	}
	return s.backend.UpdateChat(ctx, chat)
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
	return s.backend.SetChatModel(ctx, chatID, providerID, modelID)
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
	return s.backend.DeleteChat(ctx, chatID)
}

func (s *Store) SetChatQueuedInputs(ctx context.Context, chatID domain.ID, items []domain.QueuedInput) error {
	return s.backend.SetChatQueuedInputs(ctx, chatID, items)
}

func (s *Store) SetSessionProjectRoot(ctx context.Context, sessionID domain.ID, projectRoot string) error {
	return s.backend.SetSessionProjectRoot(ctx, sessionID, projectRoot)
}

func (s *Store) SetSessionPermissionProfile(ctx context.Context, sessionID domain.ID, profile string) error {
	return s.backend.SetSessionPermissionProfile(ctx, sessionID, profile)
}

func (s *Store) AddSessionPermissionRule(ctx context.Context, sessionID domain.ID, rule domain.PermissionOverride) error {
	return s.backend.AddSessionPermissionRule(ctx, sessionID, rule)
}

func (s *Store) SetSessionToolStates(ctx context.Context, sessionID domain.ID, states map[domain.ToolKind]bool) error {
	return s.backend.SetSessionToolStates(ctx, sessionID, states)
}

func (s *Store) UpdateSessionTitle(ctx context.Context, sessionID domain.ID, title string, generatedAt time.Time, refreshCount int) error {
	return s.backend.UpdateSessionTitle(ctx, sessionID, title, generatedAt, refreshCount)
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
	return s.backend.UpdateSessionAgents(ctx, sessionID, projectRoot, projectChecksum, resolved, summary, files, generatedAt)
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
	return s.backend.CreateApproval(ctx, sessionID, tool, command)
}

func (s *Store) CreateChatApproval(ctx context.Context, chatID domain.ID, tool domain.ToolKind, command string) (Approval, error) {
	return s.backend.CreateChatApproval(ctx, chatID, tool, command)
}

func (s *Store) UpdateApproval(ctx context.Context, approvalID domain.ID, status domain.ApprovalStatus) error {
	return s.backend.UpdateApproval(ctx, approvalID, status)
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
	return s.backend.AddTask(ctx, sessionID, body, status)
}

func (s *Store) PutTask(ctx context.Context, task Task) error {
	if task.ID == "" {
		return fmt.Errorf("put task: id is required")
	}
	if task.SessionID == "" {
		return fmt.Errorf("put task: session id is required")
	}
	return s.backend.PutTask(ctx, task)
}

func (s *Store) UpdateTask(ctx context.Context, taskID domain.ID, status domain.TaskStatus) error {
	return s.backend.UpdateTask(ctx, taskID, status)
}

func (s *Store) ListTasks(ctx context.Context, sessionID domain.ID) ([]Task, error) {
	return s.backend.ListTasks(ctx, sessionID)
}

func (s *Store) SetMilestonePlan(ctx context.Context, sessionID domain.ID, summary string, milestones []Milestone) (MilestonePlan, error) {
	return s.backend.SetMilestonePlan(ctx, sessionID, summary, milestones)
}

func (s *Store) PutMilestonePlan(ctx context.Context, plan MilestonePlan) error {
	if plan.SessionID == "" {
		return fmt.Errorf("put milestone plan: session id is required")
	}
	return s.backend.PutMilestonePlan(ctx, plan)
}

func (s *Store) GetMilestonePlan(ctx context.Context, sessionID domain.ID) (MilestonePlan, error) {
	return s.backend.GetMilestonePlan(ctx, sessionID)
}

func (s *Store) AddTodoItems(ctx context.Context, sessionID domain.ID, milestoneRef string, contents []string) ([]TodoItem, error) {
	return s.backend.AddTodoItems(ctx, sessionID, milestoneRef, contents)
}

func (s *Store) PutTodoItem(ctx context.Context, item TodoItem) error {
	if item.ID == "" {
		return fmt.Errorf("put todo item: id is required")
	}
	if item.SessionID == "" {
		return fmt.Errorf("put todo item: session id is required")
	}
	return s.backend.PutTodoItem(ctx, item)
}

func (s *Store) UpdateTodoItem(ctx context.Context, todoID domain.ID, status domain.TodoStatus, content string) (TodoItem, error) {
	return s.backend.UpdateTodoItem(ctx, todoID, status, content)
}

func (s *Store) ListTodos(ctx context.Context, sessionID domain.ID, milestoneRef string) ([]TodoItem, error) {
	return s.backend.ListTodos(ctx, sessionID, milestoneRef)
}

func (s *Store) GetApproval(ctx context.Context, approvalID domain.ID) (Approval, error) {
	return s.backend.GetApproval(ctx, approvalID)
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
