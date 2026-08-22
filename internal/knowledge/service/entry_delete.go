package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/lkarlslund/koder/internal/knowledge"
	knowledgeStore "github.com/lkarlslund/koder/internal/knowledge/store"
)

var (
	ErrEntryMustBeArchived  = errors.New("knowledge entry must be archived before deletion")
	ErrEntryDeletionBlocked = errors.New("knowledge entry deletion is blocked")
)

type DeleteEntryRequest struct {
	EntryID          knowledge.EntryID
	ExpectedRevision uint64
	Confirmed        bool
}

type EntryDeletionBlockedError struct {
	EntryID  knowledge.EntryID
	Blockers knowledgeStore.EntryDeletionBlockers
}

func (e *EntryDeletionBlockedError) Error() string {
	return fmt.Sprintf("%s: entry %s has %d links and is the replacement for %d superseded entries",
		ErrEntryDeletionBlocked, e.EntryID, len(e.Blockers.LinkIDs), len(e.Blockers.SupersededEntryIDs))
}

func (e *EntryDeletionBlockedError) Unwrap() error { return ErrEntryDeletionBlocked }

func (s *Service) DeleteEntry(ctx context.Context, request DeleteEntryRequest) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if request.EntryID == "" || request.ExpectedRevision == 0 {
		return fmt.Errorf("%w: entry ID and expected revision are required", knowledge.ErrInvalidRecord)
	}
	if !request.Confirmed {
		return ErrDeleteConfirmationRequired
	}
	actor, err := s.actor(ctx)
	if err != nil {
		return fmt.Errorf("resolve knowledge actor: %w", err)
	}
	if err := actor.Validate(); err != nil {
		return err
	}
	err = s.store.Update(ctx, func(tx knowledgeStore.WriteTx) error {
		entry, err := tx.Entry(ctx, request.EntryID)
		if err != nil {
			return err
		}
		if _, err := s.authorizeEntryChunk(ctx, tx, actor, ChunkPolicyEntryDelete, entry.ChunkID); err != nil {
			return err
		}
		if entry.Revision.Number != request.ExpectedRevision {
			return fmt.Errorf("%w: entry %s expected revision %d, current revision %d", knowledgeStore.ErrConflict, request.EntryID, request.ExpectedRevision, entry.Revision.Number)
		}
		if entry.State != knowledge.EntryStateArchived {
			return fmt.Errorf("%w: archive entry %s before deleting it", ErrEntryMustBeArchived, request.EntryID)
		}
		blockers, err := tx.EntryDeletionBlockers(ctx, request.EntryID)
		if err != nil {
			return err
		}
		if !blockers.Empty() {
			return &EntryDeletionBlockedError{EntryID: request.EntryID, Blockers: blockers}
		}
		return tx.DeleteEntry(ctx, request.EntryID, request.ExpectedRevision)
	})
	if err != nil {
		return fmt.Errorf("delete knowledge entry: %w", err)
	}
	return nil
}
