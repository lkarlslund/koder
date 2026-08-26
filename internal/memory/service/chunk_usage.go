package service

import (
	"context"
	"fmt"

	"github.com/lkarlslund/koder/internal/memory"
	memoryStoreAPI "github.com/lkarlslund/koder/internal/memory/store"
)

// MarkChunkUsed records successful memory use without changing the chunk's content
// revision or updated timestamp. Callers should invoke it only when retrieved memory
// contributes to work, not for passive list/detail inspection.
func (s *Service) MarkChunkUsed(ctx context.Context, chunkID memory.ChunkID) (memory.Chunk, error) {
	if err := ctx.Err(); err != nil {
		return memory.Chunk{}, err
	}
	if chunkID == "" {
		return memory.Chunk{}, fmt.Errorf("%w: chunk ID is required", memory.ErrInvalidRecord)
	}
	actor, err := s.actor(ctx)
	if err != nil {
		return memory.Chunk{}, fmt.Errorf("resolve memory actor: %w", err)
	}
	if err := actor.Validate(); err != nil {
		return memory.Chunk{}, err
	}
	var result memory.Chunk
	err = s.store.Update(ctx, func(tx memoryStoreAPI.WriteTx) error {
		chunk, err := tx.Chunk(ctx, chunkID)
		if err != nil {
			return err
		}
		if err := s.authorizeChunk(ctx, actor, ChunkPolicySearch, chunk); err != nil {
			return err
		}
		if err := tx.TouchChunk(ctx, chunkID, s.now().UTC().Round(0)); err != nil {
			return err
		}
		result, err = tx.Chunk(ctx, chunkID)
		return err
	})
	if err != nil {
		return memory.Chunk{}, fmt.Errorf("mark memory chunk used: %w", err)
	}
	return result, nil
}
