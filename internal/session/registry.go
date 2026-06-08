package session

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/lkarlslund/koder/internal/accesssettings"
	chatpkg "github.com/lkarlslund/koder/internal/chat"
	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/id"
	"github.com/lkarlslund/koder/internal/store"
)

type RegistryConfig struct {
	DefaultProvider string
	DefaultModel    string
	AccessSettings  accesssettings.Settings
}

type Registry struct {
	store      *store.Store
	chatLoader ChatLoader

	mu       sync.RWMutex
	sessions map[id.ID]*Session
	config   RegistryConfig
}

func NewRegistry(st *store.Store, chatLoader ChatLoader, cfg RegistryConfig) *Registry {
	return &Registry{
		store:      st,
		chatLoader: chatLoader,
		sessions:   map[id.ID]*Session{},
		config:     cloneRegistryConfig(cfg),
	}
}

func (r *Registry) UpdateConfig(cfg RegistryConfig) {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.config = cloneRegistryConfig(cfg)
	r.mu.Unlock()
}

func (r *Registry) Load(ctx context.Context, sessionID id.ID) (*Session, error) {
	if r == nil || r.store == nil {
		return nil, fmt.Errorf("session registry store is required")
	}
	if r.chatLoader == nil {
		return nil, fmt.Errorf("chat loader is required")
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

	loaded, err := Load(ctx, r.store, r.chatLoader, sessionID)
	if err != nil {
		return nil, err
	}
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
	return ListSessions(ctx, r.store)
}

func (r *Registry) Create(ctx context.Context, title, projectRoot string, createProjectRoot bool) (*Session, error) {
	if r == nil || r.store == nil {
		return nil, fmt.Errorf("session registry store is required")
	}
	title = strings.TrimSpace(title)
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
	cfg := r.currentConfig()
	session, err := CreateSession(ctx, r.store, title, cfg.DefaultProvider, cfg.DefaultModel, nil)
	if err != nil {
		return nil, err
	}
	owner, err := r.Load(ctx, session.ID)
	if err != nil {
		return nil, err
	}
	if _, err := owner.UpdateSession(ctx, func(session *domain.Session) {
		session.ProjectRoot = projectRoot
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
	r.mu.Lock()
	owner := r.sessions[sessionID]
	delete(r.sessions, sessionID)
	r.mu.Unlock()
	if owner != nil {
		if err := owner.Close(ctx); err != nil {
			return err
		}
	}
	return DeleteSession(ctx, r.store, sessionID)
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
