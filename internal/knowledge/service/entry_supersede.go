package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/lkarlslund/koder/internal/knowledge"
	knowledgeStore "github.com/lkarlslund/koder/internal/knowledge/store"
)

var ErrInvalidSupersession = errors.New("invalid knowledge entry supersession")

type SupersedeEntryRequest struct {
	EntryID            knowledge.EntryID
	ExpectedRevision   uint64
	ReplacementEntryID knowledge.EntryID
	Reason             string
}

type SupersedeEntryResult struct {
	Entry       knowledge.Entry
	Replacement knowledge.Entry
	Updated     bool
}

// SupersedeEntry retires one entry in favor of an existing active replacement. Requiring
// the replacement to exist first makes the correction inspectable before the old claim is
// hidden, while the actual state transition remains atomic.
func (s *Service) SupersedeEntry(ctx context.Context, request SupersedeEntryRequest) (SupersedeEntryResult, error) {
	if err := ctx.Err(); err != nil {
		return SupersedeEntryResult{}, err
	}
	if request.EntryID == "" || request.ReplacementEntryID == "" || request.ExpectedRevision == 0 {
		return SupersedeEntryResult{}, fmt.Errorf("%w: entry ID, replacement entry ID, and expected revision are required", knowledge.ErrInvalidRecord)
	}
	if request.EntryID == request.ReplacementEntryID {
		return SupersedeEntryResult{}, fmt.Errorf("%w: an entry cannot supersede itself", ErrInvalidSupersession)
	}
	result := SupersedeEntryResult{}
	err := s.store.Update(ctx, func(tx knowledgeStore.WriteTx) error {
		current, err := tx.Entry(ctx, request.EntryID)
		if err != nil {
			return err
		}
		if current.Revision.Number != request.ExpectedRevision {
			return fmt.Errorf("%w: entry %s expected revision %d, current revision %d", knowledgeStore.ErrConflict, request.EntryID, request.ExpectedRevision, current.Revision.Number)
		}
		replacement, err := tx.Entry(ctx, request.ReplacementEntryID)
		if err != nil {
			return err
		}
		result.Replacement = replacement
		if current.State == knowledge.EntryStateSuperseded {
			if current.SupersededByID != replacement.ID {
				return fmt.Errorf("%w: entry %s is already superseded by %s", ErrInvalidSupersession, current.ID, current.SupersededByID)
			}
			result.Entry = current
			return nil
		}
		if current.State != knowledge.EntryStateActive && current.State != knowledge.EntryStateDraft {
			return fmt.Errorf("%w: entry %s in state %q cannot be superseded", ErrInvalidSupersession, current.ID, current.State)
		}
		if replacement.State != knowledge.EntryStateActive {
			return fmt.Errorf("%w: replacement entry %s must be active, not %q", ErrInvalidSupersession, replacement.ID, replacement.State)
		}
		if replacement.ChunkID != current.ChunkID {
			return fmt.Errorf("%w: replacement entry %s belongs to chunk %s, not %s", ErrInvalidSupersession, replacement.ID, replacement.ChunkID, current.ChunkID)
		}
		chunk, err := tx.Chunk(ctx, current.ChunkID)
		if err != nil {
			return err
		}
		if chunk.State == knowledge.ChunkStateArchived {
			return fmt.Errorf("%w: restore chunk %s before superseding entries", ErrParentChunkArchived, chunk.ID)
		}
		actor, err := s.actor(ctx)
		if err != nil {
			return fmt.Errorf("resolve knowledge actor: %w", err)
		}
		if err := actor.Validate(); err != nil {
			return err
		}
		now := s.now().UTC().Round(0)
		if !now.After(current.UpdatedAt) {
			now = current.UpdatedAt.Add(time.Nanosecond)
		}
		reason := request.Reason
		if reason == "" {
			reason = fmt.Sprintf("superseded by entry %s", replacement.ID)
		}
		next := current
		next.State = knowledge.EntryStateSuperseded
		next.SupersededByID = replacement.ID
		next.UpdatedAt = now
		next.Revision = knowledge.Revision{
			Number: current.Revision.Number + 1, ID: knowledge.RevisionID(s.newID()),
			Actor: actor, Reason: reason, CreatedAt: now,
		}
		if err := next.Validate(); err != nil {
			return err
		}
		if err := tx.PutEntry(ctx, next, current.Revision.Number); err != nil {
			return err
		}
		result.Entry = next
		result.Updated = true
		return nil
	})
	if err != nil {
		return SupersedeEntryResult{}, fmt.Errorf("supersede knowledge entry: %w", err)
	}
	return result, nil
}
