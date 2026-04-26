package store

import (
	"context"
	"fmt"
	"strings"
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
	backend backend
}

type backend interface {
	Close() error
	EnsureSession(context.Context, string, string) (domain.Session, error)
	CreateSession(context.Context, string, string, string, *int64) (domain.Session, error)
	ListSessions(context.Context) ([]domain.Session, error)
	GetSession(context.Context, int64) (domain.Session, error)
	CreateChat(context.Context, int64, string, domain.WorkflowRole, *int64) (domain.Chat, error)
	ListChats(context.Context, int64) ([]domain.Chat, error)
	GetChat(context.Context, int64) (domain.Chat, error)
	DefaultChat(context.Context, int64) (domain.Chat, error)
	UpdateSessionWorkspace(context.Context, int64, string, string) error
	SetSessionPermissionProfile(context.Context, int64, string) error
	SetSessionToolStates(context.Context, int64, map[domain.ToolKind]bool) error
	UpdateSessionTitle(context.Context, int64, string) error
	UpdateSessionAgents(context.Context, int64, string, string, string, string, []domain.AgentsFile, time.Time) error
	CountMessagesByRole(context.Context, int64, domain.MessageRole) (int, error)
	SetSessionModel(context.Context, int64, string, string) error
	AddMessage(context.Context, int64, domain.MessageRole, string) (domain.Message, error)
	AddChatMessage(context.Context, int64, domain.MessageRole, string) (domain.Message, error)
	UpdateMessageSummary(context.Context, int64, string) error
	AddPart(context.Context, int64, domain.PartKind, string, string) (domain.Part, error)
	UpdatePartMetaJSON(context.Context, int64, string) error
	PartsForSession(context.Context, int64) ([]domain.Message, map[int64][]domain.Part, error)
	PartsForChat(context.Context, int64) ([]domain.Message, map[int64][]domain.Part, error)
	CreateApproval(context.Context, int64, domain.ToolKind, string) (Approval, error)
	CreateChatApproval(context.Context, int64, domain.ToolKind, string) (Approval, error)
	UpdateApproval(context.Context, int64, domain.ApprovalStatus) error
	PendingApprovals(context.Context, int64) ([]Approval, error)
	PendingApprovalsForChat(context.Context, int64) ([]Approval, error)
	AddTask(context.Context, int64, string, domain.TaskStatus) (Task, error)
	UpdateTask(context.Context, int64, domain.TaskStatus) error
	ListTasks(context.Context, int64) ([]Task, error)
	SetMilestonePlan(context.Context, int64, string, []Milestone) (MilestonePlan, error)
	GetMilestonePlan(context.Context, int64) (MilestonePlan, error)
	AddTodoItems(context.Context, int64, string, []string) ([]TodoItem, error)
	UpdateTodoItem(context.Context, int64, domain.TodoStatus, string) (TodoItem, error)
	ListTodos(context.Context, int64, string) ([]TodoItem, error)
	GetApproval(context.Context, int64) (Approval, error)
}

type Approval struct {
	ID        int64
	SessionID int64
	ChatID    int64
	Tool      domain.ToolKind
	Command   string
	Status    domain.ApprovalStatus
	CreatedAt time.Time
}

type Task struct {
	ID        int64
	SessionID int64
	Body      string
	Status    domain.TaskStatus
	CreatedAt time.Time
}

type MilestonePlan struct {
	SessionID  int64
	Summary    string
	Milestones []Milestone
	UpdatedAt  time.Time
}

type Milestone struct {
	Ref      string
	Title    string
	Status   domain.MilestoneStatus
	Notes    string
	Position int
}

