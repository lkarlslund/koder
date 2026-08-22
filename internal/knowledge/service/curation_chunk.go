package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/lkarlslund/koder/internal/knowledge"
	knowledgeStore "github.com/lkarlslund/koder/internal/knowledge/store"
)

const CuratedLearningChunkID knowledge.ChunkID = "00000000-0000-7000-8000-000000000002"

// EnsureCuratedLearningChunk creates the stable destination used by background
// curation for installation-wide technical and project-independent learning.
// Personal candidates continue to target the separate personal/me chunk.
func (s *Service) EnsureCuratedLearningChunk(ctx context.Context) (EnsurePersonalChunkResult, error) {
	if err := ctx.Err(); err != nil {
		return EnsurePersonalChunkResult{}, err
	}
	result := EnsurePersonalChunkResult{}
	err := s.store.Update(ctx, func(tx knowledgeStore.WriteTx) error {
		current, err := tx.Chunk(ctx, CuratedLearningChunkID)
		switch {
		case err == nil:
			result.Chunk = current
			return nil
		case !errors.Is(err, knowledgeStore.ErrNotFound):
			return err
		}
		actor, err := s.actor(ctx)
		if err != nil {
			return fmt.Errorf("resolve knowledge actor: %w", err)
		}
		now := s.now().UTC().Round(0)
		chunk := knowledge.Chunk{
			ID: CuratedLearningChunkID, Title: "Learned from chats",
			Description: "Reusable knowledge Koder learned from completed conversations.",
			Kind:        knowledge.ChunkKindReference, Scope: knowledge.Scope{Kind: knowledge.ScopeKindGlobal},
			Visibility: knowledge.VisibilityInstallation, State: knowledge.ChunkStateActive, SchemaVersion: 1,
			Revision:  knowledge.Revision{Number: 1, ID: knowledge.RevisionID(s.newID()), Actor: actor, Reason: "seed curated learning chunk", CreatedAt: now},
			CreatedAt: now, UpdatedAt: now,
		}
		if err := chunk.Validate(); err != nil {
			return err
		}
		if err := tx.PutChunk(ctx, chunk, 0); err != nil {
			return err
		}
		result.Chunk, result.Created = chunk, true
		return nil
	})
	if err != nil {
		return EnsurePersonalChunkResult{}, fmt.Errorf("ensure curated learning chunk: %w", err)
	}
	if result.Created {
		s.publishMutation(ctx, chunkMutation(MutationCreated, result.Chunk))
	}
	return result, nil
}
