package service

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/lkarlslund/koder/internal/knowledge"
	knowledgeStore "github.com/lkarlslund/koder/internal/knowledge/store"
)

// VerifyEntryRequest records a deliberate assessment of an entry's current
// support. Evidence must already exist so every assessed state remains
// inspectable and portable.
type VerifyEntryRequest struct {
	EntryID          knowledge.EntryID
	ExpectedRevision uint64
	Status           knowledge.VerificationStatus
	Method           string
	EvidenceIDs      []knowledge.EvidenceID
	Reason           string
}

type VerifyEntryResult struct {
	Entry   knowledge.Entry
	Updated bool
}

// VerifyEntry replaces server-owned verification metadata and advances the
// entry revision. Setting unverified deliberately clears prior assessment
// details; assessed states require at least one existing evidence object.
func (s *Service) VerifyEntry(ctx context.Context, request VerifyEntryRequest) (VerifyEntryResult, error) {
	if err := ctx.Err(); err != nil {
		return VerifyEntryResult{}, err
	}
	if request.EntryID == "" || request.ExpectedRevision == 0 {
		return VerifyEntryResult{}, fmt.Errorf("%w: entry ID and expected revision are required", knowledge.ErrInvalidRecord)
	}
	if request.Status == knowledge.VerificationStatusUnspecified || !request.Status.IsAVerificationStatus() {
		return VerifyEntryResult{}, fmt.Errorf("%w: verification status is required", knowledge.ErrInvalidRecord)
	}
	request.Method = strings.TrimSpace(request.Method)
	if len(request.Method) > 4<<10 {
		return VerifyEntryResult{}, fmt.Errorf("%w: verification method exceeds 4 KiB", knowledge.ErrInvalidRecord)
	}
	request.EvidenceIDs = slices.Clone(request.EvidenceIDs)
	if request.Status == knowledge.VerificationStatusUnverified {
		request.Method = ""
		request.EvidenceIDs = nil
	} else if len(request.EvidenceIDs) == 0 {
		return VerifyEntryResult{}, fmt.Errorf("%w: assessed verification requires evidence", knowledge.ErrInvalidRecord)
	}

	result := VerifyEntryResult{}
	actor, err := s.actor(ctx)
	if err != nil {
		return VerifyEntryResult{}, fmt.Errorf("resolve knowledge actor: %w", err)
	}
	if err := actor.Validate(); err != nil {
		return VerifyEntryResult{}, err
	}
	err = s.store.Update(ctx, func(tx knowledgeStore.WriteTx) error {
		current, err := tx.Entry(ctx, request.EntryID)
		if err != nil {
			return err
		}
		chunk, err := s.authorizeEntryChunk(ctx, tx, actor, ChunkPolicyEntryVerify, current.ChunkID)
		if err != nil {
			return err
		}
		if current.Revision.Number != request.ExpectedRevision {
			return fmt.Errorf("%w: entry %s expected revision %d, current revision %d", knowledgeStore.ErrConflict, request.EntryID, request.ExpectedRevision, current.Revision.Number)
		}
		if current.State != knowledge.EntryStateActive && current.State != knowledge.EntryStateDraft {
			return fmt.Errorf("%w: entry %s is %q", ErrEntryNotEditable, current.ID, current.State)
		}
		if chunk.State == knowledge.ChunkStateArchived {
			return fmt.Errorf("%w: restore chunk %s before verifying entries", ErrParentChunkArchived, chunk.ID)
		}
		if err := validateEvidenceReferences(ctx, tx, request.EvidenceIDs); err != nil {
			return err
		}
		if current.Verification.Status == knowledge.VerificationStatusUnverified && request.Status == knowledge.VerificationStatusUnverified {
			result.Entry = current
			return nil
		}
		now := s.now().UTC().Round(0)
		if !now.After(current.UpdatedAt) {
			now = current.UpdatedAt.Add(time.Nanosecond)
		}
		verification := knowledge.Verification{Status: request.Status}
		if request.Status != knowledge.VerificationStatusUnverified {
			verification.Method = request.Method
			verification.EvidenceIDs = request.EvidenceIDs
			verification.Actor = actor
			verification.VerifiedAt = now
		}
		reason := strings.TrimSpace(request.Reason)
		if reason == "" {
			reason = "set verification to " + request.Status.String()
		}
		next := current
		next.Verification = verification
		next.UpdatedAt = now
		next.Revision = knowledge.Revision{
			Number: current.Revision.Number + 1,
			ID:     knowledge.RevisionID(s.newID()),
			Actor:  actor, Reason: reason, CreatedAt: now,
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
		return VerifyEntryResult{}, fmt.Errorf("verify knowledge entry: %w", err)
	}
	return result, nil
}
