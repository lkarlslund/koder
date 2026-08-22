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

// LookupLexicalPostings exposes normalized posting candidates to the retrieval pipeline.
// Scoring and policy filtering are applied by later search stages, not by this index API.
func (s *Service) LookupLexicalPostings(ctx context.Context, request knowledgeStore.LexicalPostingRequest) (knowledgeStore.LexicalPostingPage, error) {
	return s.store.LookupLexicalPostings(ctx, request)
}
