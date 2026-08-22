package service

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/lkarlslund/koder/internal/knowledge"
	"github.com/lkarlslund/koder/internal/knowledge/kpackage"
	knowledgeStore "github.com/lkarlslund/koder/internal/knowledge/store"
	"github.com/lkarlslund/koder/internal/knowledge/store/memory"
)

type updateCountingStore struct {
	knowledgeStore.Store
	updates int
}

func (s *updateCountingStore) Update(ctx context.Context, fn func(knowledgeStore.WriteTx) error) error {
	s.updates++
	return s.Store.Update(ctx, fn)
}

func TestPreviewImportReportsAdditionsAndDependenciesWithoutWrites(t *testing.T) {
	t.Parallel()
	backend := memory.New()
	t.Cleanup(func() { _ = backend.Close() })
	store := &updateCountingStore{Store: backend}
	service := newTestService(t, store, nil)
	dependency, err := service.CreateChunk(context.Background(), CreateChunkRequest{Chunk: testChunkCandidate()})
	if err != nil {
		t.Fatal(err)
	}
	store.updates = 0

	pkg := previewPackage("01a02b00-0000-7000-8000-000000000001", "Imported tools")
	pkg.Manifest.Dependencies = []kpackage.Dependency{{
		PackageID: "01a02b00-0000-7000-8000-000000000010", ChunkID: string(dependency.Chunk.ID),
		Version: "1.0.0", Title: dependency.Chunk.Title, Required: true,
	}}
	preview, err := service.PreviewImport(context.Background(), pkg)
	if err != nil {
		t.Fatalf("PreviewImport() error = %v", err)
	}
	if store.updates != 0 {
		t.Fatalf("PreviewImport() opened %d write transactions", store.updates)
	}
	if preview.Summary.Additions != 1 || preview.Summary.References != 1 || preview.Summary.Blockers != 0 || !preview.ReadyToStage {
		t.Fatalf("PreviewImport() = %#v", preview)
	}
	if len(preview.Impacts) != 2 || preview.Impacts[0].Action != ImportImpactAdd || preview.Impacts[1].Action != ImportImpactReference {
		t.Fatalf("PreviewImport() impacts = %#v", preview.Impacts)
	}
}

func TestGenericPackageImportCannotTargetReservedPersonalKnowledge(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	backend := memory.New()
	t.Cleanup(func() { _ = backend.Close() })
	store := &updateCountingStore{Store: backend}
	service := newTestService(t, store, nil)

	for _, test := range []struct {
		name string
		pkg  kpackage.ValidatedPackage
	}{
		{name: "reserved ID", pkg: previewPackage(string(PersonalMeChunkID), "Forged personal package")},
		{name: "reserved scope", pkg: func() kpackage.ValidatedPackage {
			pkg := previewPackage("01a02b00-0000-7000-8000-000000000099", "Aliased personal package")
			pkg.Manifest.Chunk.Kind = knowledge.ChunkKindPersonal
			pkg.Manifest.Chunk.Scope = knowledge.Scope{Kind: knowledge.ScopeKindPersonal, Selector: "me"}
			return pkg
		}()},
	} {
		t.Run(test.name, func(t *testing.T) {
			store.updates = 0
			if _, err := service.PreviewImport(ctx, test.pkg); !errors.Is(err, ErrProtectedChunk) {
				t.Fatalf("PreviewImport(personal) error = %v, want ErrProtectedChunk", err)
			}
			if store.updates != 0 {
				t.Fatalf("rejected personal preview opened %d write transactions", store.updates)
			}
		})
	}

	pkg := previewPackage(string(PersonalMeChunkID), "Forged activation")
	err := backend.Update(ctx, func(tx knowledgeStore.WriteTx) error {
		_, err := service.activateImportTransaction(ctx, tx,
			knowledge.Actor{Kind: knowledge.ActorKindUser, ID: "user:test"}, pkg, ImportPreview{}, ImportConflictPolicyMerge, serviceTime)
		return err
	})
	if !errors.Is(err, ErrProtectedChunk) {
		t.Fatalf("activateImportTransaction(personal) error = %v, want ErrProtectedChunk", err)
	}
}

