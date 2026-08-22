package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lkarlslund/koder/internal/knowledge"
	knowledgeStore "github.com/lkarlslund/koder/internal/knowledge/store"
	"github.com/lkarlslund/koder/internal/knowledge/store/memory"
)

func TestPersonalEntriesRequireOriginSpecificProvenance(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := memory.New()
	t.Cleanup(func() { _ = store.Close() })
	service := newTestService(t, store, nil)
	if _, err := service.EnsurePersonalChunk(ctx); err != nil {
		t.Fatalf("EnsurePersonalChunk(): %v", err)
	}

	explicit := testEntryCandidate()
	explicit.PersonalOrigin = knowledge.PersonalOriginExplicit
	explicitResult, err := service.CreateEntry(ctx, CreateEntryRequest{ChunkID: PersonalMeChunkID, Entry: explicit})
	if err != nil || explicitResult.Entry.State != knowledge.EntryStateActive || explicitResult.Entry.Scope.Selector != "me" {
		t.Fatalf("explicit personal entry = %#v, %v", explicitResult, err)
	}

	observation := testEvidenceCandidate()
	observation.Type = knowledge.EvidenceTypeToolResult
	observationResult, err := service.CreateEvidence(ctx, CreateEvidenceRequest{Evidence: observation})
	if err != nil {
		t.Fatalf("CreateEvidence(observation): %v", err)
	}
	observed := testEntryCandidate()
	observed.Title = "Observed preference"
	observed.PersonalOrigin = knowledge.PersonalOriginObserved
	observed.ObservedAt = serviceTime.Add(-time.Hour)
	observed.EvidenceIDs = []knowledge.EvidenceID{observationResult.Evidence.ID}
	observedResult, err := service.CreateEntry(ctx, CreateEntryRequest{ChunkID: PersonalMeChunkID, Entry: observed})
	if err != nil || observedResult.Entry.PersonalOrigin != knowledge.PersonalOriginObserved {
		t.Fatalf("observed personal entry = %#v, %v", observedResult, err)
	}
}

func TestObservedPersonalEntryRejectsNonObservationalEvidence(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := memory.New()
	t.Cleanup(func() { _ = store.Close() })
	service := newTestService(t, store, nil)
	_, _ = service.EnsurePersonalChunk(ctx)
	statement := testEvidenceCandidate()
	statement.Type = knowledge.EvidenceTypeUserStatement
	evidence, err := service.CreateEvidence(ctx, CreateEvidenceRequest{Evidence: statement})
	if err != nil {
		t.Fatalf("CreateEvidence(statement): %v", err)
	}
	entry := testEntryCandidate()
	entry.PersonalOrigin = knowledge.PersonalOriginObserved
	entry.ObservedAt = serviceTime
	entry.EvidenceIDs = []knowledge.EvidenceID{evidence.Evidence.ID}
	if _, err := service.CreateEntry(ctx, CreateEntryRequest{ChunkID: PersonalMeChunkID, Entry: entry}); !errors.Is(err, ErrPersonalOriginPolicy) {
		t.Fatalf("CreateEntry(non-observation) error = %v, want ErrPersonalOriginPolicy", err)
	}
	stats, err := store.ScanCanonical(ctx, func(knowledgeStore.CanonicalRecord) error { return nil })
	if err != nil || stats.Entries != 0 {
		t.Fatalf("rejected observed entry stats = %#v, %v", stats, err)
	}
}

func TestSensitiveInferenceRemainsDraftUntilExplicitlyConfirmed(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := memory.New()
	t.Cleanup(func() { _ = store.Close() })
	service := newTestService(t, store, nil)
	_, _ = service.EnsurePersonalChunk(ctx)
	inferred := testEntryCandidate()
	inferred.Title = "Possible medical preference"
	inferred.PersonalOrigin = knowledge.PersonalOriginInferred
	inferred.Confidence = 0.6
	inferred.Risk = []knowledge.RiskClass{knowledge.RiskClassMedical}
	if _, err := service.CreateEntry(ctx, CreateEntryRequest{
		ChunkID: PersonalMeChunkID, Entry: inferred,
	}); !errors.Is(err, ErrReviewRequired) {
		t.Fatalf("CreateEntry(unreviewed inference) error = %v, want ErrReviewRequired", err)
	}
	created, err := service.CreateEntry(ctx, CreateEntryRequest{
		ChunkID: PersonalMeChunkID, Entry: inferred, ReviewApproved: true,
	})
	if err != nil || created.Entry.State != knowledge.EntryStateDraft {
		t.Fatalf("reviewed inference = %#v, %v", created, err)
	}
	confirmed := EntryContentFrom(created.Entry)
	confirmed.PersonalOrigin = knowledge.PersonalOriginExplicit
	confirmed.Confidence = 1
	updated, err := service.UpdateEntry(ctx, UpdateEntryRequest{
		EntryID: created.Entry.ID, ExpectedRevision: 1, Content: confirmed,
		Reason: "user confirmed inference", ReviewApproved: true,
	})
	if err != nil || updated.Entry.State != knowledge.EntryStateActive || updated.Entry.PersonalOrigin != knowledge.PersonalOriginExplicit {
		t.Fatalf("confirmed inference = %#v, %v", updated, err)
	}
}

