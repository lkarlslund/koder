package memory

import (
	"context"

	memoryStoreAPI "github.com/lkarlslund/koder/internal/memory/store"
)

func (s *Store) LookupLexicalPostings(ctx context.Context, request memoryStoreAPI.LexicalPostingRequest) (memoryStoreAPI.LexicalPostingPage, error) {
	if err := ctx.Err(); err != nil {
		return memoryStoreAPI.LexicalPostingPage{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return memoryStoreAPI.LexicalPostingPage{}, memoryStoreAPI.ErrClosed
	}
	postings := make([]memoryStoreAPI.LexicalPosting, 0, len(s.data.entries)*4)
	for _, entry := range s.data.entries {
		if err := ctx.Err(); err != nil {
			return memoryStoreAPI.LexicalPostingPage{}, err
		}
		postings = append(postings, memoryStoreAPI.EntryLexicalPostings(entry)...)
	}
	return memoryStoreAPI.PaginateLexicalPostings(postings, request, s.indexGeneration, uint64(len(s.data.entries)))
}
