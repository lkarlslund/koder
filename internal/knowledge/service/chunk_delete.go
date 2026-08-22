package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/lkarlslund/koder/internal/knowledge"
	knowledgeStore "github.com/lkarlslund/koder/internal/knowledge/store"
)

var (
	ErrDeleteConfirmationRequired = errors.New("knowledge deletion requires confirmation")
	ErrChunkMustBeArchived        = errors.New("knowledge chunk must be archived before deletion")
	ErrChunkNotEmpty              = errors.New("knowledge chunk is not empty")
)

type DeleteChunkRequest struct {
	ChunkID          knowledge.ChunkID
	ExpectedRevision uint64
	Confirmed        bool
}

// DeleteChunk permanently removes one archived, empty chunk and its revision history.
// Confirmation is deliberately part of the service contract so every future caller—not
// only a particular UI—must opt in to the destructive operation.
func (s *Service) DeleteChunk(ctx context.Context, request DeleteChunkRequest) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if request.ChunkID == "" || request.ExpectedRevision == 0 {
		return fmt.Errorf("%w: chunk ID and expected revision are required", knowledge.ErrInvalidRecord)
	}
	if !request.Confirmed {
		return ErrDeleteConfirmationRequired
	}
	err := s.store.Update(ctx, func(tx knowledgeStore.WriteTx) error {
		current, err := tx.Chunk(ctx, request.ChunkID)
		if err != nil {
			return err
		}
		if current.Revision.Number != request.ExpectedRevision {
			return fmt.Errorf("%w: chunk %s expected revision %d, current revision %d", knowledgeStore.ErrConflict, request.ChunkID, request.ExpectedRevision, current.Revision.Number)
		}
		if current.State != knowledge.ChunkStateArchived {
			return fmt.Errorf("%w: archive chunk %s before deleting it", ErrChunkMustBeArchived, request.ChunkID)
		}
		if current.Counts != (knowledge.ChunkCounts{}) || len(current.DependencyIDs) != 0 {
			return fmt.Errorf("%w: chunk %s has entries, links, evidence, or declared dependencies", ErrChunkNotEmpty, request.ChunkID)
		}
		return tx.DeleteChunk(ctx, request.ChunkID, request.ExpectedRevision)
	})
	if err != nil {
		return fmt.Errorf("delete knowledge chunk: %w", err)
	}
	return nil
}
