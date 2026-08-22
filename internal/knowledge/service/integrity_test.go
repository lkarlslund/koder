package service

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/lkarlslund/koder/internal/knowledge"
	knowledgeStore "github.com/lkarlslund/koder/internal/knowledge/store"
	"github.com/lkarlslund/koder/internal/knowledge/store/memory"
)

type integrityFaultStore struct {
	*memory.Store
	omitChunkIndex bool
	omitHistory    bool
	omitLexical    bool
}

func (s *integrityFaultStore) LookupLexicalPostings(ctx context.Context, request knowledgeStore.LexicalPostingRequest) (knowledgeStore.LexicalPostingPage, error) {
	page, err := s.Store.LookupLexicalPostings(ctx, request)
	if err == nil && s.omitLexical {
		page.Postings = nil
		page.DocumentFrequencies = nil
		page.NextCursor = ""
	}
	return page, err
}

func (s *integrityFaultStore) ListChunks(ctx context.Context, request knowledgeStore.ChunkListRequest) (knowledgeStore.ChunkPage, error) {
	page, err := s.Store.ListChunks(ctx, request)
	if err == nil && s.omitChunkIndex {
		page.Chunks = nil
		page.NextCursor = ""
	}
	return page, err
}

func (s *integrityFaultStore) ListRevisions(ctx context.Context, request knowledgeStore.RevisionListRequest) (knowledgeStore.RevisionPage, error) {
	if s.omitHistory {
		return knowledgeStore.RevisionPage{}, knowledgeStore.ErrNotFound
	}
	return s.Store.ListRevisions(ctx, request)
}

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
	if err != nil || report.IssueCount != 0 || report.Truncated || report.Scanned.Chunks != 1 || report.Scanned.Entries != 1 ||
		report.RevisionsScanned != 2 || report.IndexesChecked < 2 {
		t.Fatalf("ScanIntegrity() = %#v, %v", report, err)
	}
}

func TestScanIntegrityChecksIndexesAndRevisionHistory(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	base := memory.New()
	t.Cleanup(func() { _ = base.Close() })
	creator := newTestService(t, base, nil)
	chunk, err := creator.CreateChunk(ctx, CreateChunkRequest{Chunk: testChunkCandidate()})
	if err != nil {
		t.Fatal(err)
	}
	entry, err := creator.CreateEntry(ctx, CreateEntryRequest{ChunkID: chunk.Chunk.ID, Entry: testEntryCandidate()})
	if err != nil {
		t.Fatal(err)
	}
	service := newTestService(t, &integrityFaultStore{Store: base, omitChunkIndex: true, omitHistory: true, omitLexical: true}, nil)
	report, err := service.ScanIntegrity(ctx, IntegrityScanRequest{})
	if err != nil {
		t.Fatal(err)
	}
	want := map[IntegrityIssueKind]bool{IntegrityIndexMissing: true, IntegrityRevisionMissing: true}
	for _, issue := range report.Issues {
		delete(want, issue.Kind)
	}
	if len(want) != 0 {
		t.Fatalf("missing issue kinds %v; report=%#v", want, report)
	}
	if !slices.ContainsFunc(report.Issues, func(issue IntegrityIssue) bool {
		return issue.Kind == IntegrityIndexMissing && issue.ObjectKind == "entry" && issue.ObjectID == string(entry.Entry.ID)
	}) {
		t.Fatalf("missing lexical entry index issue; report=%#v", report)
	}
}

func TestScanIntegrityChecksImportedPackageProvenance(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := memory.New()
	t.Cleanup(func() { _ = store.Close() })
	service := newTestService(t, store, nil)
	created, err := service.CreateChunk(ctx, CreateChunkRequest{Chunk: testChunkCandidate()})
	if err != nil {
		t.Fatal(err)
	}
	err = store.Update(ctx, func(tx knowledgeStore.WriteTx) error {
		chunk, err := tx.Chunk(ctx, created.Chunk.ID)
		if err != nil {
			return err
		}
		chunk.Publisher.ID = "publisher-with-missing-name"
		chunk.Revision.Number++
		chunk.Revision.ID = "00000000-0000-7000-8000-000000000099"
		chunk.Revision.Actor = knowledge.Actor{Kind: knowledge.ActorKindImport, ID: "00000000-0000-7000-8000-000000000098"}
		chunk.Revision.CreatedAt = chunk.UpdatedAt.Add(time.Second)
		chunk.UpdatedAt = chunk.Revision.CreatedAt
		return tx.PutChunk(ctx, chunk, created.Chunk.Revision.Number)
	})
	if err != nil {
		t.Fatal(err)
	}
	report, err := service.ScanIntegrity(ctx, IntegrityScanRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if report.PackagesChecked != 1 || !slices.ContainsFunc(report.Issues, func(issue IntegrityIssue) bool { return issue.Kind == IntegrityPackageProvenance }) {
		t.Fatalf("package integrity report = %#v", report)
	}
}

func TestScanIntegrityHonorsOperationalPolicy(t *testing.T) {
	t.Parallel()
	store := memory.New()
	t.Cleanup(func() { _ = store.Close() })
	service, err := New(Config{
		Store: store, Actor: ContextActorSource(knowledge.Actor{Kind: knowledge.ActorKindUser, ID: "user:test"}),
		Operational: OperationalPolicyFunc(func(context.Context, knowledge.Actor, OperationalAction) error { return errors.New("denied") }),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.ScanIntegrity(context.Background(), IntegrityScanRequest{}); !errors.Is(err, ErrOperationalPolicyDenied) {
		t.Fatalf("ScanIntegrity() error = %v", err)
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
