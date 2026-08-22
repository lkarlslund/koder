package service

import (
	"context"
	"fmt"
	"time"

	"github.com/lkarlslund/koder/internal/knowledge"
	knowledgeStore "github.com/lkarlslund/koder/internal/knowledge/store"
)

type LinkLifecycleRequest struct {
	LinkID           knowledge.LinkID
	ExpectedRevision uint64
	Reason           string
}

type LinkLifecycleResult struct {
	Link    knowledge.Link
	Updated bool
}

// Unlink reversibly removes a relationship from active traversal. Permanent deletion is
// reserved for confirmed cascade/deletion workflows.
func (s *Service) Unlink(ctx context.Context, request LinkLifecycleRequest) (LinkLifecycleResult, error) {
	return s.changeLinkState(ctx, request, knowledge.LinkStateArchived)
}

func (s *Service) RestoreLink(ctx context.Context, request LinkLifecycleRequest) (LinkLifecycleResult, error) {
	return s.changeLinkState(ctx, request, knowledge.LinkStateActive)
}

func (s *Service) changeLinkState(ctx context.Context, request LinkLifecycleRequest, target knowledge.LinkState) (LinkLifecycleResult, error) {
	if err := ctx.Err(); err != nil {
		return LinkLifecycleResult{}, err
	}
	if request.LinkID == "" || request.ExpectedRevision == 0 {
		return LinkLifecycleResult{}, fmt.Errorf("%w: link ID and expected revision are required", knowledge.ErrInvalidRecord)
	}
	if target != knowledge.LinkStateArchived && target != knowledge.LinkStateActive {
		return LinkLifecycleResult{}, fmt.Errorf("%w: unsupported link target state %q", ErrInvalidLifecycleTransition, target)
	}
	result := LinkLifecycleResult{}
	actor, err := s.actor(ctx)
	if err != nil {
		return LinkLifecycleResult{}, fmt.Errorf("resolve knowledge actor: %w", err)
	}
	if err := actor.Validate(); err != nil {
		return LinkLifecycleResult{}, err
	}
	err = s.store.Update(ctx, func(tx knowledgeStore.WriteTx) error {
		current, err := tx.Link(ctx, request.LinkID)
		if err != nil {
			return err
		}
		sourceChunk, err := resolveLinkEndpoint(ctx, tx, current.Source)
		if err != nil {
			return fmt.Errorf("resolve link source: %w", err)
		}
		targetChunk, err := resolveLinkEndpoint(ctx, tx, current.Target)
		if err != nil {
			return fmt.Errorf("resolve link target: %w", err)
		}
		action := ChunkPolicyLinkUnlink
		requireActive := false
		if target == knowledge.LinkStateActive {
			action = ChunkPolicyLinkRestore
			requireActive = true
		}
		if err := s.authorizeLinkChunks(ctx, actor, action, requireActive, sourceChunk, targetChunk); err != nil {
			return err
		}
		if current.Revision.Number != request.ExpectedRevision {
			return fmt.Errorf("%w: link %s expected revision %d, current revision %d", knowledgeStore.ErrConflict, request.LinkID, request.ExpectedRevision, current.Revision.Number)
		}
		if current.State == target {
			result.Link = current
			return nil
		}
		if target == knowledge.LinkStateArchived && current.State != knowledge.LinkStateActive {
			return fmt.Errorf("%w: link %s in state %q cannot be unlinked", ErrInvalidLifecycleTransition, request.LinkID, current.State)
		}
		if target == knowledge.LinkStateActive && current.State != knowledge.LinkStateArchived {
			return fmt.Errorf("%w: link %s in state %q cannot be restored", ErrInvalidLifecycleTransition, request.LinkID, current.State)
		}
		now := s.now().UTC().Round(0)
		if !now.After(current.UpdatedAt) {
			now = current.UpdatedAt.Add(time.Nanosecond)
		}
		reason := request.Reason
		if reason == "" {
			if target == knowledge.LinkStateArchived {
				reason = "unlink relationship"
			} else {
				reason = "restore relationship"
			}
		}
		next := current
		next.State = target
		next.UpdatedAt = now
		next.Revision = knowledge.Revision{
			Number: current.Revision.Number + 1, ID: knowledge.RevisionID(s.newID()),
			Actor: actor, Reason: reason, CreatedAt: now,
		}
		if err := next.Validate(); err != nil {
			return err
		}
		if err := tx.PutLink(ctx, next, current.Revision.Number); err != nil {
			return err
		}
		result.Link = next
		result.Updated = true
		return nil
	})
	if err != nil {
		return LinkLifecycleResult{}, fmt.Errorf("change knowledge link lifecycle: %w", err)
	}
	if result.Updated {
		kind := MutationUnlinked
		if target == knowledge.LinkStateActive {
			kind = MutationRestored
		}
		s.publishMutation(linkMutation(kind, result.Link))
	}
	return result, nil
}
