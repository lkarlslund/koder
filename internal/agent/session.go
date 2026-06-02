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

	loaded, err := sessionpkg.Load(ctx, e.store, e.Chat, sessionID)
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

// Sessions returns persisted session metadata.
func (e *Engine) Sessions(ctx context.Context) ([]domain.Session, error) {
	if e == nil || e.store == nil {
		return nil, fmt.Errorf("engine store is required")
	}
	return sessionpkg.ListSessions(ctx, e.store)
}

// CreateSession creates, configures, and loads a live session owner.
func (e *Engine) CreateSession(ctx context.Context, title, projectRoot string) (*sessionpkg.Session, error) {
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
			return nil, err
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
