package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/lkarlslund/koder/internal/memory"
	memoryStoreAPI "github.com/lkarlslund/koder/internal/memory/store"
)

var ErrPersonalOriginPolicy = errors.New("personal memory origin policy violation")

func applyPersonalEntryCreatePolicy(candidate *memory.Entry, chunk memory.Chunk, classification memory.ClassificationResult) error {
	if chunk.ID == PersonalMeChunkID {
		if candidate.Scope != chunk.Scope {
			return fmt.Errorf("%w: personal/me entries must retain the personal/me scope", ErrPersonalOriginPolicy)
		}
		if candidate.PersonalOrigin == memory.PersonalOriginUnspecified {
			return fmt.Errorf("%w: personal/me entries require an explicit, observed, or inferred origin", ErrPersonalOriginPolicy)
		}
	}
	if candidate.Scope.Kind == memory.ScopeKindPersonal && candidate.PersonalOrigin == memory.PersonalOriginInferred &&
		(classification.Decision == memory.ClassificationDecisionReview || candidate.IsSensitiveInference()) {
		candidate.State = memory.EntryStateDraft
	}
	return nil
}

func applyPersonalEntryUpdatePolicy(next *memory.Entry, current memory.Entry, chunk memory.Chunk, classification memory.ClassificationResult) error {
	if chunk.ID == PersonalMeChunkID {
		if next.Scope != chunk.Scope {
			return fmt.Errorf("%w: personal/me entries must retain the personal/me scope", ErrPersonalOriginPolicy)
		}
		if next.PersonalOrigin == memory.PersonalOriginUnspecified {
			return fmt.Errorf("%w: personal/me entries require an explicit, observed, or inferred origin", ErrPersonalOriginPolicy)
		}
	}
	if current.Scope.Kind == memory.ScopeKindPersonal {
		switch current.PersonalOrigin {
		case memory.PersonalOriginExplicit:
			if next.PersonalOrigin != memory.PersonalOriginExplicit {
				return fmt.Errorf("%w: explicit personal memory cannot be downgraded to %q", ErrPersonalOriginPolicy, next.PersonalOrigin)
			}
		case memory.PersonalOriginObserved:
			if next.PersonalOrigin == memory.PersonalOriginInferred || next.PersonalOrigin == memory.PersonalOriginUnspecified {
				return fmt.Errorf("%w: observed personal memory cannot be downgraded to %q", ErrPersonalOriginPolicy, next.PersonalOrigin)
			}
		}
	}
	if next.Scope.Kind != memory.ScopeKindPersonal || next.PersonalOrigin != memory.PersonalOriginInferred {
		if current.State == memory.EntryStateDraft && current.PersonalOrigin == memory.PersonalOriginInferred &&
			(next.PersonalOrigin == memory.PersonalOriginExplicit || next.PersonalOrigin == memory.PersonalOriginObserved) {
			next.State = memory.EntryStateActive
		}
		return nil
	}
	if classification.Decision == memory.ClassificationDecisionReview || next.IsSensitiveInference() {
		next.State = memory.EntryStateDraft
	}
	return nil
}

func validatePersonalEntryEvidence(ctx context.Context, tx memoryStoreAPI.ReadTx, entry memory.Entry) error {
	if entry.Scope.Kind != memory.ScopeKindPersonal || entry.PersonalOrigin != memory.PersonalOriginObserved {
		return nil
	}
	for _, evidenceID := range entry.EvidenceIDs {
		evidence, err := tx.Evidence(ctx, evidenceID)
		if err != nil {
			return err
		}
		if evidence.Type == memory.EvidenceTypeObservation || evidence.Type == memory.EvidenceTypeToolResult {
			return nil
		}
	}
	return fmt.Errorf("%w: observed personal memory requires observation or tool-result evidence", ErrPersonalOriginPolicy)
}
