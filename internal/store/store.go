package store

import (
	"context"
	"fmt"
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
	SetSessionPermissionProfile(context.Context, int64, string) error
	UpdateSessionTitle(context.Context, int64, string) error
	UpdateSessionAgents(context.Context, int64, string, string, string, string, []domain.AgentsFile, time.Time) error
	CountMessagesByRole(context.Context, int64, domain.MessageRole) (int, error)
	SetSessionModel(context.Context, int64, string, string) error
	AddMessage(context.Context, int64, domain.MessageRole, string) (domain.Message, error)
	UpdateMessageSummary(context.Context, int64, string) error
	AddPart(context.Context, int64, domain.PartKind, string, string) (domain.Part, error)
	UpdatePartMetaJSON(context.Context, int64, string) error
	PartsForSession(context.Context, int64) ([]domain.Message, map[int64][]domain.Part, error)
	CreateApproval(context.Context, int64, domain.ToolKind, string) (Approval, error)
	UpdateApproval(context.Context, int64, domain.ApprovalStatus) error
	PendingApprovals(context.Context, int64) ([]Approval, error)
	AddTask(context.Context, int64, string, domain.TaskStatus) (Task, error)
	UpdateTask(context.Context, int64, domain.TaskStatus) error
	ListTasks(context.Context, int64) ([]Task, error)
	GetApproval(context.Context, int64) (Approval, error)
}

type Approval struct {
	ID        int64
	SessionID int64
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

func (s *Store) ListSessions(ctx context.Context) ([]domain.Session, error) {
	return s.backend.ListSessions(ctx)
}

func (s *Store) GetSession(ctx context.Context, sessionID int64) (domain.Session, error) {
	return s.backend.GetSession(ctx, sessionID)
}

func (s *Store) SetSessionPermissionProfile(ctx context.Context, sessionID int64, profile string) error {
	return s.backend.SetSessionPermissionProfile(ctx, sessionID, profile)
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

func (s *Store) CreateApproval(ctx context.Context, sessionID int64, tool domain.ToolKind, command string) (Approval, error) {
	return s.backend.CreateApproval(ctx, sessionID, tool, command)
}

func (s *Store) UpdateApproval(ctx context.Context, approvalID int64, status domain.ApprovalStatus) error {
	return s.backend.UpdateApproval(ctx, approvalID, status)
}

func (s *Store) PendingApprovals(ctx context.Context, sessionID int64) ([]Approval, error) {
	return s.backend.PendingApprovals(ctx, sessionID)
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
	if source.PermissionProfile != "" {
		if err := s.SetSessionPermissionProfile(ctx, forked.ID, source.PermissionProfile); err != nil {
			return domain.Session{}, err
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
