package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/lkarlslund/koder/internal/memory"
	memoryStoreAPI "github.com/lkarlslund/koder/internal/memory/store"
)

var (
	ErrDeleteConfirmationRequired = errors.New("memory deletion requires confirmation")
	ErrChunkMustBeArchived        = errors.New("memory chunk must be archived before deletion")
	ErrChunkNotEmpty              = errors.New("memory chunk is not empty")
)

// ChunkDeletionBlockedError exposes exact canonical blockers without requiring callers
// to parse an error string.
type ChunkDeletionBlockedError struct {
	ChunkID  memory.ChunkID
	Blockers memoryStoreAPI.ChunkDeletionBlockers
}

func (e *ChunkDeletionBlockedError) Error() string {
	return fmt.Sprintf("%s: chunk %s has %d entries, %d links, %d exclusively owned evidence records, %d dependencies, %d dependent chunks, and reported counts entries=%d links=%d evidence=%d",
		ErrChunkNotEmpty, e.ChunkID, len(e.Blockers.EntryIDs), len(e.Blockers.LinkIDs), len(e.Blockers.EvidenceIDs), len(e.Blockers.DependencyIDs),
		len(e.Blockers.DependentChunkIDs), e.Blockers.ReportedCounts.Entries, e.Blockers.ReportedCounts.Links, e.Blockers.ReportedCounts.Evidence)
}

func (e *ChunkDeletionBlockedError) Unwrap() error { return ErrChunkNotEmpty }

type DeleteChunkRequest struct {
	ChunkID          memory.ChunkID
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
	actor, err := s.actor(ctx)
	if err != nil {
		return fmt.Errorf("resolve memory actor: %w", err)
	}
	if err := actor.Validate(); err != nil {
		return err
	}
	var deleted memory.Chunk
	err = s.store.Update(ctx, func(tx memoryStoreAPI.WriteTx) error {
		current, blockers, err := s.chunkDeletionTarget(ctx, tx, request, actor, ChunkPolicyDelete)
		if err != nil {
			return err
		}
		if !blockers.Empty() {
			return &ChunkDeletionBlockedError{ChunkID: request.ChunkID, Blockers: blockers}
		}
		deleted = current
		return tx.DeleteChunk(ctx, request.ChunkID, request.ExpectedRevision)
	})
	if err != nil {
		return fmt.Errorf("delete memory chunk: %w", err)
	}
	s.publishMutation(ctx, chunkMutation(MutationDeleted, deleted))
	return nil
}

func validateDeleteChunkRequest(ctx context.Context, request DeleteChunkRequest) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if request.ChunkID == "" || request.ExpectedRevision == 0 {
		return fmt.Errorf("%w: chunk ID and expected revision are required", memory.ErrInvalidRecord)
	}
	if !request.Confirmed {
		return ErrDeleteConfirmationRequired
	}
	return nil
}

func (s *Service) chunkDeletionTarget(ctx context.Context, tx memoryStoreAPI.WriteTx, request DeleteChunkRequest, actor memory.Actor, action ChunkPolicyAction) (memory.Chunk, memoryStoreAPI.ChunkDeletionBlockers, error) {
	current, err := tx.Chunk(ctx, request.ChunkID)
	if err != nil {
		return memory.Chunk{}, memoryStoreAPI.ChunkDeletionBlockers{}, err
	}
	if err := s.authorizeChunk(ctx, actor, action, current); err != nil {
		return memory.Chunk{}, memoryStoreAPI.ChunkDeletionBlockers{}, err
	}
	if current.Revision.Number != request.ExpectedRevision {
		return memory.Chunk{}, memoryStoreAPI.ChunkDeletionBlockers{}, fmt.Errorf("%w: chunk %s expected revision %d, current revision %d", memoryStoreAPI.ErrConflict, request.ChunkID, request.ExpectedRevision, current.Revision.Number)
	}
	if current.ID == PersonalMeChunkID {
		return memory.Chunk{}, memoryStoreAPI.ChunkDeletionBlockers{}, fmt.Errorf("%w: personal/me cannot be deleted", ErrProtectedChunk)
	}
	if current.State != memory.ChunkStateArchived {
		return memory.Chunk{}, memoryStoreAPI.ChunkDeletionBlockers{}, fmt.Errorf("%w: archive chunk %s before deleting it", ErrChunkMustBeArchived, request.ChunkID)
	}
	blockers, err := tx.ChunkDeletionBlockers(ctx, request.ChunkID)
	if err != nil {
		return memory.Chunk{}, memoryStoreAPI.ChunkDeletionBlockers{}, err
	}
	return current, blockers, nil
}
