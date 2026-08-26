package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/lkarlslund/koder/internal/memory"
	memoryStoreAPI "github.com/lkarlslund/koder/internal/memory/store"
)

// ListEntries hides drafts, superseded entries, and archives unless callers explicitly
// request lifecycle states.
func (s *Service) ListEntries(ctx context.Context, request memoryStoreAPI.EntryListRequest) (memoryStoreAPI.EntryPage, error) {
	if len(request.Filter.States) == 0 {
		request.Filter.States = []memory.EntryState{memory.EntryStateActive}
	}
	if chunkPolicyAllowsAll(s.chunkPolicy) {
		return s.store.ListEntries(ctx, request)
	}
	if err := ctx.Err(); err != nil {
		return memoryStoreAPI.EntryPage{}, err
	}
	actor, err := s.actor(ctx)
	if err != nil {
		return memoryStoreAPI.EntryPage{}, fmt.Errorf("resolve memory actor: %w", err)
	}
	if err := actor.Validate(); err != nil {
		return memoryStoreAPI.EntryPage{}, err
	}
	limit := request.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		return memoryStoreAPI.EntryPage{}, fmt.Errorf("%w: entry page limit must not exceed 200", memory.ErrInvalidRecord)
	}
	scan := request
	scan.Limit = 1
	cursor := request.Cursor
	result := memoryStoreAPI.EntryPage{Entries: make([]memory.Entry, 0, limit)}
	decisions := make(map[memory.ChunkID]bool)
	for len(result.Entries) < limit {
		scan.Cursor = cursor
		page, err := s.store.ListEntries(ctx, scan)
		if err != nil {
			return memoryStoreAPI.EntryPage{}, err
		}
		if len(page.Entries) == 0 {
			break
		}
		entry := page.Entries[0]
		cursor = page.NextCursor
		allowed, known := decisions[entry.ChunkID]
		if !known {
			var chunk memory.Chunk
			err := s.store.View(ctx, func(tx memoryStoreAPI.ReadTx) error {
				var err error
				chunk, err = tx.Chunk(ctx, entry.ChunkID)
				return err
			})
			if err != nil {
				return memoryStoreAPI.EntryPage{}, err
			}
			err = s.chunkPolicy.AuthorizeChunk(ctx, actor, ChunkPolicyRead, chunk)
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return memoryStoreAPI.EntryPage{}, err
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
