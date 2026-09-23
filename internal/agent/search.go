package agent

import (
	"context"

	chatpkg "github.com/lkarlslund/koder/internal/chat"
	"github.com/lkarlslund/koder/internal/id"
)

// PersistedSessionMatches searches chat metadata and transcript storage without
// loading the session or any chat runtime.
func (e *Engine) PersistedSessionMatches(ctx context.Context, sessionID id.ID, query string) (bool, error) {
	source := chatpkg.NewSource(func() chatpkg.Deps { return chatpkg.Deps{Store: e.store} })
	return source.SessionMatches(ctx, sessionID, query)
}
