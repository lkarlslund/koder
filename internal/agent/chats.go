package agent

import (
	"context"
	"fmt"

	chatpkg "github.com/lkarlslund/koder/internal/chat"
	"github.com/lkarlslund/koder/internal/domain"
)

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
		Tools:   e.toolsRuntime,
		Runtime: e.toolsRuntime,
		Life:    e.toolsRuntime,
		Pending: e.toolsRuntime,
		Compact: e,
	}
}
