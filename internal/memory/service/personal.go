package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/lkarlslund/koder/internal/memory"
	memoryStoreAPI "github.com/lkarlslund/koder/internal/memory/store"
)

const PersonalMeChunkID memory.ChunkID = "00000000-0000-7000-8000-000000000001"

var ErrProtectedChunk = errors.New("protected memory chunk")

type EnsurePersonalChunkResult struct {
	Chunk   memory.Chunk
	Created bool
}

// EnsurePersonalChunk creates Koder's stable private personal container. It deliberately
// creates no entries: facts about a person require explicit later ingestion policy.
func (s *Service) EnsurePersonalChunk(ctx context.Context) (EnsurePersonalChunkResult, error) {
	if err := ctx.Err(); err != nil {
		return EnsurePersonalChunkResult{}, err
	}
	result := EnsurePersonalChunkResult{}
	err := s.store.Update(ctx, func(tx memoryStoreAPI.WriteTx) error {
		current, err := tx.Chunk(ctx, PersonalMeChunkID)
		switch {
		case err == nil:
			if !isCanonicalPersonalMe(current) {
				return fmt.Errorf("%w: reserved personal/me identity has incompatible metadata", ErrProtectedChunk)
			}
			result.Chunk = current
			return nil
		case !errors.Is(err, memoryStoreAPI.ErrNotFound):
			return err
		}
		actor, err := s.actor(ctx)
		if err != nil {
			return fmt.Errorf("resolve memory actor: %w", err)
		}
		if err := actor.Validate(); err != nil {
			return err
		}
		now := s.now().UTC().Round(0)
		chunk := memory.Chunk{
			ID: PersonalMeChunkID, Title: "About me", Kind: memory.ChunkKindPersonal,
			Scope:      memory.Scope{Kind: memory.ScopeKindPersonal, Selector: "me"},
			Visibility: memory.VisibilityPrivate, State: memory.ChunkStateActive, SchemaVersion: 1,
			Revision: memory.Revision{
				Number: 1, ID: memory.RevisionID(s.newID()), Actor: actor,
				Reason: "seed personal/me chunk", CreatedAt: now,
			},
			CreatedAt: now, UpdatedAt: now,
		}
		if err := chunk.Validate(); err != nil {
			return err
		}
		if err := tx.PutChunk(ctx, chunk, 0); err != nil {
			return err
		}
		result.Chunk = chunk
		result.Created = true
		return nil
	})
	if err != nil {
		return EnsurePersonalChunkResult{}, fmt.Errorf("ensure personal/me memory chunk: %w", err)
	}
	if result.Created {
		s.publishMutation(ctx, chunkMutation(MutationCreated, result.Chunk))
	}
	return result, nil
}

func isPersonalMeScope(chunk memory.Chunk) bool {
	return chunk.Kind == memory.ChunkKindPersonal && chunk.Scope.Kind == memory.ScopeKindPersonal && chunk.Scope.Selector == "me"
}

func isCanonicalPersonalMe(chunk memory.Chunk) bool {
	return chunk.ID == PersonalMeChunkID && isPersonalMeScope(chunk) &&
		chunk.Visibility == memory.VisibilityPrivate && chunk.State == memory.ChunkStateActive
}

func validatePersonalMeMutation(current, next memory.Chunk) error {
	if current.ID == PersonalMeChunkID {
		if !isPersonalMeScope(next) || next.Visibility != memory.VisibilityPrivate || len(next.SharedWith) != 0 {
			return fmt.Errorf("%w: personal/me kind, scope, and private visibility cannot be changed", ErrProtectedChunk)
		}
		return nil
	}
	if isPersonalMeScope(next) {
		return fmt.Errorf("%w: personal/me scope is reserved for the built-in chunk", ErrProtectedChunk)
	}
	return nil
}
