package session

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/lkarlslund/koder/internal/accesssettings"
	chatpkg "github.com/lkarlslund/koder/internal/chat"
	"github.com/lkarlslund/koder/internal/chatrole"
	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/id"
	"github.com/lkarlslund/koder/internal/planning"
	"github.com/lkarlslund/koder/internal/store"
	"github.com/lkarlslund/koder/internal/tools/chattool"
)

type RegistryConfig struct {
	DefaultProvider   string
	DefaultModel      string
	PermissionProfile string
	AccessSettings    accesssettings.Settings
	MaxChildChats     int
	PrepareChatSpec   func(context.Context, domain.ChatCreateSpec) (domain.ChatCreateSpec, error)
	OnChatArchived    func(context.Context, id.ID)
	BackendAvailable  func(domain.ChatBackend) error
	BeforeChatUpdate  func(context.Context, domain.Chat, chattool.UpdateRequest) error
	BeforeChatDelete  func(context.Context, domain.Chat) error
}

type Registry struct {
	store    *store.Store
	chatsSrc *chatpkg.Source
	planSrc  *planning.Source

	mu       sync.RWMutex
	sessions map[id.ID]*Session
	config   RegistryConfig
}

func NewRegistry(st *store.Store, chatsSrc *chatpkg.Source, planSrc *planning.Source, cfg RegistryConfig) *Registry {
	return &Registry{
		store:    st,
		chatsSrc: chatsSrc,
		planSrc:  planSrc,
		sessions: map[id.ID]*Session{},
		config:   cloneRegistryConfig(cfg),
	}
}

func (r *Registry) UpdateConfig(cfg RegistryConfig) {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.config = cloneRegistryConfig(cfg)
	sessions := make([]*Session, 0, len(r.sessions))
	for _, owner := range r.sessions {
		if owner != nil {
			sessions = append(sessions, owner)
		}
	}
	r.mu.Unlock()
	for _, owner := range sessions {
		owner.UpdateConfig(cfg)
	}
}

func (r *Registry) Load(ctx context.Context, sessionID id.ID) (*Session, error) {
	if r == nil || r.store == nil {
		return nil, fmt.Errorf("session registry store is required")
	}
	if r.chatsSrc == nil {
		return nil, fmt.Errorf("chat source is required")
	}
	if r.planSrc == nil {
		return nil, fmt.Errorf("planning source is required")
	}
	if sessionID == "" {
		return nil, fmt.Errorf("session id is required")
	}
	r.mu.RLock()
	if existing := r.sessions[sessionID]; existing != nil {
		r.mu.RUnlock()
		return existing, nil
	}
	r.mu.RUnlock()

	loaded, err := Load(ctx, r.store, r.chatsSrc, r.planSrc, sessionID)
	if err != nil {
		return nil, err
	}
	loaded.UpdateConfig(r.currentConfig())
	r.mu.Lock()
	if existing := r.sessions[sessionID]; existing != nil {
		r.mu.Unlock()
		_ = loaded.Close(context.Background())
		return existing, nil
	}
	r.sessions[sessionID] = loaded
	r.mu.Unlock()
	return loaded, nil
}

func (r *Registry) Loaded() []*Session {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*Session, 0, len(r.sessions))
	for _, owner := range r.sessions {
		if owner != nil {
			out = append(out, owner)
		}
	}
	return out
}

