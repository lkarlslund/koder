package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/lkarlslund/koder/internal/memory"
	memoryStoreAPI "github.com/lkarlslund/koder/internal/memory/store"
)

var ErrInvalidSupersession = errors.New("invalid memory entry supersession")

type SupersedeEntryRequest struct {
	EntryID            memory.EntryID
	ExpectedRevision   uint64
	ReplacementEntryID memory.EntryID
	Reason             string
}

type SupersedeEntryResult struct {
	Entry       memory.Entry
	Replacement memory.Entry
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
		return SupersedeEntryResult{}, fmt.Errorf("%w: entry ID, replacement entry ID, and expected revision are required", memory.ErrInvalidRecord)
	}
	if request.EntryID == request.ReplacementEntryID {
		return SupersedeEntryResult{}, fmt.Errorf("%w: an entry cannot supersede itself", ErrInvalidSupersession)
	}
	result := SupersedeEntryResult{}
	actor, err := s.actor(ctx)
	if err != nil {
		return SupersedeEntryResult{}, fmt.Errorf("resolve memory actor: %w", err)
	}
	if err := actor.Validate(); err != nil {
		return SupersedeEntryResult{}, err
	}
	err = s.store.Update(ctx, func(tx memoryStoreAPI.WriteTx) error {
		current, err := tx.Entry(ctx, request.EntryID)
		if err != nil {
			return err
		}
		chunk, err := s.authorizeEntryChunk(ctx, tx, actor, ChunkPolicyEntrySupersede, current.ChunkID)
		if err != nil {
			return err
		}
		if current.Revision.Number != request.ExpectedRevision {
			return fmt.Errorf("%w: entry %s expected revision %d, current revision %d", memoryStoreAPI.ErrConflict, request.EntryID, request.ExpectedRevision, current.Revision.Number)
		}
		replacement, err := tx.Entry(ctx, request.ReplacementEntryID)
		if err != nil {
			return err
		}
		result.Replacement = replacement
		if current.State == memory.EntryStateSuperseded {
			if current.SupersededByID != replacement.ID {
				return fmt.Errorf("%w: entry %s is already superseded by %s", ErrInvalidSupersession, current.ID, current.SupersededByID)
			}
			result.Entry = current
			return nil
		}
		if current.State != memory.EntryStateActive && current.State != memory.EntryStateDraft {
			return fmt.Errorf("%w: entry %s in state %q cannot be superseded", ErrInvalidSupersession, current.ID, current.State)
		}
		if replacement.State != memory.EntryStateActive {
			return fmt.Errorf("%w: replacement entry %s must be active, not %q", ErrInvalidSupersession, replacement.ID, replacement.State)
		}
		if replacement.ChunkID != current.ChunkID {
			return fmt.Errorf("%w: replacement entry %s belongs to chunk %s, not %s", ErrInvalidSupersession, replacement.ID, replacement.ChunkID, current.ChunkID)
		}
		if chunk.State == memory.ChunkStateArchived {
			return fmt.Errorf("%w: restore chunk %s before superseding entries", ErrParentChunkArchived, chunk.ID)
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
		next.State = memory.EntryStateSuperseded
		next.SupersededByID = replacement.ID
		next.UpdatedAt = now
		next.Revision = memory.Revision{
			Number: current.Revision.Number + 1, ID: memory.RevisionID(s.newID()),
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
		return SupersedeEntryResult{}, fmt.Errorf("supersede memory entry: %w", err)
	}
	if result.Updated {
		event := entryMutation(MutationSuperseded, result.Entry)
		event.Related = []MutationObject{{Kind: memoryStoreAPI.RecordKindEntry, ID: string(request.ReplacementEntryID)}}
		s.publishMutation(ctx, event)
	}
	return result, nil
}