func TestPreviewImportReportsConflictsAndUnchangedRecords(t *testing.T) {
	t.Parallel()
	backend := memory.New()
	t.Cleanup(func() { _ = backend.Close() })
	store := &updateCountingStore{Store: backend}
	service := newTestService(t, store, nil)
	createdChunk, err := service.CreateChunk(context.Background(), CreateChunkRequest{Chunk: testChunkCandidate()})
	if err != nil {
		t.Fatal(err)
	}
	createdEntry, err := service.CreateEntry(context.Background(), CreateEntryRequest{
		ChunkID: createdChunk.Chunk.ID,
		Entry:   knowledge.Entry{Kind: knowledge.EntryKindFact, Title: "Existing fact", Body: "Original body\n"},
	})
	if err != nil {
		t.Fatal(err)
	}
	store.updates = 0

	pkg := previewPackage(string(createdChunk.Chunk.ID), createdChunk.Chunk.Title)
	pkg.Entries = []knowledge.Entry{createdEntry.Entry}
	pkg.Entries[0].Body = "Conflicting body\n"
	preview, err := service.PreviewImport(context.Background(), pkg)
	if err != nil {
		t.Fatalf("PreviewImport() error = %v", err)
	}
	if store.updates != 0 {
		t.Fatalf("PreviewImport() opened %d write transactions", store.updates)
	}
	if preview.Summary.Unchanged != 1 || preview.Summary.Conflicts != 1 || preview.Summary.Blockers != 1 || preview.ReadyToStage {
		t.Fatalf("PreviewImport() = %#v", preview)
	}
	if !slices.ContainsFunc(preview.Impacts, func(impact ImportImpact) bool {
		return impact.Kind == knowledgeStore.RecordKindEntry && impact.Action == ImportImpactConflict && impact.Reason == "id_exists_with_different_content"
	}) {
		t.Fatalf("PreviewImport() impacts = %#v", preview.Impacts)
	}
}

func TestPreviewImportReportsMissingDependencyAndCrossChunkLink(t *testing.T) {
	t.Parallel()
	backend := memory.New()
	t.Cleanup(func() { _ = backend.Close() })
	store := &updateCountingStore{Store: backend}
	service := newTestService(t, store, nil)
	pkg := previewPackage("01a02b00-0000-7000-8000-000000000001", "Imported tools")
	const dependencyID = "01a02b00-0000-7000-8000-000000000002"
	pkg.Manifest.Dependencies = []kpackage.Dependency{{
		PackageID: "01a02b00-0000-7000-8000-000000000010", ChunkID: dependencyID,
		Version: "1.0.0", Title: "Missing dependency", Required: true,
	}}
	pkg.Links = []knowledge.Link{{
		ID:     "01a02b00-0000-7000-8000-000000000004",
		Source: knowledge.ObjectRef{Kind: knowledge.ObjectKindChunk, ID: pkg.Manifest.Chunk.ID},
		Target: knowledge.ObjectRef{Kind: knowledge.ObjectKindChunk, ID: dependencyID},
		Kind:   knowledge.LinkKindRelatedTo,
	}}
	preview, err := service.PreviewImport(context.Background(), pkg)
	if err != nil {
		t.Fatalf("PreviewImport() error = %v", err)
	}
	if store.updates != 0 || preview.Summary.MissingDependencies != 1 || preview.Summary.CrossChunkLinks != 1 || preview.Summary.Blockers != 1 || preview.ReadyToStage {
		t.Fatalf("PreviewImport() = %#v, updates=%d", preview, store.updates)
	}
	if !slices.ContainsFunc(preview.Impacts, func(impact ImportImpact) bool {
		return impact.ID == dependencyID && impact.Action == ImportImpactMissingDependency && impact.Required && impact.Blocking
	}) {
		t.Fatalf("PreviewImport() impacts = %#v", preview.Impacts)
	}
}

