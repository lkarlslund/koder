package agent

import (
	"context"
	"fmt"

	"github.com/lkarlslund/koder/internal/chat"
	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/id"
	sessionpkg "github.com/lkarlslund/koder/internal/session"
	"github.com/lkarlslund/koder/internal/settings"
)

func sessionRegistryConfig(defaults settings.NewSessionDefaults) sessionpkg.RegistryConfig {
	return sessionpkg.RegistryConfig{
		DefaultProvider: defaults.ProviderID,
		DefaultModel:    defaults.ModelID,
		AccessSettings:  defaults.Access,
	}
}

// LoadSession returns the live owner for a persisted session, hydrating it on demand.
func (e *Engine) LoadSession(ctx context.Context, sessionID id.ID) (*sessionpkg.Session, error) {
	if e == nil || e.registry == nil {
		return nil, fmt.Errorf("session registry is required")
	}
	return e.registry.Load(ctx, sessionID)
}

// Session returns an already loaded session owner, loading it if needed.
func (e *Engine) Session(ctx context.Context, sessionID id.ID) (*sessionpkg.Session, error) {
	return e.LoadSession(ctx, sessionID)
}

// LoadedSessions returns the live session owners currently held by the registry.
func (e *Engine) LoadedSessions() []*sessionpkg.Session {
	if e == nil || e.registry == nil {
		return nil
	}
	return e.registry.Loaded()
}

func (e *Engine) chatOwner(ctx context.Context, sessionID, chatID id.ID) (*chat.Chat, error) {
	if e == nil || e.registry == nil {
		return nil, fmt.Errorf("session registry is required")
	}
	return e.registry.Chat(ctx, sessionID, chatID)
}

func (e *Engine) chatByID(ctx context.Context, chatID id.ID) (domain.Chat, error) {
	if e == nil || e.registry == nil {
		return domain.Chat{}, fmt.Errorf("session registry is required")
	}
	return e.registry.ChatByID(ctx, chatID)
}

// Sessions returns persisted session metadata.
func (e *Engine) Sessions(ctx context.Context) ([]domain.Session, error) {
	if e == nil || e.registry == nil {
		return nil, fmt.Errorf("session registry is required")
	}
	return e.registry.List(ctx)
}

// CreateSession creates, configures, and loads a live session owner.
func (e *Engine) CreateSession(ctx context.Context, title, projectRoot string, createProjectRoot bool) (*sessionpkg.Session, error) {
	if e == nil || e.registry == nil {
		return nil, fmt.Errorf("session registry is required")
	}
	return e.registry.Create(ctx, title, projectRoot, createProjectRoot)
}

// DeleteSession closes any live runtimes and deletes the persisted session.
func (e *Engine) DeleteSession(ctx context.Context, sessionID id.ID) error {
	if e == nil || e.registry == nil {
		return fmt.Errorf("session registry is required")
	}
	return e.registry.Delete(ctx, sessionID)
}

func (e *Engine) Shutdown(ctx context.Context, reason chat.CancelReason) error {
	if e == nil || e.registry == nil {
		return nil
	}
	return e.registry.Shutdown(ctx, reason)
}
