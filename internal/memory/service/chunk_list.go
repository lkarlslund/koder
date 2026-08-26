package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/lkarlslund/koder/internal/memory"
	memoryStoreAPI "github.com/lkarlslund/koder/internal/memory/store"
)

// ListChunks hides archived/draft chunks by default. Callers must explicitly provide
// lifecycle states when they need a different view.
func (s *Service) ListChunks(ctx context.Context, request memoryStoreAPI.ChunkListRequest) (memoryStoreAPI.ChunkPage, error) {
	if len(request.Filter.States) == 0 {
		request.Filter.States = []memory.ChunkState{memory.ChunkStateActive}
	}
	if chunkPolicyAllowsAll(s.chunkPolicy) {
		return s.store.ListChunks(ctx, request)
	}
	if err := ctx.Err(); err != nil {
		return memoryStoreAPI.ChunkPage{}, err
	}
	actor, err := s.actor(ctx)
	if err != nil {
		return memoryStoreAPI.ChunkPage{}, fmt.Errorf("resolve memory actor: %w", err)
	}
	if err := actor.Validate(); err != nil {
		return memoryStoreAPI.ChunkPage{}, err
	}
	limit := request.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		return memoryStoreAPI.ChunkPage{}, fmt.Errorf("%w: chunk page limit must not exceed 200", memory.ErrInvalidRecord)
	}

	// A policy may hide arbitrary chunks, so scan canonical pages one record at
	// a time. Advancing the store cursor past denied records preserves stable
	// pagination without revealing their existence or identifiers.
	scan := request
	scan.Limit = 1
	cursor := request.Cursor
	result := memoryStoreAPI.ChunkPage{Chunks: make([]memory.Chunk, 0, limit)}
	for len(result.Chunks) < limit {
		scan.Cursor = cursor
		page, err := s.store.ListChunks(ctx, scan)
		if err != nil {
			return memoryStoreAPI.ChunkPage{}, err
		}
		if len(page.Chunks) == 0 {
			break
		}
		chunk := page.Chunks[0]
		cursor = page.NextCursor
		if err := s.chunkPolicy.AuthorizeChunk(ctx, actor, ChunkPolicyRead, chunk); err == nil {
			result.Chunks = append(result.Chunks, chunk)
		} else if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return memoryStoreAPI.ChunkPage{}, err
		}
		if cursor == "" {
			break
		}
	}
	if len(result.Chunks) == limit {
		result.NextCursor = cursor
	}
	return result, nil
}

func chunkPolicyAllowsAll(policy ChunkPolicy) bool {
	switch policy.(type) {
	case AllowAllChunkPolicy, *AllowAllChunkPolicy:
		return true
	default:
		return false
	}
}
