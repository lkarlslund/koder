package agent

import (
	"context"
	"fmt"

	chatpkg "github.com/lkarlslund/koder/internal/chat"
	"github.com/lkarlslund/koder/internal/codexapp"
	"github.com/lkarlslund/koder/internal/domain"
)

// CodexModels probes the supervised app-server and returns its live model list.
func (e *Engine) CodexModels(ctx context.Context) ([]codexapp.Model, error) {
	if e == nil || !e.cfg.Codex.Enabled || e.codex == nil {
		return nil, fmt.Errorf("codex backend is disabled")
	}
	return e.codex.Models(ctx)
}

func (e *Engine) Chat(ctx context.Context, session domain.Session, chatRecord domain.Chat) (*chatpkg.Chat, error) {
	if chatRecord.ID == "" {
		return nil, fmt.Errorf("chat id is required")
	}
	return chatpkg.Load(ctx, session, chatRecord, e.ChatDeps(), nil)
}

func (e *Engine) ChatDeps() chatpkg.Deps {
	return chatpkg.Deps{
		Store:   e.store,
		Model:   e,
		Drivers: e,
		Tools:   e.toolsRuntime,
		Runtime: e.toolsRuntime,
		Life:    e.toolsRuntime,
		Pending: e.toolsRuntime,
		Compact: e,
	}
}

// DriverForChat selects the turn implementation from the chat's persisted
// backend. Additional backends are registered here without changing the chat
// actor or its user-facing lifecycle.
func (e *Engine) DriverForChat(chatRecord domain.Chat) chatpkg.TurnDriver {
	switch chatRecord.EffectiveBackend() {
	case domain.ChatBackendKoder:
		return chatpkg.NativeTurnDriver{Model: e}
	case domain.ChatBackendCodex:
		if e != nil && e.cfg.Codex.Enabled && e.codex != nil {
			return e.codex
		}
		return nil
	default:
		return nil
	}
}
