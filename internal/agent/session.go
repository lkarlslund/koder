package agent

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/lkarlslund/koder/internal/chat"
	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/id"
	sessionpkg "github.com/lkarlslund/koder/internal/session"
)

// LoadSession returns the live owner for a persisted session, hydrating it on demand.
func (e *Engine) LoadSession(ctx context.Context, sessionID id.ID) (*sessionpkg.Session, error) {
	if e == nil || e.store == nil {
		return nil, fmt.Errorf("engine store is required")
	}
	if sessionID == "" {
		return nil, fmt.Errorf("session id is required")
	}
	e.sessionMu.RLock()
	if existing := e.sessions[sessionID]; existing != nil {
		e.sessionMu.RUnlock()
		return existing, nil
	}
	e.sessionMu.RUnlock()

	loaded, err := sessionpkg.Load(ctx, e.store, e.MetadataChat, sessionID)
	if err != nil {
		return nil, err
	}
	e.sessionMu.Lock()
	if existing := e.sessions[sessionID]; existing != nil {
		e.sessionMu.Unlock()
		_ = loaded.Close(context.Background())
		return existing, nil
	}
	e.sessions[sessionID] = loaded
	e.sessionMu.Unlock()
	return loaded, nil
}

// Session returns an already loaded session owner, loading it if needed.
func (e *Engine) Session(ctx context.Context, sessionID id.ID) (*sessionpkg.Session, error) {
	return e.LoadSession(ctx, sessionID)
}

// LoadedSessions returns the live session owners currently held by the engine.
func (e *Engine) LoadedSessions() []*sessionpkg.Session {
	if e == nil {
		return nil
	}
	e.sessionMu.RLock()
	defer e.sessionMu.RUnlock()
	out := make([]*sessionpkg.Session, 0, len(e.sessions))
	for _, owner := range e.sessions {
		if owner != nil {
			out = append(out, owner)
		}
	}
	return out
}

func (e *Engine) chatOwner(ctx context.Context, sessionID, chatID id.ID) (*chat.Chat, error) {
	if sessionID == "" {
		return nil, fmt.Errorf("session id is required")
	}
	if chatID == "" {
		return nil, fmt.Errorf("chat id is required")
	}
	owner, err := e.LoadSession(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	return owner.Chat(ctx, chatID)
}

func (e *Engine) chatByID(ctx context.Context, chatID id.ID) (domain.Chat, error) {
	if chatID == "" {
		return domain.Chat{}, fmt.Errorf("chat id is required")
	}
	e.sessionMu.RLock()
	for _, owner := range e.sessions {
		if owner == nil {
			continue
		}
		snapshot := owner.Snapshot()
		for _, chatRecord := range snapshot.Chats {
			if chatRecord.ID == chatID {
				e.sessionMu.RUnlock()
				return chatRecord, nil
			}
		}
	}
	e.sessionMu.RUnlock()
	sessions, err := e.Sessions(ctx)
	if err != nil {
		return domain.Chat{}, err
	}
	for _, session := range sessions {
		owner, err := e.LoadSession(ctx, session.ID)
		if err != nil {
			return domain.Chat{}, err
		}
		chats := owner.Snapshot().Chats
		for _, chatRecord := range chats {
			if chatRecord.ID == chatID {
				return chatRecord, nil
			}
		}
	}
	return domain.Chat{}, fmt.Errorf("chat %s not found", chatID)
}

// Sessions returns persisted session metadata.
func (e *Engine) Sessions(ctx context.Context) ([]domain.Session, error) {
	if e == nil || e.store == nil {
		return nil, fmt.Errorf("engine store is required")
	}
	return sessionpkg.ListSessions(ctx, e.store)
}

// CreateSession creates, configures, and loads a live session owner.
func (e *Engine) CreateSession(ctx context.Context, title, projectRoot string, createProjectRoot bool) (*sessionpkg.Session, error) {
	if e == nil || e.store == nil {
		return nil, fmt.Errorf("engine store is required")
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
	session, err := sessionpkg.CreateSession(ctx, e.store, title, e.cfg.DefaultProvider, e.cfg.DefaultModel, nil)
	if err != nil {
		return nil, err
	}
	if err := sessionpkg.UpdateSession(ctx, e.store, session.ID, func(session *domain.Session) {
		session.ProjectRoot = projectRoot
		session.AccessSettings = e.cfg.Access
		session.ToolStates = make(domain.ToolStates, len(e.cfg.ToolDefaults))
		for kind, enabled := range e.cfg.ToolDefaults {
			session.ToolStates[kind] = enabled
		}
	}); err != nil {
		return nil, err
	}
	return e.LoadSession(ctx, session.ID)
}

// DeleteSession closes any live runtimes and deletes the persisted session.
func (e *Engine) DeleteSession(ctx context.Context, sessionID id.ID) error {
	if e == nil || e.store == nil {
		return fmt.Errorf("engine store is required")
	}
	if sessionID == "" {
		return fmt.Errorf("session id is required")
	}
	e.sessionMu.Lock()
	owner := e.sessions[sessionID]
	delete(e.sessions, sessionID)
	e.sessionMu.Unlock()
	if owner != nil {
		if err := owner.Close(ctx); err != nil {
			return err
		}
	}
	return sessionpkg.DeleteSession(ctx, e.store, sessionID)
}

func (e *Engine) Shutdown(ctx context.Context, reason chat.CancelReason) error {
	if e == nil {
		return nil
	}
	e.sessionMu.RLock()
	sessions := make([]*sessionpkg.Session, 0, len(e.sessions))
	for _, owner := range e.sessions {
		if owner != nil {
			sessions = append(sessions, owner)
		}
	}
	e.sessionMu.RUnlock()
	var firstErr error
	for _, owner := range sessions {
		if err := owner.Shutdown(ctx, reason); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}
