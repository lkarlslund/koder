package service

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/lkarlslund/koder/internal/memory"
	memoryStoreAPI "github.com/lkarlslund/koder/internal/memory/store"
)

type CascadeDeleteChunkResult struct {
	DeletedEntryIDs          []memory.EntryID
	DeletedLinkIDs           []memory.LinkID
	DeletedEvidenceIDs       []memory.EvidenceID
	UpdatedDependentChunkIDs []memory.ChunkID
}

// CascadeDeleteChunk permanently removes an archived chunk, its entries, and every link
// touching the chunk or its entries. Chunks that depend on it are retained and revised to
// remove the now-invalid dependency. The entire graph rewrite commits atomically.
func (s *Service) CascadeDeleteChunk(ctx context.Context, request DeleteChunkRequest) (CascadeDeleteChunkResult, error) {
	if err := validateDeleteChunkRequest(ctx, request); err != nil {
		return CascadeDeleteChunkResult{}, err
	}
	actor, err := s.actor(ctx)
	if err != nil {
		return CascadeDeleteChunkResult{}, fmt.Errorf("resolve memory actor: %w", err)
	}
	if err := actor.Validate(); err != nil {
		return CascadeDeleteChunkResult{}, err
	}
	result := CascadeDeleteChunkResult{}
	mutationEvents := make([]MutationEvent, 0)
	err = s.store.Update(ctx, func(tx memoryStoreAPI.WriteTx) error {
		target, blockers, err := s.chunkDeletionTarget(ctx, tx, request, actor, ChunkPolicyCascadeDelete)
		if err != nil {
			return err
		}
		touched := map[memory.ChunkID]memory.Chunk{}
		for _, linkID := range blockers.LinkIDs {
			link, err := tx.Link(ctx, linkID)
			if err != nil {
				return err
			}
			for _, endpoint := range []memory.ObjectRef{link.Source, link.Target} {
				chunk, err := resolveLinkEndpoint(ctx, tx, endpoint)
				if err != nil {
					return err
				}
				if chunk.ID != request.ChunkID {
					touched[chunk.ID] = chunk
				}
			}
		}
		for _, dependentID := range blockers.DependentChunkIDs {
			dependent, err := tx.Chunk(ctx, dependentID)
			if err != nil {
				return err
			}
			touched[dependent.ID] = dependent
		}
		touchedIDs := make([]memory.ChunkID, 0, len(touched))
		for chunkID := range touched {
			touchedIDs = append(touchedIDs, chunkID)
		}
		slices.Sort(touchedIDs)
		for _, chunkID := range touchedIDs {
			if err := s.authorizeChunk(ctx, actor, ChunkPolicyCascadeDelete, touched[chunkID]); err != nil {
				return err
			}
		}
		for _, linkID := range blockers.LinkIDs {
			link, err := tx.Link(ctx, linkID)
			if err != nil {
				return err
			}
			if err := tx.DeleteLink(ctx, linkID, link.Revision.Number); err != nil {
				return err
			}
			mutationEvents = append(mutationEvents, linkMutation(MutationDeleted, link))
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
			mutationEvents = append(mutationEvents, entryMutation(MutationDeleted, entry))
			result.DeletedEntryIDs = append(result.DeletedEntryIDs, entryID)
		}
		for _, evidenceID := range blockers.EvidenceIDs {
			evidence, err := tx.Evidence(ctx, evidenceID)
			if err != nil {
				return err
			}
			if err := tx.DeleteEvidence(ctx, evidenceID); err != nil {
				return err
			}
			mutationEvents = append(mutationEvents, evidenceMutation(MutationDeleted, evidence))
			result.DeletedEvidenceIDs = append(result.DeletedEvidenceIDs, evidenceID)
		}
		if len(blockers.DependentChunkIDs) > 0 {
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
				dependent.Revision = memory.Revision{
					Number: dependent.Revision.Number + 1, ID: memory.RevisionID(s.newID()), Actor: actor,
					Reason: fmt.Sprintf("remove dependency on deleted chunk %s", request.ChunkID), CreatedAt: now,
				}
				if err := dependent.Validate(); err != nil {
					return err
				}
				if err := tx.PutChunk(ctx, dependent, dependent.Revision.Number-1); err != nil {
					return err
				}
				mutationEvents = append(mutationEvents, chunkMutation(MutationUpdated, dependent))
				result.UpdatedDependentChunkIDs = append(result.UpdatedDependentChunkIDs, dependentID)
			}
		}
		if err := tx.DeleteChunk(ctx, request.ChunkID, request.ExpectedRevision); err != nil {
			return err
		}
		mutationEvents = append(mutationEvents, chunkMutation(MutationDeleted, target))
		return nil
	})
	if err != nil {
		return CascadeDeleteChunkResult{}, fmt.Errorf("cascade delete memory chunk: %w", err)
	}
	s.publishMutations(ctx, mutationEvents)
	return result, nil
}

func removeChunkID(values []memory.ChunkID, target memory.ChunkID) []memory.ChunkID {
	result := slices.Clone(values)
	return slices.DeleteFunc(result, func(value memory.ChunkID) bool { return value == target })
}
