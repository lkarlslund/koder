package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/lkarlslund/koder/internal/knowledge"
	knowledgeStore "github.com/lkarlslund/koder/internal/knowledge/store"
)

var ErrInvalidLifecycleTransition = errors.New("invalid knowledge lifecycle transition")

type ChunkLifecycleRequest struct {
	ChunkID          knowledge.ChunkID
	ExpectedRevision uint64
	Reason           string
}

type ChunkLifecycleResult struct {
	Chunk   knowledge.Chunk
	Updated bool
}

func (s *Service) ArchiveChunk(ctx context.Context, request ChunkLifecycleRequest) (ChunkLifecycleResult, error) {
	return s.changeChunkState(ctx, request, knowledge.ChunkStateArchived)
}

func (s *Service) RestoreChunk(ctx context.Context, request ChunkLifecycleRequest) (ChunkLifecycleResult, error) {
	return s.changeChunkState(ctx, request, knowledge.ChunkStateActive)
}

func (s *Service) changeChunkState(ctx context.Context, request ChunkLifecycleRequest, target knowledge.ChunkState) (ChunkLifecycleResult, error) {
	if err := ctx.Err(); err != nil {
		return ChunkLifecycleResult{}, err
	}
	if request.ChunkID == "" || request.ExpectedRevision == 0 {
		return ChunkLifecycleResult{}, fmt.Errorf("%w: chunk ID and expected revision are required", knowledge.ErrInvalidRecord)
	}
	if target != knowledge.ChunkStateArchived && target != knowledge.ChunkStateActive {
		return ChunkLifecycleResult{}, fmt.Errorf("%w: unsupported target state %q", ErrInvalidLifecycleTransition, target)
	}
	result := ChunkLifecycleResult{}
	actor, err := s.actor(ctx)
	if err != nil {
		return ChunkLifecycleResult{}, fmt.Errorf("resolve knowledge actor: %w", err)
	}
	if err := actor.Validate(); err != nil {
		return ChunkLifecycleResult{}, err
	}
	err = s.store.Update(ctx, func(tx knowledgeStore.WriteTx) error {
		current, err := tx.Chunk(ctx, request.ChunkID)
		if err != nil {
			return err
		}
		action := ChunkPolicyArchive
		if target == knowledge.ChunkStateActive {
			action = ChunkPolicyRestore
		}
		if err := s.authorizeChunk(ctx, actor, action, current); err != nil {
			return err
		}
		if current.Revision.Number != request.ExpectedRevision {
			return fmt.Errorf("%w: chunk %s expected revision %d, current revision %d", knowledgeStore.ErrConflict, request.ChunkID, request.ExpectedRevision, current.Revision.Number)
		}
		if current.ID == PersonalMeChunkID && target == knowledge.ChunkStateArchived {
			return fmt.Errorf("%w: personal/me cannot be archived", ErrProtectedChunk)
		}
		if current.State == target {
			result.Chunk = current
			return nil
		}
		if target == knowledge.ChunkStateActive && current.State != knowledge.ChunkStateArchived {
			return fmt.Errorf("%w: chunk %s in state %q cannot be restored", ErrInvalidLifecycleTransition, request.ChunkID, current.State)
		}
		if target == knowledge.ChunkStateArchived && current.State != knowledge.ChunkStateActive && current.State != knowledge.ChunkStateDraft {
			return fmt.Errorf("%w: chunk %s in state %q cannot be archived", ErrInvalidLifecycleTransition, request.ChunkID, current.State)
		}
		now := s.now().UTC().Round(0)
		if !now.After(current.UpdatedAt) {
			now = current.UpdatedAt.Add(time.Nanosecond)
		}
		reason := request.Reason
		if reason == "" {
			if target == knowledge.ChunkStateArchived {
				reason = "archive chunk"
			} else {
				reason = "restore chunk"
			}
		}
		next := current
		next.State = target
		next.UpdatedAt = now
		next.Revision = knowledge.Revision{
			Number: current.Revision.Number + 1,
			ID:     knowledge.RevisionID(s.newID()), Actor: actor, Reason: reason, CreatedAt: now,
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
		return ChunkLifecycleResult{}, fmt.Errorf("change knowledge chunk lifecycle: %w", err)
	}
	if result.Updated {
		kind := MutationArchived
		if target == knowledge.ChunkStateActive {
			kind = MutationRestored
		}
		s.publishMutation(ctx, chunkMutation(kind, result.Chunk))
	}
	return result, nil
}
