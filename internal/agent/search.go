package agent

import (
	"context"

	chatpkg "github.com/lkarlslund/koder/internal/chat"
	"github.com/lkarlslund/koder/internal/id"
)

// SearchPersistedSessions searches chat metadata and transcript storage without
// loading sessions or chat runtimes.
func (e *Engine) SearchPersistedSessions(ctx context.Context, sessionIDs []id.ID, query string) (map[id.ID]bool, error) {
	source := chatpkg.NewSource(func() chatpkg.Deps { return chatpkg.Deps{Store: e.store} })
	return source.SearchSessions(ctx, sessionIDs, query)
}