func TestPreviewImportDetectsLinkAndEvidenceIdentityConflicts(t *testing.T) {
	t.Parallel()
	backend := memory.New()
	t.Cleanup(func() { _ = backend.Close() })
	store := &updateCountingStore{Store: backend}
	service := newTestService(t, store, nil)
	first, err := service.CreateChunk(context.Background(), CreateChunkRequest{Chunk: testChunkCandidate()})
	if err != nil {
		t.Fatal(err)
	}
	secondCandidate := testChunkCandidate()
	secondCandidate.Title = "Second chunk"
	second, err := service.CreateChunk(context.Background(), CreateChunkRequest{Chunk: secondCandidate})
	if err != nil {
		t.Fatal(err)
	}
	entry, err := service.CreateEntry(context.Background(), CreateEntryRequest{ChunkID: first.Chunk.ID, Entry: testEntryCandidate()})
	if err != nil {
		t.Fatal(err)
	}
	evidence, err := service.CreateEvidence(context.Background(), CreateEvidenceRequest{Evidence: testEvidenceCandidate()})
	if err != nil {
		t.Fatal(err)
	}
	link, err := service.CreateLink(context.Background(), CreateLinkRequest{Link: knowledge.Link{
		Source: knowledge.ObjectRef{Kind: knowledge.ObjectKindEntry, ID: string(entry.Entry.ID)},
		Target: knowledge.ObjectRef{Kind: knowledge.ObjectKindChunk, ID: string(second.Chunk.ID)},
		Kind:   knowledge.LinkKindAppliesTo,
	}})
	if err != nil {
		t.Fatal(err)
	}
	store.updates = 0

	pkg := previewPackage(string(first.Chunk.ID), first.Chunk.Title)
	pkg.Entries = []knowledge.Entry{entry.Entry}
	pkg.Evidence = []knowledge.Evidence{evidence.Evidence}
	duplicateEvidence := evidence.Evidence
	duplicateEvidence.ID = "01a02b00-0000-7000-8000-000000000020"
	pkg.Evidence = append(pkg.Evidence, duplicateEvidence)
	pkg.Links = []knowledge.Link{link.Link}
	equivalentLink := link.Link
	equivalentLink.ID = "01a02b00-0000-7000-8000-000000000021"
	pkg.Links = append(pkg.Links, equivalentLink)

	preview, err := service.PreviewImport(context.Background(), pkg)
	if err != nil {
		t.Fatalf("PreviewImport() error = %v", err)
	}
	if store.updates != 0 || preview.Summary.Unchanged != 4 || preview.Summary.Conflicts != 2 || preview.Summary.Blockers != 2 {
		t.Fatalf("PreviewImport() = %#v, updates=%d", preview, store.updates)
	}
	for _, reason := range []string{"equivalent_link_exists", "evidence_source_exists"} {
		if !slices.ContainsFunc(preview.Impacts, func(impact ImportImpact) bool { return impact.Reason == reason && impact.Blocking }) {
			t.Errorf("PreviewImport() lacks %q: %#v", reason, preview.Impacts)
		}
	}
}

func TestPreviewImportRequiresReviewAndRejectsSecrets(t *testing.T) {
	t.Parallel()
	backend := memory.New()
	t.Cleanup(func() { _ = backend.Close() })
	store := &updateCountingStore{Store: backend}
	service := newTestService(t, store, nil)

	reviewPackage := previewPackage("01a02b00-0000-7000-8000-000000000001", "Medical notes")
	reviewPackage.Manifest.Chunk.Risk = []knowledge.RiskClass{knowledge.RiskClassMedical}
	reviewPackage.Manifest.Chunk.Locale = "en-DK"
	reviewPackage.Manifest.Chunk.Domain = "medicine"
	reviewPackage.Manifest.Chunk.SourcePolicy = "Require current authoritative medical sources."
	reviewPackage.Manifest.Chunk.ReviewAfter = serviceTime.AddDate(0, 1, 0)
	preview, err := service.PreviewImport(context.Background(), reviewPackage)
	if err != nil || !preview.ReviewRequired || preview.ReadyToStage || preview.Summary.Blockers != 0 {
		t.Fatalf("PreviewImport(review) = %#v, %v", preview, err)
	}

	rejected := previewPackage("01a02b00-0000-7000-8000-000000000002", "Unsafe notes")
	rejected.Manifest.Chunk.Description = "password=synthetic-package-secret-84291"
	preview, err = service.PreviewImport(context.Background(), rejected)
	if !errors.Is(err, kpackage.ErrRejectedContent) || preview.Classification.Decision != knowledge.ClassificationDecisionReject {
		t.Fatalf("PreviewImport(rejected) = %#v, %v", preview, err)
	}
	if store.updates != 0 {
		t.Fatalf("PreviewImport() opened %d write transactions", store.updates)
	}
}

