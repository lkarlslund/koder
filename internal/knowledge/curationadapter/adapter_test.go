package curationadapter

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lkarlslund/koder/internal/knowledge"
	"github.com/lkarlslund/koder/internal/knowledge/curation"
	knowledgeService "github.com/lkarlslund/koder/internal/knowledge/service"
	knowledgeStore "github.com/lkarlslund/koder/internal/knowledge/store"
	"github.com/lkarlslund/koder/internal/knowledge/store/memory"
)

func TestLowRiskApplierBridgesValidatedCandidateToAtomicService(t *testing.T) {
	t.Parallel()
	store := memory.New()
	t.Cleanup(func() { _ = store.Close() })
	now := time.Date(2026, 8, 22, 20, 0, 0, 0, time.UTC)
	ids := []string{
		"00000000-0000-7000-8000-000000000081", "00000000-0000-7000-8000-000000000082",
		"00000000-0000-7000-8000-000000000083", "00000000-0000-7000-8000-000000000084",
		"00000000-0000-7000-8000-000000000085",
	}
	service, err := knowledgeService.New(knowledgeService.Config{
		Store: store, Actor: knowledgeService.ContextActorSource(knowledge.Actor{Kind: knowledge.ActorKindSystem, ID: "system:curator"}),
		Now: func() time.Time { return now }, NewID: func() string { value := ids[0]; ids = ids[1:]; return value },
	})
	if err != nil {
		t.Fatal(err)
	}
	chunk, err := service.CreateChunk(context.Background(), knowledgeService.CreateChunkRequest{Chunk: knowledge.Chunk{
		Title: "Disk tools", Kind: knowledge.ChunkKindReference, Scope: knowledge.Scope{Kind: knowledge.ScopeKindGlobal},
	}})
	if err != nil {
		t.Fatal(err)
	}
	record := knowledge.CurationRecord{
		ID: "00000000-0000-7000-8000-000000000080",
		Source: knowledge.CompletedTurnRef{
			SessionID: "00000000-0000-7000-8000-000000000091", ChatID: "00000000-0000-7000-8000-000000000092",
			UserItemID: "00000000-0000-7000-8000-000000000093", AssistantItemID: "00000000-0000-7000-8000-000000000094",
			SealedAt: now.Add(-time.Minute),
		},
		State: knowledge.CurationStateCandidatesReady,
	}
	draft := curation.CandidateDraft{
		Action: curation.CandidateActionCreateEntry, Route: curation.CandidateRouteAutomatic, ChunkID: chunk.Chunk.ID,
		Entry: curation.EntryDraft{
			Kind: knowledge.EntryKindFact, Title: "Use sfdisk", Summary: "fdisk is unavailable here.",
			Scope: knowledge.Scope{Kind: knowledge.ScopeKindGlobal}, Confidence: 0.9,
		},
		Reason: "Successful fallback", SourceItemIDs: []string{record.Source.UserItemID, record.Source.AssistantItemID},
	}
	result, err := (LowRiskApplier{Service: service}).Apply(context.Background(), record, draft)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Created || result.Entry.Verification.Status != knowledge.VerificationStatusVerified {
		t.Fatalf("Apply() = %#v", result)
	}
	draft.Action = curation.CandidateActionSupersedeEntry
	if _, err := (LowRiskApplier{Service: service}).Apply(context.Background(), record, draft); !errors.Is(err, knowledgeService.ErrReviewRequired) {
		t.Fatalf("Apply(review action) error = %v", err)
	}
	stats, err := store.ScanCanonical(context.Background(), func(knowledgeStore.CanonicalRecord) error { return nil })
	if err != nil || stats.Entries != 1 || stats.Evidence != 1 {
		t.Fatalf("canonical stats = %#v, %v", stats, err)
	}
}
