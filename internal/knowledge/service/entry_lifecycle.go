package service

import (
	"context"
	"fmt"
	"time"

	"github.com/lkarlslund/koder/internal/knowledge"
	knowledgeStore "github.com/lkarlslund/koder/internal/knowledge/store"
)

type EntryLifecycleRequest struct {
	EntryID          knowledge.EntryID
	ExpectedRevision uint64
	Reason           string
}

type EntryLifecycleResult struct {
	Entry   knowledge.Entry
	Updated bool
}

func (s *Service) ArchiveEntry(ctx context.Context, request EntryLifecycleRequest) (EntryLifecycleResult, error) {
	return s.changeEntryState(ctx, request, knowledge.EntryStateArchived)
}

func (s *Service) RestoreEntry(ctx context.Context, request EntryLifecycleRequest) (EntryLifecycleResult, error) {
	return s.changeEntryState(ctx, request, knowledge.EntryStateActive)
}

func (s *Service) changeEntryState(ctx context.Context, request EntryLifecycleRequest, target knowledge.EntryState) (EntryLifecycleResult, error) {
	if err := ctx.Err(); err != nil {
		return EntryLifecycleResult{}, err
	}
	if request.EntryID == "" || request.ExpectedRevision == 0 {
		return EntryLifecycleResult{}, fmt.Errorf("%w: entry ID and expected revision are required", knowledge.ErrInvalidRecord)
	}
	if target != knowledge.EntryStateArchived && target != knowledge.EntryStateActive {
		return EntryLifecycleResult{}, fmt.Errorf("%w: unsupported entry target state %q", ErrInvalidLifecycleTransition, target)
	}
	result := EntryLifecycleResult{}
	actor, err := s.actor(ctx)
	if err != nil {
		return EntryLifecycleResult{}, fmt.Errorf("resolve knowledge actor: %w", err)
	}
	if err := actor.Validate(); err != nil {
		return EntryLifecycleResult{}, err
	}
	err = s.store.Update(ctx, func(tx knowledgeStore.WriteTx) error {
		current, err := tx.Entry(ctx, request.EntryID)
		if err != nil {
			return err
		}
		action := ChunkPolicyEntryArchive
		if target == knowledge.EntryStateActive {
			action = ChunkPolicyEntryRestore
		}
		chunk, err := s.authorizeEntryChunk(ctx, tx, actor, action, current.ChunkID)
		if err != nil {
			return err
		}
		if current.Revision.Number != request.ExpectedRevision {
			return fmt.Errorf("%w: entry %s expected revision %d, current revision %d", knowledgeStore.ErrConflict, request.EntryID, request.ExpectedRevision, current.Revision.Number)
		}
		if current.State == target {
			result.Entry = current
			return nil
		}
		if target == knowledge.EntryStateActive {
			if current.State != knowledge.EntryStateArchived {
				return fmt.Errorf("%w: entry %s in state %q cannot be restored", ErrInvalidLifecycleTransition, request.EntryID, current.State)
			}
			if chunk.State == knowledge.ChunkStateArchived {
				return fmt.Errorf("%w: restore chunk %s before restoring entries", ErrParentChunkArchived, chunk.ID)
			}
		} else if current.State != knowledge.EntryStateActive && current.State != knowledge.EntryStateDraft {
			return fmt.Errorf("%w: entry %s in state %q cannot be archived", ErrInvalidLifecycleTransition, request.EntryID, current.State)
		}
		now := s.now().UTC().Round(0)
		if !now.After(current.UpdatedAt) {
			now = current.UpdatedAt.Add(time.Nanosecond)
		}
		reason := request.Reason
		if reason == "" {
			if target == knowledge.EntryStateArchived {
				reason = "archive entry"
			} else {
				reason = "restore entry"
			}
		}
		next := current
		next.State = target
		next.SupersededByID = ""
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
		return EntryLifecycleResult{}, fmt.Errorf("change knowledge entry lifecycle: %w", err)
	}
	if result.Updated {
		kind := MutationArchived
		if target == knowledge.EntryStateActive {
			kind = MutationRestored
		}
		s.publishMutation(ctx, entryMutation(kind, result.Entry))
	}
	return result, nil
}