func TestPreviewImportAuthorizesIncomingLinks(t *testing.T) {
	t.Parallel()
	backend := memory.New()
	t.Cleanup(func() { _ = backend.Close() })
	store := &updateCountingStore{Store: backend}
	service := newTestService(t, store, nil)
	service.chunkPolicy = denyChunkAction(ChunkPolicyLinkCreate)
	pkg := previewPackage("01a02b00-0000-7000-8000-000000000001", "Denied link")
	pkg.Entries = []knowledge.Entry{{
		ID: "01a02b00-0000-7000-8000-000000000002", ChunkID: knowledge.ChunkID(pkg.Manifest.Chunk.ID), Title: "Imported entry",
	}}
	pkg.Links = []knowledge.Link{{
		ID:     "01a02b00-0000-7000-8000-000000000003",
		Source: knowledge.ObjectRef{Kind: knowledge.ObjectKindEntry, ID: string(pkg.Entries[0].ID)},
		Target: knowledge.ObjectRef{Kind: knowledge.ObjectKindChunk, ID: pkg.Manifest.Chunk.ID},
		Kind:   knowledge.LinkKindPartOf,
	}}
	if _, err := service.PreviewImport(context.Background(), pkg); !errors.Is(err, ErrChunkPolicyDenied) {
		t.Fatalf("PreviewImport() error = %v, want ErrChunkPolicyDenied", err)
	}
	if store.updates != 0 {
		t.Fatalf("PreviewImport() opened %d write transactions", store.updates)
	}
}

func TestPreviewImportOrdersPackageRecordsByID(t *testing.T) {
	t.Parallel()
	backend := memory.New()
	t.Cleanup(func() { _ = backend.Close() })
	service := newTestService(t, backend, nil)
	pkg := previewPackage("01a02b00-0000-7000-8000-000000000001", "Ordered")
	pkg.Entries = []knowledge.Entry{
		{ID: "01a02b00-0000-7000-8000-000000000003", ChunkID: knowledge.ChunkID(pkg.Manifest.Chunk.ID), Title: "Third"},
		{ID: "01a02b00-0000-7000-8000-000000000002", ChunkID: knowledge.ChunkID(pkg.Manifest.Chunk.ID), Title: "Second"},
	}
	preview, err := service.PreviewImport(context.Background(), pkg)
	if err != nil {
		t.Fatal(err)
	}
	if len(preview.Impacts) != 3 || preview.Impacts[1].ID >= preview.Impacts[2].ID {
		t.Fatalf("PreviewImport() impacts are not deterministic: %#v", preview.Impacts)
	}
}

func previewPackage(chunkID, title string) kpackage.ValidatedPackage {
	return kpackage.ValidatedPackage{
		Manifest: kpackage.Manifest{
			Format: kpackage.Format, SchemaVersion: kpackage.SchemaVersion,
			Package: kpackage.Identity{ID: "01a02b00-0000-7000-8000-000000000000", Version: "1.0.0"},
			Chunk: kpackage.ManifestChunk{
				ID: chunkID, Title: title, Kind: knowledge.ChunkKindReference,
				Scope: knowledge.Scope{Kind: knowledge.ScopeKindGlobal}, Visibility: knowledge.VisibilityPrivate,
				State: knowledge.ChunkStateActive,
			},
		},
		SignatureState: kpackage.SignatureStateUnsigned,
	}
}
