package service

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lkarlslund/koder/internal/knowledge"
	"github.com/lkarlslund/koder/internal/knowledge/kpackage"
	knowledgeStore "github.com/lkarlslund/koder/internal/knowledge/store"
	"github.com/lkarlslund/koder/internal/knowledge/store/memory"
)

func TestExportAndValidatePackageArchiveRoundTrip(t *testing.T) {
	t.Parallel()
	backend := memory.New()
	t.Cleanup(func() { _ = backend.Close() })
	service := newImportTestService(t, backend, func() time.Time { return serviceTime }, 0xf00)
	pkg := importTestPackage(t)
	activateImportTestPackage(t, service, pkg, ImportConflictPolicyUnspecified)

	var first bytes.Buffer
	result, err := service.ExportPackage(context.Background(), &first, ExportPackageRequest{ChunkID: knowledge.ChunkID(pkg.Manifest.Chunk.ID)})
	if err != nil {
		t.Fatalf("ExportPackage() error = %v", err)
	}
	if result.Filename != "portable-knowledge.kknowledge" || result.Entries != 1 || result.Links != 1 || result.Evidence != 1 || result.Assets != 1 || result.Size != int64(first.Len()) {
		t.Fatalf("ExportPackage() = %#v", result)
	}
	var second bytes.Buffer
	if _, err := service.ExportPackage(context.Background(), &second, ExportPackageRequest{ChunkID: knowledge.ChunkID(pkg.Manifest.Chunk.ID)}); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first.Bytes(), second.Bytes()) {
		t.Fatal("ExportPackage() is not deterministic for an unchanged chunk")
	}
	validated, err := service.ValidateImportArchive(context.Background(), first.Bytes())
	if err != nil {
		t.Fatalf("ValidateImportArchive() error = %v", err)
	}
	if validated.Manifest.Package.ID != pkg.Manifest.Chunk.ID || validated.Manifest.Package.Version != "0.0.1" ||
		len(validated.Entries) != 1 || len(validated.Links) != 1 || len(validated.Evidence) != 1 || len(validated.Assets) != 1 {
		t.Fatalf("validated export = %#v", validated)
	}
}

