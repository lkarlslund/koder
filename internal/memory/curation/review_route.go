package curation

import (
	"context"
	"fmt"
	"slices"

	"github.com/lkarlslund/koder/internal/memory"
)

const (
	reviewReasonClassification     = "classification_review"
	reviewReasonRisk               = "risk_label"
	reviewReasonPersonalInference  = "personal_inference"
	reviewReasonNonAutomaticAction = "non_automatic_action"
)

// ReviewRoutingSink assigns a server-owned route after validation and before candidate
// storage. A model cannot opt itself into automatic application.
type ReviewRoutingSink struct {
	next CandidateSink
}

func NewReviewRoutingSink(next CandidateSink) (*ReviewRoutingSink, error) {
	if next == nil {
		return nil, fmt.Errorf("%w: review routing requires a candidate sink", ErrUnavailable)
	}
	return &ReviewRoutingSink{next: next}, nil
}

func (s *ReviewRoutingSink) StoreCandidates(ctx context.Context, recordID memory.CurationRecordID, drafts []CandidateDraft) (uint32, error) {
	routed := cloneCandidateDrafts(drafts)
	for index := range routed {
		draft := &routed[index]
		if draft.Classification.Decision != memory.ClassificationDecisionAllow && draft.Classification.Decision != memory.ClassificationDecisionReview {
			return 0, fmt.Errorf("%w: candidate classification cannot be routed", memory.ErrInvalidRecord)
		}
		reasons := make([]string, 0, 4)
		if draft.Classification.Decision == memory.ClassificationDecisionReview {
			reasons = append(reasons, reviewReasonClassification)
		}
		if len(draft.Entry.Risk) != 0 {
			reasons = append(reasons, reviewReasonRisk)
		}
		if draft.Entry.Scope.Kind == memory.ScopeKindPersonal && draft.Entry.PersonalOrigin == memory.PersonalOriginInferred {
			reasons = append(reasons, reviewReasonPersonalInference)
		}
		if draft.Action != CandidateActionCreateEntry && draft.Action != CandidateActionUpdateEntry {
			reasons = append(reasons, reviewReasonNonAutomaticAction)
		}
		slices.Sort(reasons)
		draft.ReviewReasons = reasons
		if len(reasons) == 0 {
			draft.Route = CandidateRouteAutomatic
		} else {
			draft.Route = CandidateRoutePendingReview
		}
	}
	return s.next.StoreCandidates(ctx, recordID, routed)
}
