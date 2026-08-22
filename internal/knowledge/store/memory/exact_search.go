package memory

import (
	"context"

	knowledgeStore "github.com/lkarlslund/koder/internal/knowledge/store"
)

func (s *Store) SearchExact(ctx context.Context, request knowledgeStore.ExactSearchRequest) (knowledgeStore.ExactSearchPage, error) {
	if err := ctx.Err(); err != nil {
		return knowledgeStore.ExactSearchPage{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return knowledgeStore.ExactSearchPage{}, knowledgeStore.ErrClosed
	}
	records := make([]knowledgeStore.CanonicalRecord, 0,
		len(s.data.chunks)+len(s.data.entries)+len(s.data.links)+len(s.data.evidence))
	for _, value := range s.data.chunks {
		value := cloneChunk(value)
		records = append(records, knowledgeStore.CanonicalRecord{Kind: knowledgeStore.RecordKindChunk, Chunk: &value})
	}
	for _, value := range s.data.entries {
		value := cloneEntry(value)
		records = append(records, knowledgeStore.CanonicalRecord{Kind: knowledgeStore.RecordKindEntry, Entry: &value})
	}
	for _, value := range s.data.links {
		value := cloneLink(value)
		records = append(records, knowledgeStore.CanonicalRecord{Kind: knowledgeStore.RecordKindLink, Link: &value})
	}
	for _, value := range s.data.evidence {
		records = append(records, knowledgeStore.CanonicalRecord{Kind: knowledgeStore.RecordKindEvidence, Evidence: &value})
	}
	return knowledgeStore.PaginateExactSearch(records, request, s.indexGeneration)
}
