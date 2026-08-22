package service

import (
	"context"
	"fmt"
	"time"

	"github.com/lkarlslund/koder/internal/knowledge"
	knowledgeStore "github.com/lkarlslund/koder/internal/knowledge/store"
)

type RestoreEntryRevisionRequest struct {
	EntryID          knowledge.EntryID
	ExpectedRevision uint64
	SourceRevision   uint64
	Reason           string
}

// RestoreEntryRevision creates a new revision from older canonical content. It never
// rewinds or deletes audit history and uses an optimistic current-revision precondition.
func (s *Service) RestoreEntryRevision(ctx context.Context, request RestoreEntryRevisionRequest) (UpdateEntryResult, error) {
	if request.EntryID == "" || request.ExpectedRevision == 0 || request.SourceRevision == 0 || request.SourceRevision >= request.ExpectedRevision {
		return UpdateEntryResult{}, fmt.Errorf("%w: entry, current revision, and older source revision are required", knowledge.ErrInvalidRecord)
	}
	page, err := s.History(ctx, knowledgeStore.RevisionListRequest{
		Object: knowledge.ObjectRef{Kind: knowledge.ObjectKindEntry, ID: string(request.EntryID)}, Limit: 200,
	})
	if err != nil {
		return UpdateEntryResult{}, err
	}
	var source *knowledge.Entry
	for _, record := range page.Revisions {
		if record.Entry != nil && record.Entry.Revision.Number == request.SourceRevision {
			value := *record.Entry
			source = &value
			break
		}
	}
	if source == nil {
		return UpdateEntryResult{}, fmt.Errorf("%w: source entry revision was not found in bounded history", knowledgeStore.ErrNotFound)
	}
	actor, err := s.actor(ctx)
	if err != nil {
		return UpdateEntryResult{}, err
	}
	result := UpdateEntryResult{}
	err = s.store.Update(ctx, func(tx knowledgeStore.WriteTx) error {
		current, err := tx.Entry(ctx, request.EntryID)
		if err != nil {
			return err
		}
		chunk, err := s.authorizeEntryChunk(ctx, tx, actor, ChunkPolicyEntryUpdate, current.ChunkID)
		if err != nil {
			return err
		}
		if current.Revision.Number != request.ExpectedRevision {
			return fmt.Errorf("%w: entry changed before revision restore", knowledgeStore.ErrConflict)
		}
		if current.State != knowledge.EntryStateActive && current.State != knowledge.EntryStateDraft {
			return fmt.Errorf("%w: entry %s is %q", ErrEntryNotEditable, current.ID, current.State)
		}
		if chunk.State == knowledge.ChunkStateArchived {
			return fmt.Errorf("%w: restore chunk %s before editing entries", ErrParentChunkArchived, chunk.ID)
		}
		next := *source
		next.ID, next.ChunkID, next.CreatedAt = current.ID, current.ChunkID, current.CreatedAt
		now := s.now().UTC().Round(0)
		if !now.After(current.UpdatedAt) {
			now = current.UpdatedAt.Add(time.Nanosecond)
		}
		next.UpdatedAt = now
		next.Revision = knowledge.Revision{
			Number: current.Revision.Number + 1, ID: knowledge.RevisionID(s.newID()), Actor: actor,
			Reason: request.Reason, CreatedAt: now,
		}
		if err := validateEvidenceReferences(ctx, tx, next.EvidenceIDs, next.Verification.EvidenceIDs); err != nil {
			return err
		}
		if err := validatePersonalEntryEvidence(ctx, tx, next); err != nil {
			return err
		}
		if err := next.Validate(); err != nil {
			return err
		}
		if err := tx.PutEntry(ctx, next, current.Revision.Number); err != nil {
			return err
		}
		result.Entry, result.Updated = next, true
		return nil
	})
	if err != nil {
		return UpdateEntryResult{}, fmt.Errorf("restore knowledge entry revision: %w", err)
	}
	s.publishMutation(ctx, entryMutation(MutationUpdated, result.Entry))
	return result, nil
}
