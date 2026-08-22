package service

import (
	"context"
	"testing"

	"github.com/lkarlslund/koder/internal/knowledge"
	"github.com/lkarlslund/koder/internal/knowledge/store/memory"
)

func TestScanIntegrityReportsHealthyCanonicalGraph(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := memory.New()
	t.Cleanup(func() { _ = store.Close() })
	service := newTestService(t, store, nil)
	chunk, err := service.CreateChunk(ctx, CreateChunkRequest{Chunk: testChunkCandidate()})
	if err != nil {
		t.Fatalf("CreateChunk(): %v", err)
	}
	if _, err := service.CreateEntry(ctx, CreateEntryRequest{ChunkID: chunk.Chunk.ID, Entry: testEntryCandidate()}); err != nil {
		t.Fatalf("CreateEntry(): %v", err)
	}
	report, err := service.ScanIntegrity(ctx, IntegrityScanRequest{})
	if err != nil || report.IssueCount != 0 || report.Truncated || report.Scanned.Chunks != 1 || report.Scanned.Entries != 1 {
		t.Fatalf("ScanIntegrity() = %#v, %v", report, err)
	}
}

func TestScanIntegrityBoundsAndCountsIssues(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := memory.New()
	t.Cleanup(func() { _ = store.Close() })
	service := newTestService(t, store, nil)
	candidate := testChunkCandidate()
	candidate.DependencyIDs = []knowledge.ChunkID{"01a01688-fc5d-7f7d-8bb8-de244977f8af"}
	if _, err := service.CreateChunk(ctx, CreateChunkRequest{Chunk: candidate}); err != nil {
		t.Fatalf("CreateChunk(): %v", err)
	}
	if _, err := service.CreateEvidence(ctx, CreateEvidenceRequest{Evidence: testEvidenceCandidate()}); err != nil {
		t.Fatalf("CreateEvidence(): %v", err)
	}
	report, err := service.ScanIntegrity(ctx, IntegrityScanRequest{IssueLimit: 1})
	if err != nil || report.IssueCount != 2 || len(report.Issues) != 1 || !report.Truncated {
		t.Fatalf("bounded ScanIntegrity() = %#v, %v", report, err)
	}
}

func TestDeriveIntegrityIssuesFindsReferentialAndRelationshipDamage(t *testing.T) {
	t.Parallel()
	chunkID := knowledge.ChunkID("019f132e-4f3a-739a-9ab2-5198dcd19e67")
	missingChunk := knowledge.ChunkID("019f132e-4f3a-739a-9ab2-5198dcd19e68")
	entryID := knowledge.EntryID("01a01f76-1ff6-7c1d-967a-66ad5703dd33")
	missingEntry := knowledge.EntryID("01a01f76-1ff6-7c1d-967a-66ad5703dd34")
	missingEvidence := knowledge.EvidenceID("01a01688-fc6b-7a53-a907-4f903461820e")
	unreferencedEvidence := knowledge.EvidenceID("01a01688-fc6b-7a53-a907-4f903461820f")
	chunks := map[knowledge.ChunkID]knowledge.Chunk{
		chunkID: {ID: chunkID, DependencyIDs: []knowledge.ChunkID{missingChunk}, Counts: knowledge.ChunkCounts{Entries: 99}},
	}
	entries := map[knowledge.EntryID]knowledge.Entry{
		entryID: {
			ID: entryID, ChunkID: missingChunk, State: knowledge.EntryStateSuperseded,
			SupersededByID: missingEntry, EvidenceIDs: []knowledge.EvidenceID{missingEvidence},
		},
	}
	source := knowledge.ObjectRef{Kind: knowledge.ObjectKindEntry, ID: string(entryID)}
	target := knowledge.ObjectRef{Kind: knowledge.ObjectKindChunk, ID: string(missingChunk)}
	links := map[knowledge.LinkID]knowledge.Link{
		"01a020a6-84d5-7b03-a995-bb2cfb4528b0": {
			ID: "01a020a6-84d5-7b03-a995-bb2cfb4528b0", Source: source, Target: target,
			Kind: knowledge.LinkKindRelatedTo, EvidenceIDs: []knowledge.EvidenceID{missingEvidence},
		},
		"01a020a6-84d5-7b03-a995-bb2cfb4528b1": {
			ID: "01a020a6-84d5-7b03-a995-bb2cfb4528b1", Source: target, Target: source,
			Kind: knowledge.LinkKindRelatedTo,
		},
	}
	evidence := map[knowledge.EvidenceID]knowledge.Evidence{
		unreferencedEvidence: {ID: unreferencedEvidence},
	}
	issues := deriveIntegrityIssues(chunks, entries, links, evidence)
	want := map[IntegrityIssueKind]bool{
		IntegrityOrphanEntry: true, IntegrityUnreferencedEvidence: true,
		IntegrityMissingDependency: true, IntegrityMissingEvidence: true,
		IntegrityBrokenSupersession: true, IntegrityBrokenLinkTarget: true,
		IntegrityBrokenLinkSource: true, IntegrityDuplicateLink: true,
		IntegrityChunkCountMismatch: true,
	}
	for _, issue := range issues {
		delete(want, issue.Kind)
	}
	if len(want) != 0 {
		t.Fatalf("missing integrity issue kinds %v; got %#v", want, issues)
	}
}
