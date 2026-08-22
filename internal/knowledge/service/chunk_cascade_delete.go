package service

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/lkarlslund/koder/internal/knowledge"
	knowledgeStore "github.com/lkarlslund/koder/internal/knowledge/store"
)

type CascadeDeleteChunkResult struct {
	DeletedEntryIDs          []knowledge.EntryID
	DeletedLinkIDs           []knowledge.LinkID
	DeletedEvidenceIDs       []knowledge.EvidenceID
	UpdatedDependentChunkIDs []knowledge.ChunkID
}

// CascadeDeleteChunk permanently removes an archived chunk, its entries, and every link
// touching the chunk or its entries. Chunks that depend on it are retained and revised to
// remove the now-invalid dependency. The entire graph rewrite commits atomically.
func (s *Service) CascadeDeleteChunk(ctx context.Context, request DeleteChunkRequest) (CascadeDeleteChunkResult, error) {
	if err := validateDeleteChunkRequest(ctx, request); err != nil {
		return CascadeDeleteChunkResult{}, err
	}
	result := CascadeDeleteChunkResult{}
	err := s.store.Update(ctx, func(tx knowledgeStore.WriteTx) error {
		_, blockers, err := chunkDeletionTarget(ctx, tx, request)
		if err != nil {
			return err
		}
		for _, linkID := range blockers.LinkIDs {
			link, err := tx.Link(ctx, linkID)
			if err != nil {
				return err
			}
			if err := tx.DeleteLink(ctx, linkID, link.Revision.Number); err != nil {
				return err
			}
			result.DeletedLinkIDs = append(result.DeletedLinkIDs, linkID)
		}
		for _, entryID := range blockers.EntryIDs {
			entry, err := tx.Entry(ctx, entryID)
			if err != nil {
				return err
			}
			if err := tx.DeleteEntry(ctx, entryID, entry.Revision.Number); err != nil {
				return err
			}
			result.DeletedEntryIDs = append(result.DeletedEntryIDs, entryID)
		}
		for _, evidenceID := range blockers.EvidenceIDs {
			if err := tx.DeleteEvidence(ctx, evidenceID); err != nil {
				return err
			}
			result.DeletedEvidenceIDs = append(result.DeletedEvidenceIDs, evidenceID)
		}
		if len(blockers.DependentChunkIDs) > 0 {
			actor, err := s.actor(ctx)
			if err != nil {
				return fmt.Errorf("resolve knowledge actor: %w", err)
			}
			if err := actor.Validate(); err != nil {
				return err
			}
			for _, dependentID := range blockers.DependentChunkIDs {
				dependent, err := tx.Chunk(ctx, dependentID)
				if err != nil {
					return err
				}
				dependent.DependencyIDs = removeChunkID(dependent.DependencyIDs, request.ChunkID)
				now := s.now().UTC().Round(0)
				if !now.After(dependent.UpdatedAt) {
					now = dependent.UpdatedAt.Add(time.Nanosecond)
				}
				dependent.UpdatedAt = now
				dependent.Revision = knowledge.Revision{
					Number: dependent.Revision.Number + 1, ID: knowledge.RevisionID(s.newID()), Actor: actor,
					Reason: fmt.Sprintf("remove dependency on deleted chunk %s", request.ChunkID), CreatedAt: now,
				}
				if err := dependent.Validate(); err != nil {
					return err
				}
				if err := tx.PutChunk(ctx, dependent, dependent.Revision.Number-1); err != nil {
					return err
				}
				result.UpdatedDependentChunkIDs = append(result.UpdatedDependentChunkIDs, dependentID)
			}
		}
		return tx.DeleteChunk(ctx, request.ChunkID, request.ExpectedRevision)
	})
	if err != nil {
		return CascadeDeleteChunkResult{}, fmt.Errorf("cascade delete knowledge chunk: %w", err)
	}
	return result, nil
}

func removeChunkID(values []knowledge.ChunkID, target knowledge.ChunkID) []knowledge.ChunkID {
	result := slices.Clone(values)
	return slices.DeleteFunc(result, func(value knowledge.ChunkID) bool { return value == target })
}
