package memory

import (
	"context"

	memoryStoreAPI "github.com/lkarlslund/koder/internal/memory/store"
)

func (s *Store) SearchExact(ctx context.Context, request memoryStoreAPI.ExactSearchRequest) (memoryStoreAPI.ExactSearchPage, error) {
	if err := ctx.Err(); err != nil {
		return memoryStoreAPI.ExactSearchPage{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return memoryStoreAPI.ExactSearchPage{}, memoryStoreAPI.ErrClosed
	}
	records := make([]memoryStoreAPI.CanonicalRecord, 0,
		len(s.data.chunks)+len(s.data.entries)+len(s.data.links)+len(s.data.evidence))
	for _, value := range s.data.chunks {
		value := cloneChunk(value)
		records = append(records, memoryStoreAPI.CanonicalRecord{Kind: memoryStoreAPI.RecordKindChunk, Chunk: &value})
	}
	for _, value := range s.data.entries {
		value := cloneEntry(value)
		records = append(records, memoryStoreAPI.CanonicalRecord{Kind: memoryStoreAPI.RecordKindEntry, Entry: &value})
	}
	for _, value := range s.data.links {
		value := cloneLink(value)
		records = append(records, memoryStoreAPI.CanonicalRecord{Kind: memoryStoreAPI.RecordKindLink, Link: &value})
	}
	for _, value := range s.data.evidence {
		records = append(records, memoryStoreAPI.CanonicalRecord{Kind: memoryStoreAPI.RecordKindEvidence, Evidence: &value})
	}
	return memoryStoreAPI.PaginateExactSearch(records, request, s.indexGeneration)
}
