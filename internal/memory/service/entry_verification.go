package service

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/lkarlslund/koder/internal/memory"
	memoryStoreAPI "github.com/lkarlslund/koder/internal/memory/store"
)

// VerifyEntryRequest records a deliberate assessment of an entry's current
// support. Evidence must already exist so every assessed state remains
// inspectable and portable.
type VerifyEntryRequest struct {
	EntryID          memory.EntryID
	ExpectedRevision uint64
	Status           memory.VerificationStatus
	Method           string
	EvidenceIDs      []memory.EvidenceID
	Reason           string
}

type VerifyEntryResult struct {
	Entry   memory.Entry
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
		return VerifyEntryResult{}, fmt.Errorf("%w: entry ID and expected revision are required", memory.ErrInvalidRecord)
	}
	if request.Status == memory.VerificationStatusUnspecified || !request.Status.IsAVerificationStatus() {
		return VerifyEntryResult{}, fmt.Errorf("%w: verification status is required", memory.ErrInvalidRecord)
	}
	request.Method = strings.TrimSpace(request.Method)
	if len(request.Method) > 4<<10 {
		return VerifyEntryResult{}, fmt.Errorf("%w: verification method exceeds 4 KiB", memory.ErrInvalidRecord)
	}
	request.EvidenceIDs = slices.Clone(request.EvidenceIDs)
	if request.Status == memory.VerificationStatusUnverified {
		request.Method = ""
		request.EvidenceIDs = nil
	} else if len(request.EvidenceIDs) == 0 {
		return VerifyEntryResult{}, fmt.Errorf("%w: assessed verification requires evidence", memory.ErrInvalidRecord)
	}

	result := VerifyEntryResult{}
	actor, err := s.actor(ctx)
	if err != nil {
		return VerifyEntryResult{}, fmt.Errorf("resolve memory actor: %w", err)
	}
	if err := actor.Validate(); err != nil {
		return VerifyEntryResult{}, err
	}
	err = s.store.Update(ctx, func(tx memoryStoreAPI.WriteTx) error {
		current, err := tx.Entry(ctx, request.EntryID)
		if err != nil {
			return err
		}
		chunk, err := s.authorizeEntryChunk(ctx, tx, actor, ChunkPolicyEntryVerify, current.ChunkID)
		if err != nil {
			return err
		}
		if current.Revision.Number != request.ExpectedRevision {
			return fmt.Errorf("%w: entry %s expected revision %d, current revision %d", memoryStoreAPI.ErrConflict, request.EntryID, request.ExpectedRevision, current.Revision.Number)
		}
		if current.State != memory.EntryStateActive && current.State != memory.EntryStateDraft {
			return fmt.Errorf("%w: entry %s is %q", ErrEntryNotEditable, current.ID, current.State)
		}
		if chunk.State == memory.ChunkStateArchived {
			return fmt.Errorf("%w: restore chunk %s before verifying entries", ErrParentChunkArchived, chunk.ID)
		}
		if err := validateEvidenceReferences(ctx, tx, request.EvidenceIDs); err != nil {
			return err
		}
		if current.Verification.Status == memory.VerificationStatusUnverified && request.Status == memory.VerificationStatusUnverified {
			result.Entry = current
			return nil
		}
		now := s.now().UTC().Round(0)
		if !now.After(current.UpdatedAt) {
			now = current.UpdatedAt.Add(time.Nanosecond)
		}
		verification := memory.Verification{Status: request.Status}
		if request.Status != memory.VerificationStatusUnverified {
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
		next.Revision = memory.Revision{
			Number: current.Revision.Number + 1,
			ID:     memory.RevisionID(s.newID()),
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
		return VerifyEntryResult{}, fmt.Errorf("verify memory entry: %w", err)
	}
	if result.Updated {
		s.publishMutation(ctx, entryMutation(MutationVerified, result.Entry))
	}
	return result, nil
}
