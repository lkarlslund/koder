package curation

import (
	"context"
	"fmt"

	"github.com/lkarlslund/koder/internal/knowledge"
	knowledgeService "github.com/lkarlslund/koder/internal/knowledge/service"
	knowledgeStore "github.com/lkarlslund/koder/internal/knowledge/store"
)

const defaultDeduplicationEntryLimit = 10_000

// ServiceEntrySource adapts authorized service reads to candidate deduplication.
type ServiceEntrySource struct {
	Service    *knowledgeService.Service
	EntryLimit int
}

func (s ServiceEntrySource) EntriesForDeduplication(ctx context.Context, chunkID knowledge.ChunkID) ([]knowledge.Entry, error) {
	if s.Service == nil {
		return nil, ErrUnavailable
	}
	limit := s.EntryLimit
	if limit <= 0 {
		limit = defaultDeduplicationEntryLimit
	}
	entries := make([]knowledge.Entry, 0, min(limit, 200))
	cursor := ""
	for {
		page, err := s.Service.ListEntries(ctx, knowledgeStore.EntryListRequest{
			Filter: knowledgeStore.EntryFilter{
				ChunkIDs: []knowledge.ChunkID{chunkID},
				States:   []knowledge.EntryState{knowledge.EntryStateActive, knowledge.EntryStateSuperseded},
			},
			Limit: min(200, limit-len(entries)), Cursor: cursor,
		})
		if err != nil {
			return nil, err
		}
		entries = append(entries, page.Entries...)
		if page.NextCursor == "" {
			return entries, nil
		}
		if len(entries) >= limit {
			return nil, fmt.Errorf("%w: candidate deduplication exceeds %d entries in one chunk", knowledge.ErrInvalidRecord, limit)
		}
		cursor = page.NextCursor
	}
}
