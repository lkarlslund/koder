package service

import (
	"context"

	knowledgeStore "github.com/lkarlslund/koder/internal/knowledge/store"
)

// SearchExact resolves canonical IDs and normalized titles, aliases, and tags. Broader
// lexical and graph-expanded retrieval is layered on top of this high-precision primitive.
func (s *Service) SearchExact(ctx context.Context, request knowledgeStore.ExactSearchRequest) (knowledgeStore.ExactSearchPage, error) {
	return s.store.SearchExact(ctx, request)
}
