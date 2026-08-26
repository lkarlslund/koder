package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/lkarlslund/koder/internal/memory"
	memoryStoreAPI "github.com/lkarlslund/koder/internal/memory/store"
)

var ErrInvalidLifecycleTransition = errors.New("invalid memory lifecycle transition")

type ChunkLifecycleRequest struct {
	ChunkID          memory.ChunkID
	ExpectedRevision uint64
	Reason           string
}

type ChunkLifecycleResult struct {
	Chunk   memory.Chunk
	Updated bool
}

func (s *Service) ArchiveChunk(ctx context.Context, request ChunkLifecycleRequest) (ChunkLifecycleResult, error) {
	return s.changeChunkState(ctx, request, memory.ChunkStateArchived)
}

func (s *Service) RestoreChunk(ctx context.Context, request ChunkLifecycleRequest) (ChunkLifecycleResult, error) {
	return s.changeChunkState(ctx, request, memory.ChunkStateActive)
}

func (s *Service) changeChunkState(ctx context.Context, request ChunkLifecycleRequest, target memory.ChunkState) (ChunkLifecycleResult, error) {
	if err := ctx.Err(); err != nil {
		return ChunkLifecycleResult{}, err
	}
	if request.ChunkID == "" || request.ExpectedRevision == 0 {
		return ChunkLifecycleResult{}, fmt.Errorf("%w: chunk ID and expected revision are required", memory.ErrInvalidRecord)
	}
	if target != memory.ChunkStateArchived && target != memory.ChunkStateActive {
		return ChunkLifecycleResult{}, fmt.Errorf("%w: unsupported target state %q", ErrInvalidLifecycleTransition, target)
	}
	result := ChunkLifecycleResult{}
	actor, err := s.actor(ctx)
	if err != nil {
		return ChunkLifecycleResult{}, fmt.Errorf("resolve memory actor: %w", err)
	}
	if err := actor.Validate(); err != nil {
		return ChunkLifecycleResult{}, err
	}
	err = s.store.Update(ctx, func(tx memoryStoreAPI.WriteTx) error {
		current, err := tx.Chunk(ctx, request.ChunkID)
		if err != nil {
			return err
		}
		action := ChunkPolicyArchive
		if target == memory.ChunkStateActive {
			action = ChunkPolicyRestore
		}
		if err := s.authorizeChunk(ctx, actor, action, current); err != nil {
			return err
		}
		if current.Revision.Number != request.ExpectedRevision {
			return fmt.Errorf("%w: chunk %s expected revision %d, current revision %d", memoryStoreAPI.ErrConflict, request.ChunkID, request.ExpectedRevision, current.Revision.Number)
		}
		if current.ID == PersonalMeChunkID && target == memory.ChunkStateArchived {
			return fmt.Errorf("%w: personal/me cannot be archived", ErrProtectedChunk)
		}
		if current.State == target {
			result.Chunk = current
			return nil
		}
		if target == memory.ChunkStateActive && current.State != memory.ChunkStateArchived {
			return fmt.Errorf("%w: chunk %s in state %q cannot be restored", ErrInvalidLifecycleTransition, request.ChunkID, current.State)
		}
		if target == memory.ChunkStateArchived && current.State != memory.ChunkStateActive && current.State != memory.ChunkStateDraft {
			return fmt.Errorf("%w: chunk %s in state %q cannot be archived", ErrInvalidLifecycleTransition, request.ChunkID, current.State)
		}
		now := s.now().UTC().Round(0)
		if !now.After(current.UpdatedAt) {
			now = current.UpdatedAt.Add(time.Nanosecond)
		}
		reason := request.Reason
		if reason == "" {
			if target == memory.ChunkStateArchived {
				reason = "archive chunk"
			} else {
				reason = "restore chunk"
			}
		}
		next := current
		next.State = target
		if err := validateHighRiskChunkPolicy(next); err != nil {
			return err
		}
		next.UpdatedAt = now
		next.Revision = memory.Revision{
			Number: current.Revision.Number + 1,
			ID:     memory.RevisionID(s.newID()), Actor: actor, Reason: reason, CreatedAt: now,
		}
		if err := next.Validate(); err != nil {
			return err
		}
		if err := tx.PutChunk(ctx, next, current.Revision.Number); err != nil {
			return err
		}
		result.Chunk = next
		result.Updated = true
		return nil
	})
	if err != nil {
		return ChunkLifecycleResult{}, fmt.Errorf("change memory chunk lifecycle: %w", err)
	}
	if result.Updated {
		kind := MutationArchived
		if target == memory.ChunkStateActive {
			kind = MutationRestored
		}
		s.publishMutation(ctx, chunkMutation(kind, result.Chunk))
	}
	return result, nil
}