func TestPersonalOriginCannotBeDowngraded(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := memory.New()
	t.Cleanup(func() { _ = store.Close() })
	service := newTestService(t, store, nil)
	_, _ = service.EnsurePersonalChunk(ctx)
	entry := testEntryCandidate()
	entry.PersonalOrigin = knowledge.PersonalOriginExplicit
	created, err := service.CreateEntry(ctx, CreateEntryRequest{ChunkID: PersonalMeChunkID, Entry: entry})
	if err != nil {
		t.Fatalf("CreateEntry(): %v", err)
	}
	downgrade := EntryContentFrom(created.Entry)
	downgrade.PersonalOrigin = knowledge.PersonalOriginInferred
	downgrade.Confidence = 0.5
	if _, err := service.UpdateEntry(ctx, UpdateEntryRequest{
		EntryID: created.Entry.ID, ExpectedRevision: 1, Content: downgrade,
	}); !errors.Is(err, ErrPersonalOriginPolicy) {
		t.Fatalf("UpdateEntry(downgrade) error = %v, want ErrPersonalOriginPolicy", err)
	}
	got, _ := service.Entry(ctx, created.Entry.ID)
	if got.PersonalOrigin != knowledge.PersonalOriginExplicit || got.Revision.Number != 1 {
		t.Fatalf("downgrade changed canonical entry: %#v", got)
	}
}

func TestPersonalMeEntriesCannotEscapeScopeOrOriginLabels(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := memory.New()
	t.Cleanup(func() { _ = store.Close() })
	service := newTestService(t, store, nil)
	_, _ = service.EnsurePersonalChunk(ctx)

	global := testEntryCandidate()
	global.Scope = knowledge.Scope{Kind: knowledge.ScopeKindGlobal}
	if _, err := service.CreateEntry(ctx, CreateEntryRequest{ChunkID: PersonalMeChunkID, Entry: global}); !errors.Is(err, ErrPersonalOriginPolicy) {
		t.Fatalf("CreateEntry(global in personal/me) error = %v, want ErrPersonalOriginPolicy", err)
	}
	unlabelled := testEntryCandidate()
	if _, err := service.CreateEntry(ctx, CreateEntryRequest{ChunkID: PersonalMeChunkID, Entry: unlabelled}); !errors.Is(err, ErrPersonalOriginPolicy) {
		t.Fatalf("CreateEntry(unlabelled personal/me) error = %v, want ErrPersonalOriginPolicy", err)
	}

	explicit := testEntryCandidate()
	explicit.PersonalOrigin = knowledge.PersonalOriginExplicit
	created, err := service.CreateEntry(ctx, CreateEntryRequest{ChunkID: PersonalMeChunkID, Entry: explicit})
	if err != nil {
		t.Fatal(err)
	}
	content := EntryContentFrom(created.Entry)
	content.Scope = knowledge.Scope{Kind: knowledge.ScopeKindGlobal}
	content.PersonalOrigin = knowledge.PersonalOriginUnspecified
	if _, err := service.UpdateEntry(ctx, UpdateEntryRequest{
		EntryID: created.Entry.ID, ExpectedRevision: created.Entry.Revision.Number, Content: content,
	}); !errors.Is(err, ErrPersonalOriginPolicy) {
		t.Fatalf("UpdateEntry(personal/me scope escape) error = %v, want ErrPersonalOriginPolicy", err)
	}
}

func TestSensitiveInferenceRemainsDraftEvenWhenClassifierAllowsIt(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := memory.New()
	t.Cleanup(func() { _ = store.Close() })
	classifier := &recordingClassifier{result: knowledge.ClassificationResult{Decision: knowledge.ClassificationDecisionAllow}}
	service := newTestService(t, store, classifier)
	_, _ = service.EnsurePersonalChunk(ctx)

	sensitive := testEntryCandidate()
	sensitive.Title = "Possible medical preference"
	sensitive.PersonalOrigin = knowledge.PersonalOriginInferred
	sensitive.Confidence = 0.6
	sensitive.Risk = []knowledge.RiskClass{knowledge.RiskClassMedical}
	created, err := service.CreateEntry(ctx, CreateEntryRequest{ChunkID: PersonalMeChunkID, Entry: sensitive})
	if err != nil || created.Entry.State != knowledge.EntryStateDraft {
		t.Fatalf("CreateEntry(classifier-allowed sensitive inference) = %#v, %v", created, err)
	}

	ordinary := testEntryCandidate()
	ordinary.Title = "Possible color preference"
	ordinary.PersonalOrigin = knowledge.PersonalOriginInferred
	ordinary.Confidence = 0.6
	active, err := service.CreateEntry(ctx, CreateEntryRequest{ChunkID: PersonalMeChunkID, Entry: ordinary})
	if err != nil || active.Entry.State != knowledge.EntryStateActive {
		t.Fatalf("CreateEntry(low-risk inference) = %#v, %v", active, err)
	}
	content := EntryContentFrom(active.Entry)
	content.Risk = []knowledge.RiskClass{knowledge.RiskClassPersonalSensitive}
	updated, err := service.UpdateEntry(ctx, UpdateEntryRequest{
		EntryID: active.Entry.ID, ExpectedRevision: active.Entry.Revision.Number, Content: content,
	})
	if err != nil || updated.Entry.State != knowledge.EntryStateDraft {
		t.Fatalf("UpdateEntry(add sensitive risk) = %#v, %v", updated, err)
	}
}