func (r *Registry) Chat(ctx context.Context, sessionID, chatID id.ID) (*chatpkg.Chat, error) {
	if sessionID == "" {
		return nil, fmt.Errorf("session id is required")
	}
	if chatID == "" {
		return nil, fmt.Errorf("chat id is required")
	}
	owner, err := r.Load(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	return owner.Chat(ctx, chatID)
}

func (r *Registry) ChatByID(ctx context.Context, chatID id.ID) (domain.Chat, error) {
	if chatID == "" {
		return domain.Chat{}, fmt.Errorf("chat id is required")
	}
	r.mu.RLock()
	for _, owner := range r.sessions {
		if owner == nil {
			continue
		}
		for _, chatRecord := range owner.Snapshot().Chats {
			if chatRecord.ID == chatID {
				r.mu.RUnlock()
				return chatRecord, nil
			}
		}
	}
	r.mu.RUnlock()
	sessions, err := r.List(ctx)
	if err != nil {
		return domain.Chat{}, err
	}
	for _, session := range sessions {
		owner, err := r.Load(ctx, session.ID)
		if err != nil {
			return domain.Chat{}, err
		}
		for _, chatRecord := range owner.Snapshot().Chats {
			if chatRecord.ID == chatID {
				return chatRecord, nil
			}
		}
	}
	return domain.Chat{}, fmt.Errorf("chat %s not found", chatID)
}

func (r *Registry) List(ctx context.Context) ([]domain.Session, error) {
	if r == nil || r.store == nil {
		return nil, fmt.Errorf("session registry store is required")
	}
	return listSessionRecords(ctx, r.store)
}

func (r *Registry) Create(ctx context.Context, title, projectRoot string, createProjectRoot bool) (*Session, error) {
	return r.create(ctx, title, projectRoot, createProjectRoot, "", domain.SessionKindRegular, false, chatrole.Orchestrator)
}

// CreateQuick creates a one-chat quick session in a caller-owned managed project root.
func (r *Registry) CreateQuick(ctx context.Context, sessionID id.ID, projectRoot string) (*Session, error) {
	return r.createQuick(ctx, sessionID, projectRoot, chatrole.Standalone)
}

// CreateQuickWithSpec creates a one-chat managed session using the same chat
// dimensions as the browser and phone creation surfaces.
func (r *Registry) CreateQuickWithSpec(ctx context.Context, sessionID id.ID, projectRoot string, spec domain.ChatCreateSpec) (*Session, error) {
	spec = spec.Normalized()
	if _, ok := chatrole.DefaultRegistry().Lookup(chatrole.Role(spec.WorkflowRole)); !ok {
		return nil, fmt.Errorf("profile %q is not registered", spec.WorkflowRole)
	}
	if spec.Backend != domain.ChatBackendKoder && spec.Backend != domain.ChatBackendCodex {
		return nil, fmt.Errorf("chat backend %q is not supported", spec.Backend)
	}
	if spec.InteractionMode != domain.InteractionModeText && spec.InteractionMode != domain.InteractionModeVoice {
		return nil, fmt.Errorf("interaction mode %q is not supported", spec.InteractionMode)
	}
	if spec.MilestoneKey != "" && spec.TaskRef != "" {
		return nil, fmt.Errorf("chat scope may select a milestone or a task, not both")
	}
	return r.createWithSpec(ctx, spec.Title, projectRoot, false, sessionID, domain.SessionKindQuick, true, spec)
}

// CreateQuickVoice creates a one-chat managed session whose chat uses the
// voice orchestrator profile.
func (r *Registry) CreateQuickVoice(ctx context.Context, sessionID id.ID, projectRoot string) (*Session, error) {
	return r.createQuick(ctx, sessionID, projectRoot, chatrole.Voice)
}

func (r *Registry) createQuick(ctx context.Context, sessionID id.ID, projectRoot string, role chatrole.Role) (*Session, error) {
	if sessionID == "" {
		return nil, fmt.Errorf("session id is required")
	}
	return r.create(ctx, "", projectRoot, false, sessionID, domain.SessionKindQuick, true, role)
}

// CreateVoice creates a durable one-chat voice coordination session.
func (r *Registry) CreateVoice(ctx context.Context, title string) (*Session, error) {
	return r.create(ctx, title, "", false, "", domain.SessionKindVoice, false, chatrole.Voice)
}

func (r *Registry) create(ctx context.Context, title, projectRoot string, createProjectRoot bool, sessionID id.ID, kind domain.SessionKind, managed bool, role domain.WorkflowRole) (*Session, error) {
	return r.createWithSpec(ctx, title, projectRoot, createProjectRoot, sessionID, kind, managed, domain.ChatCreateSpec{WorkflowRole: role})
}

func (r *Registry) createWithSpec(ctx context.Context, title, projectRoot string, createProjectRoot bool, sessionID id.ID, kind domain.SessionKind, managed bool, spec domain.ChatCreateSpec) (*Session, error) {
	if r == nil || r.store == nil {
		return nil, fmt.Errorf("session registry store is required")
	}
	spec = spec.Normalized()
	cfg := r.currentConfig()
	if cfg.PrepareChatSpec != nil {
		var err error
		spec, err = cfg.PrepareChatSpec(ctx, spec)
		if err != nil {
			return nil, err
		}
		spec = spec.Normalized()
	}
	title = strings.TrimSpace(title)
	titleUserDefined := title != ""
	if title == "" {
		title = "New Session"
	}
	projectRoot = strings.TrimSpace(projectRoot)
	if projectRoot != "" {
		info, err := os.Stat(projectRoot)
		if err != nil {
			if !os.IsNotExist(err) || !createProjectRoot {
				if os.IsNotExist(err) {
					return nil, fmt.Errorf("project root does not exist: %s", projectRoot)
				}
				return nil, err
			}
			if err := os.MkdirAll(projectRoot, 0o755); err != nil {
				return nil, fmt.Errorf("create project root %s: %w", projectRoot, err)
			}
			info, err = os.Stat(projectRoot)
			if err != nil {
				return nil, err
			}
		}
		if !info.IsDir() {
			return nil, fmt.Errorf("project root must be a directory: %s", projectRoot)
		}
	}
	modelID := cfg.DefaultModel
	providerID := cfg.DefaultProvider
	if strings.TrimSpace(spec.ProviderID) != "" {
		providerID = strings.TrimSpace(spec.ProviderID)
	}
	if strings.TrimSpace(spec.ModelID) != "" {
		modelID = strings.TrimSpace(spec.ModelID)
	}
	if spec.Backend == domain.ChatBackendCodex {
		modelID = spec.ModelID
		providerID = ""
	} else if spec.ModelID != "" {
		modelID = spec.ModelID
	}
	permissionProfile := cfg.PermissionProfile
	if spec.PermissionProfile != "" {
		permissionProfile = spec.PermissionProfile
	}
	session, err := createSessionRecordWithOptions(ctx, r.store, r.chatsSrc, createSessionOptions{
		ID:                     sessionID,
		Title:                  title,
		TitleUserDefined:       titleUserDefined,
		ProviderID:             providerID,
		ModelID:                modelID,
		PermissionProfile:      permissionProfile,
		Kind:                   kind,
		ProjectRoot:            projectRoot,
		ProjectRootManaged:     managed,
		InitialChatRole:        spec.WorkflowRole,
		InitialChatBackend:     spec.Backend,
		InitialInteractionMode: spec.InteractionMode,
		InitialToolStates:      spec.ToolStates,
		InitialMilestoneKey:    spec.MilestoneKey,
		InitialTaskRef:         spec.TaskRef,
	})
	if err != nil {
		return nil, err
	}
	owner, err := r.Load(ctx, session.ID)
	if err != nil {
		return nil, err
	}
	if _, err := owner.UpdateSession(ctx, func(session *domain.Session) {
		session.AccessSettings = cfg.AccessSettings
	}); err != nil {
		return nil, err
	}
	return owner, nil
}

func (r *Registry) Delete(ctx context.Context, sessionID id.ID) error {
	if r == nil || r.store == nil {
		return fmt.Errorf("session registry store is required")
	}
	if sessionID == "" {
		return fmt.Errorf("session id is required")
	}
	r.mu.RLock()
	owner := r.sessions[sessionID]
	config := cloneRegistryConfig(r.config)
	r.mu.RUnlock()
	if owner == nil {
		loaded, err := r.Load(ctx, sessionID)
		if err != nil {
			return err
		}
		owner = loaded
	}
	if config.BeforeChatDelete != nil {
		for _, chatRecord := range owner.Snapshot().Chats {
			if err := config.BeforeChatDelete(ctx, chatRecord); err != nil {
				return err
			}
		}
	}
	r.mu.Lock()
	delete(r.sessions, sessionID)
	r.mu.Unlock()
	if owner != nil {
		if err := owner.Close(ctx); err != nil {
			return err
		}
	}
	return deleteSessionRecord(ctx, r.store, r.chatsSrc, r.planSrc, sessionID)
}

func (r *Registry) Shutdown(ctx context.Context, reason chatpkg.CancelReason) error {
	if r == nil {
		return nil
	}
	sessions := r.Loaded()
	var firstErr error
	for _, owner := range sessions {
		if err := owner.Shutdown(ctx, reason); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (r *Registry) currentConfig() RegistryConfig {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return cloneRegistryConfig(r.config)
}

func cloneRegistryConfig(cfg RegistryConfig) RegistryConfig {
	return cfg
}
