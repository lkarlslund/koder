package memory

import (
	"context"

	knowledgeStore "github.com/lkarlslund/koder/internal/knowledge/store"
)

func (s *Store) LookupLexicalPostings(ctx context.Context, request knowledgeStore.LexicalPostingRequest) (knowledgeStore.LexicalPostingPage, error) {
	if err := ctx.Err(); err != nil {
		return knowledgeStore.LexicalPostingPage{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return knowledgeStore.LexicalPostingPage{}, knowledgeStore.ErrClosed
	}
	postings := make([]knowledgeStore.LexicalPosting, 0, len(s.data.entries)*4)
	for _, entry := range s.data.entries {
		if err := ctx.Err(); err != nil {
			return knowledgeStore.LexicalPostingPage{}, err
		}
		postings = append(postings, knowledgeStore.EntryLexicalPostings(entry)...)
	}
	return knowledgeStore.PaginateLexicalPostings(postings, request, s.indexGeneration)
}