type TodoItem struct {
	ID           int64
	SessionID    int64
	MilestoneRef string
	Content      string
	Status       domain.TodoStatus
	Position     int
	CreatedAt    time.Time
	UpdatedAt    time.Time
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

func (s *Store) CreateSession(ctx context.Context, title, providerID, modelID string, parentID *int64) (domain.Session, error) {
	return s.backend.CreateSession(ctx, title, providerID, modelID, parentID)
}

func (s *Store) CreateChat(ctx context.Context, sessionID int64, title string, role domain.WorkflowRole, parentChatID *int64) (domain.Chat, error) {
	return s.backend.CreateChat(ctx, sessionID, title, role, parentChatID)
}

func (s *Store) ListSessions(ctx context.Context) ([]domain.Session, error) {
	return s.backend.ListSessions(ctx)
}

func (s *Store) GetSession(ctx context.Context, sessionID int64) (domain.Session, error) {
	return s.backend.GetSession(ctx, sessionID)
}

func (s *Store) ListChats(ctx context.Context, sessionID int64) ([]domain.Chat, error) {
	return s.backend.ListChats(ctx, sessionID)
}

func (s *Store) GetChat(ctx context.Context, chatID int64) (domain.Chat, error) {
	return s.backend.GetChat(ctx, chatID)
}

func (s *Store) DefaultChat(ctx context.Context, sessionID int64) (domain.Chat, error) {
	return s.backend.DefaultChat(ctx, sessionID)
}

func (s *Store) UpdateSessionWorkspace(ctx context.Context, sessionID int64, cwd, projectRoot string) error {
	return s.backend.UpdateSessionWorkspace(ctx, sessionID, cwd, projectRoot)
}

func (s *Store) SetSessionPermissionProfile(ctx context.Context, sessionID int64, profile string) error {
	return s.backend.SetSessionPermissionProfile(ctx, sessionID, profile)
}

func (s *Store) SetSessionToolStates(ctx context.Context, sessionID int64, states map[domain.ToolKind]bool) error {
	return s.backend.SetSessionToolStates(ctx, sessionID, states)
}

func (s *Store) UpdateSessionTitle(ctx context.Context, sessionID int64, title string) error {
	return s.backend.UpdateSessionTitle(ctx, sessionID, title)
}

func (s *Store) UpdateSessionAgents(
	ctx context.Context,
	sessionID int64,
	projectRoot string,
	projectChecksum string,
	resolved string,
	summary string,
	files []domain.AgentsFile,
	generatedAt time.Time,
) error {
	return s.backend.UpdateSessionAgents(ctx, sessionID, projectRoot, projectChecksum, resolved, summary, files, generatedAt)
}

func (s *Store) CountMessagesByRole(ctx context.Context, sessionID int64, role domain.MessageRole) (int, error) {
	return s.backend.CountMessagesByRole(ctx, sessionID, role)
}

func (s *Store) SetSessionModel(ctx context.Context, sessionID int64, providerID, modelID string) error {
	return s.backend.SetSessionModel(ctx, sessionID, providerID, modelID)
}

func (s *Store) AddMessage(ctx context.Context, sessionID int64, role domain.MessageRole, summary string) (domain.Message, error) {
	return s.backend.AddMessage(ctx, sessionID, role, summary)
}

func (s *Store) AddChatMessage(ctx context.Context, chatID int64, role domain.MessageRole, summary string) (domain.Message, error) {
	return s.backend.AddChatMessage(ctx, chatID, role, summary)
}

func (s *Store) UpdateMessageSummary(ctx context.Context, messageID int64, summary string) error {
	return s.backend.UpdateMessageSummary(ctx, messageID, summary)
}

func (s *Store) AddPart(ctx context.Context, messageID int64, kind domain.PartKind, body, metaJSON string) (domain.Part, error) {
	return s.backend.AddPart(ctx, messageID, kind, body, metaJSON)
}

func (s *Store) UpdatePartMetaJSON(ctx context.Context, partID int64, metaJSON string) error {
	return s.backend.UpdatePartMetaJSON(ctx, partID, metaJSON)
}

func (s *Store) PartsForSession(ctx context.Context, sessionID int64) ([]domain.Message, map[int64][]domain.Part, error) {
	return s.backend.PartsForSession(ctx, sessionID)
}

func (s *Store) PartsForChat(ctx context.Context, chatID int64) ([]domain.Message, map[int64][]domain.Part, error) {
	return s.backend.PartsForChat(ctx, chatID)
}

func (s *Store) CreateApproval(ctx context.Context, sessionID int64, tool domain.ToolKind, command string) (Approval, error) {
	return s.backend.CreateApproval(ctx, sessionID, tool, command)
}

func (s *Store) CreateChatApproval(ctx context.Context, chatID int64, tool domain.ToolKind, command string) (Approval, error) {
	return s.backend.CreateChatApproval(ctx, chatID, tool, command)
}

func (s *Store) UpdateApproval(ctx context.Context, approvalID int64, status domain.ApprovalStatus) error {
	return s.backend.UpdateApproval(ctx, approvalID, status)
}

func (s *Store) PendingApprovals(ctx context.Context, sessionID int64) ([]Approval, error) {
	return s.backend.PendingApprovals(ctx, sessionID)
}

func (s *Store) PendingApprovalsForChat(ctx context.Context, chatID int64) ([]Approval, error) {
	return s.backend.PendingApprovalsForChat(ctx, chatID)
}

func (s *Store) AddTask(ctx context.Context, sessionID int64, body string, status domain.TaskStatus) (Task, error) {
	return s.backend.AddTask(ctx, sessionID, body, status)
}

func (s *Store) UpdateTask(ctx context.Context, taskID int64, status domain.TaskStatus) error {
	return s.backend.UpdateTask(ctx, taskID, status)
}

func (s *Store) ListTasks(ctx context.Context, sessionID int64) ([]Task, error) {
	return s.backend.ListTasks(ctx, sessionID)
}

func (s *Store) SetMilestonePlan(ctx context.Context, sessionID int64, summary string, milestones []Milestone) (MilestonePlan, error) {
	return s.backend.SetMilestonePlan(ctx, sessionID, summary, milestones)
}

func (s *Store) GetMilestonePlan(ctx context.Context, sessionID int64) (MilestonePlan, error) {
	return s.backend.GetMilestonePlan(ctx, sessionID)
}

func (s *Store) AddTodoItems(ctx context.Context, sessionID int64, milestoneRef string, contents []string) ([]TodoItem, error) {
	return s.backend.AddTodoItems(ctx, sessionID, milestoneRef, contents)
}

func (s *Store) UpdateTodoItem(ctx context.Context, todoID int64, status domain.TodoStatus, content string) (TodoItem, error) {
	return s.backend.UpdateTodoItem(ctx, todoID, status, content)
}

func (s *Store) ListTodos(ctx context.Context, sessionID int64, milestoneRef string) ([]TodoItem, error) {
	return s.backend.ListTodos(ctx, sessionID, milestoneRef)
}

func (s *Store) GetApproval(ctx context.Context, approvalID int64) (Approval, error) {
	return s.backend.GetApproval(ctx, approvalID)
}

func (s *Store) ForkSession(ctx context.Context, sourceSessionID int64) (domain.Session, error) {
	source, err := s.GetSession(ctx, sourceSessionID)
	if err != nil {
		return domain.Session{}, err
	}
	messages, partsByMessage, err := s.PartsForSession(ctx, sourceSessionID)
	if err != nil {
		return domain.Session{}, err
	}
	forked, err := s.CreateSession(ctx, source.Title, source.ProviderID, source.ModelID, &source.ID)
	if err != nil {
		return domain.Session{}, err
	}
	if err := s.UpdateSessionWorkspace(ctx, forked.ID, source.CWD, source.ProjectRoot); err != nil {
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
	for _, msg := range messages {
		next, err := s.AddMessage(ctx, forked.ID, msg.Role, msg.Summary)
		if err != nil {
			return domain.Session{}, err
		}
		for _, part := range partsByMessage[msg.ID] {
			if _, err := s.AddPart(ctx, next.ID, part.Kind, part.Body, part.MetaJSON); err != nil {
				return domain.Session{}, err
			}
		}
	}
	return s.GetSession(ctx, forked.ID)
}
