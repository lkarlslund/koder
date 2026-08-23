package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/lkarlslund/koder/internal/chat"
	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/id"
	"github.com/lkarlslund/koder/internal/offeredfile"
	sessionpkg "github.com/lkarlslund/koder/internal/session"
	"github.com/lkarlslund/koder/internal/settings"
	"github.com/lkarlslund/koder/internal/tools/chattool"
)

func (e *Engine) sessionRegistryConfig(defaults settings.NewSessionDefaults) sessionpkg.RegistryConfig {
	cfg := sessionpkg.RegistryConfig{
		DefaultProvider:   defaults.ProviderID,
		DefaultModel:      defaults.ModelID,
		PermissionProfile: defaults.PermissionProfile,
		AccessSettings:    defaults.Access,
		MaxChildChats:     defaults.MaxChildChats,
	}
	cfg.PrepareChatSpec = e.prepareChatCreateSpec
	if e != nil && e.browser != nil {
		cfg.OnChatArchived = e.browser.CleanupChat
	}
	codexEnabled := e != nil && e.cfg.Codex.Enabled
	cfg.BackendAvailable = func(backend domain.ChatBackend) error {
		if backend == domain.ChatBackendCodex && !codexEnabled {
			return fmt.Errorf("codex backend is disabled")
		}
		return nil
	}
	if e != nil && e.codex != nil {
		cfg.BeforeChatUpdate = func(ctx context.Context, before domain.Chat, update chattool.UpdateRequest) error {
			return e.codex.UpdateChat(ctx, before, update.Title, update.Archived)
		}
		cfg.BeforeChatDelete = e.codex.DeleteChat
	}
	return cfg
}

func (e *Engine) prepareChatCreateSpec(ctx context.Context, spec domain.ChatCreateSpec) (domain.ChatCreateSpec, error) {
	spec = spec.Normalized()
	if spec.Backend != domain.ChatBackendCodex || strings.TrimSpace(spec.ModelID) != "" {
		return spec, nil
	}
	models, err := e.CodexModels(ctx)
	if err != nil {
		return domain.ChatCreateSpec{}, fmt.Errorf("resolve default codex model: %w", err)
	}
	for _, model := range models {
		if model.IsDefault && !model.Hidden && strings.TrimSpace(model.ID) != "" {
			spec.ModelID = strings.TrimSpace(model.ID)
			return spec, nil
		}
	}
	for _, model := range models {
		if !model.Hidden && strings.TrimSpace(model.ID) != "" {
			spec.ModelID = strings.TrimSpace(model.ID)
			return spec, nil
		}
	}
	return domain.ChatCreateSpec{}, fmt.Errorf("resolve default codex model: app-server returned no usable models")
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

// ResolveOfferedFile loads a live-file capability by its opaque token.
func (e *Engine) ResolveOfferedFile(ctx context.Context, token string) (offeredfile.Record, error) {
	if e == nil || e.offeredFiles == nil {
		return offeredfile.Record{}, fmt.Errorf("offered file service is unavailable")
	}
	return e.offeredFiles.Resolve(ctx, token)
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

// CreateQuickSession creates a standalone one-chat session with a managed workspace.
func (e *Engine) CreateQuickSession(ctx context.Context) (*sessionpkg.Session, error) {
	return e.createQuickSession(ctx, false)
}

// CreateQuickVoiceSession creates a voice-orchestrated quick session with its
// own managed scratch workspace.
func (e *Engine) CreateQuickVoiceSession(ctx context.Context) (*sessionpkg.Session, error) {
	return e.createQuickSession(ctx, true)
}

// CreateQuickSessionWithSpec creates a managed one-chat session using the
// shared backend/role/interaction contract.
func (e *Engine) CreateQuickSessionWithSpec(ctx context.Context, spec domain.ChatCreateSpec) (*sessionpkg.Session, error) {
	if e == nil || e.registry == nil {
		return nil, fmt.Errorf("session registry is required")
	}
	sessionID := id.New()
	root := filepath.Join(e.cfg.StateDir(), "quick-chats", string(sessionID))
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, fmt.Errorf("create quick chat project root: %w", err)
	}
	owner, err := e.registry.CreateQuickWithSpec(ctx, sessionID, root, spec)
	if err != nil {
		_ = os.RemoveAll(root)
		return nil, err
	}
	return owner, nil
}

func (e *Engine) createQuickSession(ctx context.Context, voice bool) (*sessionpkg.Session, error) {
	if e == nil || e.registry == nil {
		return nil, fmt.Errorf("session registry is required")
	}
	sessionID := id.New()
	root := filepath.Join(e.cfg.StateDir(), "quick-chats", string(sessionID))
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, fmt.Errorf("create quick chat project root: %w", err)
	}
	var owner *sessionpkg.Session
	var err error
	if voice {
		owner, err = e.registry.CreateQuickVoice(ctx, sessionID, root)
	} else {
		owner, err = e.registry.CreateQuick(ctx, sessionID, root)
	}
	if err != nil {
		_ = os.RemoveAll(root)
		return nil, err
	}
	return owner, nil
}