func TestLocallyCreatedChunkExportPreviewsAsUnchanged(t *testing.T) {
	t.Parallel()
	backend := memory.New()
	t.Cleanup(func() { _ = backend.Close() })
	service, err := New(Config{
		Store: backend, Actor: ContextActorSource(knowledge.Actor{Kind: knowledge.ActorKindSystem, ID: "system:test"}),
		ImportValidation: kpackage.ValidationOptions{CurrentKoderVersion: "r9999-local"},
	})
	if err != nil {
		t.Fatal(err)
	}
	created, err := service.CreateChunk(context.Background(), CreateChunkRequest{Chunk: knowledge.Chunk{
		Title: "Local package", Kind: knowledge.ChunkKindReference,
		Scope: knowledge.Scope{Kind: knowledge.ScopeKindGlobal}, Visibility: knowledge.VisibilityPrivate,
	}})
	if err != nil {
		t.Fatal(err)
	}
	var archive bytes.Buffer
	if _, err := service.ExportPackage(context.Background(), &archive, ExportPackageRequest{ChunkID: created.Chunk.ID}); err != nil {
		t.Fatal(err)
	}
	pkg, err := service.ValidateImportArchive(context.Background(), archive.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	preview, err := service.PreviewImport(context.Background(), pkg)
	if err != nil {
		t.Fatal(err)
	}
	if preview.Summary.Conflicts != 0 || preview.Summary.Blockers != 0 || preview.Summary.Unchanged != 1 || !preview.ReadyToStage {
		t.Fatalf("local export preview = %#v", preview)
	}
}

func TestExportPackageRequiresExplicitConsentForPersonalKnowledge(t *testing.T) {
	t.Parallel()
	backend := memory.New()
	t.Cleanup(func() { _ = backend.Close() })
	service, err := New(Config{
		Store: backend, Actor: ContextActorSource(knowledge.Actor{Kind: knowledge.ActorKindSystem, ID: "system:test"}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.EnsurePersonalChunk(context.Background()); err != nil {
		t.Fatal(err)
	}

	var denied bytes.Buffer
	_, err = service.ExportPackage(context.Background(), &denied, ExportPackageRequest{ChunkID: PersonalMeChunkID})
	if !errors.Is(err, ErrPersonalExportConsent) {
		t.Fatalf("ExportPackage(personal without consent) error = %v, want ErrPersonalExportConsent", err)
	}
	if denied.Len() != 0 {
		t.Fatalf("ExportPackage(personal without consent) wrote %d bytes", denied.Len())
	}

	var allowed bytes.Buffer
	result, err := service.ExportPackage(context.Background(), &allowed, ExportPackageRequest{
		ChunkID: PersonalMeChunkID, IncludePersonal: true,
	})
	if err != nil {
		t.Fatalf("ExportPackage(personal with consent) error = %v", err)
	}
	if result.Size == 0 || allowed.Len() == 0 {
		t.Fatalf("ExportPackage(personal with consent) = %#v, bytes = %d", result, allowed.Len())
	}
}

func TestExportPackageHonorsReadPolicyAndArchiveValidationIsWriteFree(t *testing.T) {
	t.Parallel()
	backend := memory.New()
	t.Cleanup(func() { _ = backend.Close() })
	counting := &updateCountingStore{Store: backend}
	service := newImportTestService(t, counting, func() time.Time { return serviceTime }, 0x1000)
	pkg := importTestPackage(t)
	activateImportTestPackage(t, service, pkg, ImportConflictPolicyUnspecified)
	var archive bytes.Buffer
	if _, err := service.ExportPackage(context.Background(), &archive, ExportPackageRequest{ChunkID: knowledge.ChunkID(pkg.Manifest.Chunk.ID)}); err != nil {
		t.Fatal(err)
	}
	counting.updates = 0
	if _, err := service.ValidateImportArchive(context.Background(), archive.Bytes()); err != nil {
		t.Fatal(err)
	}
	if counting.updates != 0 {
		t.Fatalf("ValidateImportArchive() opened %d write transactions", counting.updates)
	}
	service.chunkPolicy = denyChunkAction(ChunkPolicyRead)
	if _, err := service.ExportPackage(context.Background(), new(bytes.Buffer), ExportPackageRequest{ChunkID: knowledge.ChunkID(pkg.Manifest.Chunk.ID)}); !errors.Is(err, ErrChunkPolicyDenied) {
		t.Fatalf("ExportPackage(denied) error = %v, want ErrChunkPolicyDenied", err)
	}
}

func TestValidateImportArchiveRejectsCanceledAndInvalidInput(t *testing.T) {
	t.Parallel()
	backend := memory.New()
	t.Cleanup(func() { _ = backend.Close() })
	service, err := New(Config{
		Store: backend, Actor: ContextActorSource(knowledge.Actor{Kind: knowledge.ActorKindSystem, ID: "system:test"}),
		ImportValidation: kpackage.ValidationOptions{CurrentKoderVersion: "r9999-local-dirty"},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := service.ValidateImportArchive(ctx, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("ValidateImportArchive(canceled) error = %v", err)
	}
	if _, err := service.ValidateImportArchive(context.Background(), []byte("not a zip")); !errors.Is(err, kpackage.ErrInvalidArchive) {
		t.Fatalf("ValidateImportArchive(invalid) error = %v", err)
	}
}

func TestExportPackageIncludesRepresentableCrossChunkDependency(t *testing.T) {
	t.Parallel()
	backend := memory.New()
	t.Cleanup(func() { _ = backend.Close() })
	service := newImportTestService(t, backend, func() time.Time { return serviceTime }, 0x1100)
	first := testChunkCandidate()
	first.Title = "First export"
	createdFirst, err := service.CreateChunk(context.Background(), CreateChunkRequest{Chunk: first})
	if err != nil {
		t.Fatal(err)
	}
	second := testChunkCandidate()
	second.Title = "External dependency"
	createdSecond, err := service.CreateChunk(context.Background(), CreateChunkRequest{Chunk: second})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.CreateLink(context.Background(), CreateLinkRequest{Link: knowledge.Link{
		Source: knowledge.ObjectRef{Kind: knowledge.ObjectKindChunk, ID: string(createdFirst.Chunk.ID)},
		Target: knowledge.ObjectRef{Kind: knowledge.ObjectKindChunk, ID: string(createdSecond.Chunk.ID)},
		Kind:   knowledge.LinkKindRelatedTo,
	}}); err != nil {
		t.Fatal(err)
	}
	var archive bytes.Buffer
	if _, err := service.ExportPackage(context.Background(), &archive, ExportPackageRequest{ChunkID: createdFirst.Chunk.ID}); err != nil {
		t.Fatal(err)
	}
	validated, err := service.ValidateImportArchive(context.Background(), archive.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if len(validated.Manifest.Dependencies) != 1 || validated.Manifest.Dependencies[0].ChunkID != string(createdSecond.Chunk.ID) || len(validated.Links) != 1 {
		t.Fatalf("cross-chunk export dependencies=%#v links=%#v", validated.Manifest.Dependencies, validated.Links)
	}
	if stats, err := backend.ScanCanonical(context.Background(), func(knowledgeStore.CanonicalRecord) error { return nil }); err != nil || stats.Chunks != 2 {
		t.Fatalf("canonical stats = %#v, %v", stats, err)
	}
}
