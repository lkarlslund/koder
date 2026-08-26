package curationadapter

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lkarlslund/koder/internal/memory"
	"github.com/lkarlslund/koder/internal/memory/curation"
	memoryService "github.com/lkarlslund/koder/internal/memory/service"
	memoryStoreAPI "github.com/lkarlslund/koder/internal/memory/store"
	memoryBackend "github.com/lkarlslund/koder/internal/memory/store/memory"
)

func TestLowRiskApplierBridgesValidatedCandidateToAtomicService(t *testing.T) {
	t.Parallel()
	store := memoryBackend.New()
	t.Cleanup(func() { _ = store.Close() })
	now := time.Date(2026, 8, 22, 20, 0, 0, 0, time.UTC)
	ids := []string{
		"00000000-0000-7000-8000-000000000081", "00000000-0000-7000-8000-000000000082",
		"00000000-0000-7000-8000-000000000083", "00000000-0000-7000-8000-000000000084",
		"00000000-0000-7000-8000-000000000085",
	}
	service, err := memoryService.New(memoryService.Config{
		Store: store, Actor: memoryService.ContextActorSource(memory.Actor{Kind: memory.ActorKindSystem, ID: "system:curator"}),
		Now: func() time.Time { return now }, NewID: func() string { value := ids[0]; ids = ids[1:]; return value },
	})
	if err != nil {
		t.Fatal(err)
	}
	chunk, err := service.CreateChunk(context.Background(), memoryService.CreateChunkRequest{Chunk: memory.Chunk{
		Title: "Disk tools", Kind: memory.ChunkKindReference, Scope: memory.Scope{Kind: memory.ScopeKindGlobal},
	}})
	if err != nil {
		t.Fatal(err)
	}
	record := memory.CurationRecord{
		ID: "00000000-0000-7000-8000-000000000080",
		Source: memory.CompletedTurnRef{
			SessionID: "00000000-0000-7000-8000-000000000091", ChatID: "00000000-0000-7000-8000-000000000092",
			UserItemID: "00000000-0000-7000-8000-000000000093", AssistantItemID: "00000000-0000-7000-8000-000000000094",
			SealedAt: now.Add(-time.Minute),
		},
		State: memory.CurationStateCandidatesReady,
	}
	draft := curation.CandidateDraft{
		Action: curation.CandidateActionCreateEntry, Route: curation.CandidateRouteAutomatic, ChunkID: chunk.Chunk.ID,
		Entry: curation.EntryDraft{
			Kind: memory.EntryKindFact, Title: "Use sfdisk", Summary: "fdisk is unavailable here.",
			Scope: memory.Scope{Kind: memory.ScopeKindGlobal}, Confidence: 0.9,
		},
		Reason: "Successful fallback", SourceItemIDs: []string{record.Source.UserItemID, record.Source.AssistantItemID},
	}
	result, err := (LowRiskApplier{Service: service}).Apply(context.Background(), record, draft)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Created || result.Entry.Verification.Status != memory.VerificationStatusVerified {
		t.Fatalf("Apply() = %#v", result)
	}
	draft.Action = curation.CandidateActionSupersedeEntry
	if _, err := (LowRiskApplier{Service: service}).Apply(context.Background(), record, draft); !errors.Is(err, memoryService.ErrReviewRequired) {
		t.Fatalf("Apply(review action) error = %v", err)
	}
	stats, err := store.ScanCanonical(context.Background(), func(memoryStoreAPI.CanonicalRecord) error { return nil })
	if err != nil || stats.Entries != 1 || stats.Evidence != 1 {
		t.Fatalf("canonical stats = %#v, %v", stats, err)
	}
}