// CreateVoiceSession creates a durable one-chat voice coordination session.
func (e *Engine) CreateVoiceSession(ctx context.Context, title string) (*sessionpkg.Session, error) {
	if e == nil || e.registry == nil {
		return nil, fmt.Errorf("session registry is required")
	}
	return e.registry.CreateVoice(ctx, title)
}

// DeleteSession closes any live runtimes and deletes the persisted session.
func (e *Engine) DeleteSession(ctx context.Context, sessionID id.ID) error {
	if e == nil || e.registry == nil {
		return fmt.Errorf("session registry is required")
	}
	owner, err := e.registry.Load(ctx, sessionID)
	if err != nil {
		return err
	}
	session := owner.Snapshot().Session
	managedRoot, err := e.managedQuickRoot(session)
	if err != nil {
		return err
	}
	if e.browser != nil {
		e.browser.CleanupSession(ctx, sessionID)
	}
	if err := e.registry.Delete(ctx, sessionID); err != nil {
		return err
	}
	var cleanupErrs []error
	if e.files != nil {
		cleanupErrs = append(cleanupErrs, e.files.DeleteSessionData(sessionID))
	}
	if e.offeredFiles != nil {
		cleanupErrs = append(cleanupErrs, e.offeredFiles.DeleteSession(ctx, sessionID))
	}
	if managedRoot != "" {
		if err := os.RemoveAll(managedRoot); err != nil {
			cleanupErrs = append(cleanupErrs, fmt.Errorf("delete managed quick chat root: %w", err))
		}
	}
	return errors.Join(cleanupErrs...)
}

func (e *Engine) managedQuickRoot(session domain.Session) (string, error) {
	if !session.ProjectRootManaged {
		return "", nil
	}
	if session.Kind != domain.SessionKindQuick {
		return "", fmt.Errorf("managed project root is only valid for quick sessions")
	}
	expected := filepath.Clean(filepath.Join(e.cfg.StateDir(), "quick-chats", string(session.ID)))
	actual := filepath.Clean(session.ProjectRoot)
	if actual != expected {
		return "", fmt.Errorf("refusing to delete unexpected managed project root %q", session.ProjectRoot)
	}
	return expected, nil
}

func (e *Engine) Shutdown(ctx context.Context, reason chat.CancelReason) error {
	if e == nil {
		return nil
	}
	var browserErr error
	if e.browser != nil {
		browserErr = e.browser.Stop(ctx)
	}
	var mcpErr error
	if e.mcp != nil {
		mcpErr = e.mcp.Close()
	}
	if e.registry == nil {
		if e.codex != nil {
			return errors.Join(browserErr, mcpErr, e.codex.Close())
		}
		return errors.Join(browserErr, mcpErr)
	}
	registryErr := e.registry.Shutdown(ctx, reason)
	var codexErr error
	if e.codex != nil {
		codexErr = e.codex.Close()
	}
	return errors.Join(browserErr, mcpErr, registryErr, codexErr)
}
