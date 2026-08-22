package service

import (
	"context"
	"fmt"

	"github.com/lkarlslund/koder/internal/knowledge"
	knowledgeStore "github.com/lkarlslund/koder/internal/knowledge/store"
)

// MarkChunkUsed records successful knowledge use without changing the chunk's content
// revision or updated timestamp. Callers should invoke it only when retrieved knowledge
// contributes to work, not for passive list/detail inspection.
func (s *Service) MarkChunkUsed(ctx context.Context, chunkID knowledge.ChunkID) (knowledge.Chunk, error) {
	if err := ctx.Err(); err != nil {
		return knowledge.Chunk{}, err
	}
	if chunkID == "" {
		return knowledge.Chunk{}, fmt.Errorf("%w: chunk ID is required", knowledge.ErrInvalidRecord)
	}
	actor, err := s.actor(ctx)
	if err != nil {
		return knowledge.Chunk{}, fmt.Errorf("resolve knowledge actor: %w", err)
	}
	if err := actor.Validate(); err != nil {
		return knowledge.Chunk{}, err
	}
	var result knowledge.Chunk
	err = s.store.Update(ctx, func(tx knowledgeStore.WriteTx) error {
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
		return knowledge.Chunk{}, fmt.Errorf("mark knowledge chunk used: %w", err)
	}
	return result, nil
}
