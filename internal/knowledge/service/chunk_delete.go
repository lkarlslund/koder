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

// ChunkDeletionBlockedError exposes exact canonical blockers without requiring callers
// to parse an error string.
type ChunkDeletionBlockedError struct {
	ChunkID  knowledge.ChunkID
	Blockers knowledgeStore.ChunkDeletionBlockers
}

func (e *ChunkDeletionBlockedError) Error() string {
	return fmt.Sprintf("%s: chunk %s has %d entries, %d links, %d exclusively owned evidence records, %d dependencies, %d dependent chunks, and reported counts entries=%d links=%d evidence=%d",
		ErrChunkNotEmpty, e.ChunkID, len(e.Blockers.EntryIDs), len(e.Blockers.LinkIDs), len(e.Blockers.EvidenceIDs), len(e.Blockers.DependencyIDs),
		len(e.Blockers.DependentChunkIDs), e.Blockers.ReportedCounts.Entries, e.Blockers.ReportedCounts.Links, e.Blockers.ReportedCounts.Evidence)
}

func (e *ChunkDeletionBlockedError) Unwrap() error { return ErrChunkNotEmpty }

type DeleteChunkRequest struct {
	ChunkID          knowledge.ChunkID
	ExpectedRevision uint64
	Confirmed        bool
}

// DeleteChunk permanently removes one archived, empty chunk and its revision history.
// Confirmation is deliberately part of the service contract so every future caller—not
// only a particular UI—must opt in to the destructive operation.
func (s *Service) DeleteChunk(ctx context.Context, request DeleteChunkRequest) error {
	if err := validateDeleteChunkRequest(ctx, request); err != nil {
		return err
	}
	err := s.store.Update(ctx, func(tx knowledgeStore.WriteTx) error {
		_, blockers, err := chunkDeletionTarget(ctx, tx, request)
		if err != nil {
			return err
		}
		if !blockers.Empty() {
			return &ChunkDeletionBlockedError{ChunkID: request.ChunkID, Blockers: blockers}
		}
		return tx.DeleteChunk(ctx, request.ChunkID, request.ExpectedRevision)
	})
	if err != nil {
		return fmt.Errorf("delete knowledge chunk: %w", err)
	}
	return nil
}

func validateDeleteChunkRequest(ctx context.Context, request DeleteChunkRequest) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if request.ChunkID == "" || request.ExpectedRevision == 0 {
		return fmt.Errorf("%w: chunk ID and expected revision are required", knowledge.ErrInvalidRecord)
	}
	if !request.Confirmed {
		return ErrDeleteConfirmationRequired
	}
	return nil
}

func chunkDeletionTarget(ctx context.Context, tx knowledgeStore.WriteTx, request DeleteChunkRequest) (knowledge.Chunk, knowledgeStore.ChunkDeletionBlockers, error) {
	current, err := tx.Chunk(ctx, request.ChunkID)
	if err != nil {
		return knowledge.Chunk{}, knowledgeStore.ChunkDeletionBlockers{}, err
	}
	if current.Revision.Number != request.ExpectedRevision {
		return knowledge.Chunk{}, knowledgeStore.ChunkDeletionBlockers{}, fmt.Errorf("%w: chunk %s expected revision %d, current revision %d", knowledgeStore.ErrConflict, request.ChunkID, request.ExpectedRevision, current.Revision.Number)
	}
	if current.State != knowledge.ChunkStateArchived {
		return knowledge.Chunk{}, knowledgeStore.ChunkDeletionBlockers{}, fmt.Errorf("%w: archive chunk %s before deleting it", ErrChunkMustBeArchived, request.ChunkID)
	}
	blockers, err := tx.ChunkDeletionBlockers(ctx, request.ChunkID)
	if err != nil {
		return knowledge.Chunk{}, knowledgeStore.ChunkDeletionBlockers{}, err
	}
	return current, blockers, nil
}
