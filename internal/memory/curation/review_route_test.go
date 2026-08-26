package curation

import (
	"context"
	"testing"

	"github.com/lkarlslund/koder/internal/memory"
)

func TestReviewRoutingSinkKeepsSensitiveAndNonAutomaticCandidatesReviewable(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		mutate func(*CandidateDraft)
		reason string
	}{
		{name: "classifier review", mutate: func(value *CandidateDraft) { value.Classification.Decision = memory.ClassificationDecisionReview }, reason: reviewReasonClassification},
		{name: "risk", mutate: func(value *CandidateDraft) { value.Entry.Risk = []memory.RiskClass{memory.RiskClassMedical} }, reason: reviewReasonRisk},
		{name: "personal inference", mutate: func(value *CandidateDraft) {
			value.Entry.Scope = memory.Scope{Kind: memory.ScopeKindPersonal, Selector: "me"}
			value.Entry.PersonalOrigin = memory.PersonalOriginInferred
		}, reason: reviewReasonPersonalInference},
		{name: "contradiction", mutate: func(value *CandidateDraft) { value.Action = CandidateActionContradictEntry }, reason: reviewReasonNonAutomaticAction},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := NewMemoryCandidateStore()
			sink, err := NewReviewRoutingSink(store)
			if err != nil {
				t.Fatal(err)
			}
			draft := dedupTestDraft()
			draft.Classification.Decision = memory.ClassificationDecisionAllow
			test.mutate(&draft)
			id := memory.CurationRecordID("00000000-0000-7000-8000-000000000020")
			if count, err := sink.StoreCandidates(context.Background(), id, []CandidateDraft{draft}); err != nil || count != 1 {
				t.Fatalf("StoreCandidates() = %d, %v", count, err)
			}
			stored := store.Candidates(context.Background(), id)
			if len(stored) != 1 || stored[0].Route != CandidateRoutePendingReview || !containsString(stored[0].ReviewReasons, test.reason) {
				t.Fatalf("routed candidate = %#v", stored)
			}
		})
	}
}

func TestReviewRoutingSinkMarksOnlyLowRiskCreateUpdateAutomatic(t *testing.T) {
	t.Parallel()
	store := NewMemoryCandidateStore()
	sink, err := NewReviewRoutingSink(store)
	if err != nil {
		t.Fatal(err)
	}
	draft := dedupTestDraft()
	draft.Classification.Decision = memory.ClassificationDecisionAllow
	id := memory.CurationRecordID("00000000-0000-7000-8000-000000000020")
	if _, err := sink.StoreCandidates(context.Background(), id, []CandidateDraft{draft}); err != nil {
		t.Fatal(err)
	}
	stored := store.Candidates(context.Background(), id)
	if len(stored) != 1 || stored[0].Route != CandidateRouteAutomatic || len(stored[0].ReviewReasons) != 0 {
		t.Fatalf("automatic candidate = %#v", stored)
	}
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
