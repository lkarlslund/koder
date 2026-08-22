package service

import (
	"context"

	"github.com/lkarlslund/koder/internal/knowledge"
	knowledgeStore "github.com/lkarlslund/koder/internal/knowledge/store"
)

// ListChunks hides archived/draft chunks by default. Callers must explicitly provide
// lifecycle states when they need a different view.
func (s *Service) ListChunks(ctx context.Context, request knowledgeStore.ChunkListRequest) (knowledgeStore.ChunkPage, error) {
	if len(request.Filter.States) == 0 {
		request.Filter.States = []knowledge.ChunkState{knowledge.ChunkStateActive}
	}
	return s.store.ListChunks(ctx, request)
}
