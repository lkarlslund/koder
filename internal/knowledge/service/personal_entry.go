package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/lkarlslund/koder/internal/knowledge"
	knowledgeStore "github.com/lkarlslund/koder/internal/knowledge/store"
)

var ErrPersonalOriginPolicy = errors.New("personal knowledge origin policy violation")

func applyPersonalEntryUpdatePolicy(next *knowledge.Entry, current knowledge.Entry, classification knowledge.ClassificationResult) error {
	if current.Scope.Kind == knowledge.ScopeKindPersonal {
		switch current.PersonalOrigin {
		case knowledge.PersonalOriginExplicit:
			if next.PersonalOrigin != knowledge.PersonalOriginExplicit {
				return fmt.Errorf("%w: explicit personal knowledge cannot be downgraded to %q", ErrPersonalOriginPolicy, next.PersonalOrigin)
			}
		case knowledge.PersonalOriginObserved:
			if next.PersonalOrigin == knowledge.PersonalOriginInferred || next.PersonalOrigin == knowledge.PersonalOriginUnspecified {
				return fmt.Errorf("%w: observed personal knowledge cannot be downgraded to %q", ErrPersonalOriginPolicy, next.PersonalOrigin)
			}
		}
	}
	if next.Scope.Kind != knowledge.ScopeKindPersonal || next.PersonalOrigin != knowledge.PersonalOriginInferred {
		if current.State == knowledge.EntryStateDraft && current.PersonalOrigin == knowledge.PersonalOriginInferred &&
			(next.PersonalOrigin == knowledge.PersonalOriginExplicit || next.PersonalOrigin == knowledge.PersonalOriginObserved) {
			next.State = knowledge.EntryStateActive
		}
		return nil
	}
	if classification.Decision == knowledge.ClassificationDecisionReview {
		next.State = knowledge.EntryStateDraft
	}
	return nil
}

func validatePersonalEntryEvidence(ctx context.Context, tx knowledgeStore.ReadTx, entry knowledge.Entry) error {
	if entry.Scope.Kind != knowledge.ScopeKindPersonal || entry.PersonalOrigin != knowledge.PersonalOriginObserved {
		return nil
	}
	for _, evidenceID := range entry.EvidenceIDs {
		evidence, err := tx.Evidence(ctx, evidenceID)
		if err != nil {
			return err
		}
		if evidence.Type == knowledge.EvidenceTypeObservation || evidence.Type == knowledge.EvidenceTypeToolResult {
			return nil
		}
	}
	return fmt.Errorf("%w: observed personal knowledge requires observation or tool-result evidence", ErrPersonalOriginPolicy)
}
