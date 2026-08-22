package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/lkarlslund/koder/internal/knowledge"
	knowledgeStore "github.com/lkarlslund/koder/internal/knowledge/store"
)

// ListEntries hides drafts, superseded entries, and archives unless callers explicitly
// request lifecycle states.
func (s *Service) ListEntries(ctx context.Context, request knowledgeStore.EntryListRequest) (knowledgeStore.EntryPage, error) {
	if len(request.Filter.States) == 0 {
		request.Filter.States = []knowledge.EntryState{knowledge.EntryStateActive}
	}
	if chunkPolicyAllowsAll(s.chunkPolicy) {
		return s.store.ListEntries(ctx, request)
	}
	if err := ctx.Err(); err != nil {
		return knowledgeStore.EntryPage{}, err
	}
	actor, err := s.actor(ctx)
	if err != nil {
		return knowledgeStore.EntryPage{}, fmt.Errorf("resolve knowledge actor: %w", err)
	}
	if err := actor.Validate(); err != nil {
		return knowledgeStore.EntryPage{}, err
	}
	limit := request.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		return knowledgeStore.EntryPage{}, fmt.Errorf("%w: entry page limit must not exceed 200", knowledge.ErrInvalidRecord)
	}
	scan := request
	scan.Limit = 1
	cursor := request.Cursor
	result := knowledgeStore.EntryPage{Entries: make([]knowledge.Entry, 0, limit)}
	decisions := make(map[knowledge.ChunkID]bool)
	for len(result.Entries) < limit {
		scan.Cursor = cursor
		page, err := s.store.ListEntries(ctx, scan)
		if err != nil {
			return knowledgeStore.EntryPage{}, err
		}
		if len(page.Entries) == 0 {
			break
		}
		entry := page.Entries[0]
		cursor = page.NextCursor
		allowed, known := decisions[entry.ChunkID]
		if !known {
			chunk, err := s.Chunk(ctx, entry.ChunkID)
			if err != nil {
				return knowledgeStore.EntryPage{}, err
			}
			err = s.chunkPolicy.AuthorizeChunk(ctx, actor, ChunkPolicyRead, chunk)
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return knowledgeStore.EntryPage{}, err
			}
			allowed = err == nil
			decisions[entry.ChunkID] = allowed
		}
		if allowed {
			result.Entries = append(result.Entries, entry)
		}
		if cursor == "" {
			break
		}
	}
	if len(result.Entries) == limit {
		result.NextCursor = cursor
	}
	return result, nil
}
