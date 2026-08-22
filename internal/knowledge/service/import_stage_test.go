package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/lkarlslund/koder/internal/knowledge"
	"github.com/lkarlslund/koder/internal/knowledge/kpackage"
	knowledgeStore "github.com/lkarlslund/koder/internal/knowledge/store"
	"github.com/lkarlslund/koder/internal/knowledge/store/memory"
)

var errSyntheticImportWrite = errors.New("synthetic import write failure")

type failingImportStore struct {
	knowledgeStore.Store
	failAt int
}

type blockingActivationStore struct {
	knowledgeStore.Store
	entered chan struct{}
	release chan struct{}
}

func (s *blockingActivationStore) Update(ctx context.Context, fn func(knowledgeStore.WriteTx) error) error {
	close(s.entered)
	select {
	case <-s.release:
	case <-ctx.Done():
		return ctx.Err()
	}
	return s.Store.Update(ctx, fn)
}

func (s *failingImportStore) Update(ctx context.Context, fn func(knowledgeStore.WriteTx) error) error {
	return s.Store.Update(ctx, func(tx knowledgeStore.WriteTx) error {
		return fn(&failingImportTx{WriteTx: tx, failAt: s.failAt})
	})
}

type failingImportTx struct {
	knowledgeStore.WriteTx
	failAt int
	puts   int
}

func (tx *failingImportTx) beforePut() error {
	tx.puts++
	if tx.failAt > 0 && tx.puts == tx.failAt {
		return errSyntheticImportWrite
	}
	return nil
}

func (tx *failingImportTx) PutChunk(ctx context.Context, value knowledge.Chunk, expected uint64) error {
	if err := tx.beforePut(); err != nil {
		return err
	}
	return tx.WriteTx.PutChunk(ctx, value, expected)
}

func (tx *failingImportTx) PutEntry(ctx context.Context, value knowledge.Entry, expected uint64) error {
	if err := tx.beforePut(); err != nil {
		return err
	}
	return tx.WriteTx.PutEntry(ctx, value, expected)
}

func (tx *failingImportTx) PutLink(ctx context.Context, value knowledge.Link, expected uint64) error {
	if err := tx.beforePut(); err != nil {
		return err
	}
	return tx.WriteTx.PutLink(ctx, value, expected)
}

func (tx *failingImportTx) PutEvidence(ctx context.Context, value knowledge.Evidence) error {
	if err := tx.beforePut(); err != nil {
		return err
	}
	return tx.WriteTx.PutEvidence(ctx, value)
}

func (tx *failingImportTx) PutAsset(ctx context.Context, value knowledgeStore.PackageAsset) error {
	if err := tx.beforePut(); err != nil {
		return err
	}
	return tx.WriteTx.PutAsset(ctx, value)
}

func TestStageAndActivateImportAtomically(t *testing.T) {
	t.Parallel()
	pkg := importTestPackage(t)
	backend := memory.New()
	t.Cleanup(func() { _ = backend.Close() })
	store := &updateCountingStore{Store: backend}
	service := newImportTestService(t, store, func() time.Time { return serviceTime }, 0x100)

	stage, err := service.StageImport(context.Background(), StageImportRequest{Package: pkg})
	if err != nil {
		t.Fatalf("StageImport() error = %v", err)
	}
	if store.updates != 0 || stage.ID == "" || !stage.Preview.ReadyToStage || stage.Preview.Summary.Additions != 5 {
		t.Fatalf("StageImport() = %#v, updates=%d", stage, store.updates)
	}
	pkg.Entries[0].Body = "Mutated after staging\n"
	pkg.Assets["assets/note.txt"][0] = 'X'

	result, err := service.ActivateImport(context.Background(), stage.ID)
	if err != nil {
		t.Fatalf("ActivateImport() error = %v", err)
	}
	if store.updates != 1 || result.Added.Additions != 5 || result.ChunkID != knowledge.ChunkID(stage.Preview.ChunkID) {
		t.Fatalf("ActivateImport() = %#v, updates=%d", result, store.updates)
	}
	stats := canonicalStats(t, backend)
	if stats.Chunks != 1 || stats.Entries != 1 || stats.Links != 1 || stats.Evidence != 1 {
		t.Fatalf("activated stats = %#v", stats)
	}
	if err := backend.View(context.Background(), func(tx knowledgeStore.ReadTx) error {
		asset, err := tx.Asset(context.Background(), result.ChunkID, "assets/note.txt")
		if err != nil {
			return err
		}
		if string(asset.Data) != "portable asset\n" {
			t.Fatalf("activated asset = %#v", asset)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	entry, err := service.Entry(context.Background(), knowledge.EntryID(stage.Preview.Impacts[1].ID))
	if err != nil {
		t.Fatal(err)
	}
	if entry.Body == "Mutated after staging\n" || entry.Revision.Actor.Kind != knowledge.ActorKindImport || entry.Revision.Actor.ID != result.Package.ID || entry.Revision.Number != 1 {
		t.Fatalf("activated entry = %#v", entry)
	}
	if _, err := service.ActivateImport(context.Background(), stage.ID); !errors.Is(err, ErrImportStageNotFound) {
		t.Fatalf("second ActivateImport() error = %v, want ErrImportStageNotFound", err)
	}
}

func TestStageImportRequiresReviewAndRejectsPreviewBlockers(t *testing.T) {
	t.Parallel()
	pkg := importTestPackage(t)
	backend := memory.New()
	t.Cleanup(func() { _ = backend.Close() })
	service := newImportTestService(t, backend, func() time.Time { return serviceTime }, 0x200)
	pkg.Manifest.Chunk.Risk = []knowledge.RiskClass{knowledge.RiskClassMedical}
	stage, err := service.StageImport(context.Background(), StageImportRequest{Package: pkg})
	if !errors.Is(err, ErrReviewRequired) || stage.Preview.Classification.Decision != knowledge.ClassificationDecisionReview {
		t.Fatalf("StageImport(review) = %#v, %v", stage, err)
	}
	if _, err := service.StageImport(context.Background(), StageImportRequest{Package: pkg, ReviewApproved: true}); err != nil {
		t.Fatalf("StageImport(approved) error = %v", err)
	}

	conflictBackend := memory.New()
	t.Cleanup(func() { _ = conflictBackend.Close() })
	conflictService := newImportTestService(t, conflictBackend, func() time.Time { return serviceTime }, 0x300)
	conflicting := packageChunk(pkg.Manifest)
	conflicting.Title = "Different local title"
	conflicting = prepareImportedChunk(conflicting, knowledge.Actor{Kind: knowledge.ActorKindSystem, ID: "system:test"}, "01a02b00-0000-7000-8000-000000000301", "local", serviceTime)
	if err := conflictBackend.Update(context.Background(), func(tx knowledgeStore.WriteTx) error { return tx.PutChunk(context.Background(), conflicting, 0) }); err != nil {
		t.Fatal(err)
	}
	stage, err = conflictService.StageImport(context.Background(), StageImportRequest{Package: pkg, ReviewApproved: true})
	if !errors.Is(err, ErrImportBlocked) || stage.Preview.Summary.Conflicts != 1 {
		t.Fatalf("StageImport(conflict) = %#v, %v", stage, err)
	}
}

func TestActivateImportRejectsStalePreviewAssumptions(t *testing.T) {
	t.Parallel()
	pkg := importTestPackage(t)
	backend := memory.New()
	t.Cleanup(func() { _ = backend.Close() })
	service := newImportTestService(t, backend, func() time.Time { return serviceTime }, 0x400)
	stage, err := service.StageImport(context.Background(), StageImportRequest{Package: pkg})
	if err != nil {
		t.Fatal(err)
	}
	concurrent := packageChunk(pkg.Manifest)
	concurrent = prepareImportedChunk(concurrent, knowledge.Actor{Kind: knowledge.ActorKindSystem, ID: "system:test"}, "01a02b00-0000-7000-8000-000000000401", "concurrent", serviceTime)
	if err := backend.Update(context.Background(), func(tx knowledgeStore.WriteTx) error { return tx.PutChunk(context.Background(), concurrent, 0) }); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ActivateImport(context.Background(), stage.ID); !errors.Is(err, ErrImportStageStale) {
		t.Fatalf("ActivateImport() error = %v, want ErrImportStageStale", err)
	}
	if _, err := service.ActivateImport(context.Background(), stage.ID); !errors.Is(err, ErrImportStageNotFound) {
		t.Fatalf("stale stage was retained: %v", err)
	}
	stats := canonicalStats(t, backend)
	if stats.Total != 1 || stats.Chunks != 1 {
		t.Fatalf("stale activation wrote records: %#v", stats)
	}
}

func TestActivateImportRollsBackEveryWriteAndAllowsRetry(t *testing.T) {
	t.Parallel()
	pkg := importTestPackage(t)
	backend := memory.New()
	t.Cleanup(func() { _ = backend.Close() })
	store := &failingImportStore{Store: backend, failAt: 3}
	service := newImportTestService(t, store, func() time.Time { return serviceTime }, 0x500)
	stage, err := service.StageImport(context.Background(), StageImportRequest{Package: pkg})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.ActivateImport(context.Background(), stage.ID); !errors.Is(err, errSyntheticImportWrite) {
		t.Fatalf("ActivateImport(failing) error = %v", err)
	}
	if stats := canonicalStats(t, backend); stats.Total != 0 {
		t.Fatalf("failed activation left canonical records: %#v", stats)
	}
	store.failAt = 0
	if _, err := service.ActivateImport(context.Background(), stage.ID); err != nil {
		t.Fatalf("ActivateImport(retry) error = %v", err)
	}
	if stats := canonicalStats(t, backend); stats.Total != 4 {
		t.Fatalf("retried activation stats = %#v", stats)
	}
	if err := backend.View(context.Background(), func(tx knowledgeStore.ReadTx) error {
		assets, err := tx.ListAssets(context.Background(), knowledge.ChunkID(pkg.Manifest.Chunk.ID))
		if err != nil || len(assets) != 1 {
			t.Fatalf("retried activation assets = %#v, %v", assets, err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestImportStageExpiryAndDiscard(t *testing.T) {
	t.Parallel()
	pkg := importTestPackage(t)
	backend := memory.New()
	t.Cleanup(func() { _ = backend.Close() })
	now := serviceTime
	service := newImportTestServiceWithTTL(t, backend, func() time.Time { return now }, 0x600, time.Minute)
	stage, err := service.StageImport(context.Background(), StageImportRequest{Package: pkg})
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Minute)
	if _, err := service.ActivateImport(context.Background(), stage.ID); !errors.Is(err, ErrImportStageExpired) {
		t.Fatalf("ActivateImport(expired) error = %v", err)
	}
	now = serviceTime
	stage, err = service.StageImport(context.Background(), StageImportRequest{Package: pkg})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.DiscardImportStage(context.Background(), stage.ID); err != nil {
		t.Fatalf("DiscardImportStage() error = %v", err)
	}
	if _, err := service.ActivateImport(context.Background(), stage.ID); !errors.Is(err, ErrImportStageNotFound) {
		t.Fatalf("ActivateImport(discarded) error = %v", err)
	}
}

func TestImportStageAllowsOnlyOneActivationAtATime(t *testing.T) {
	t.Parallel()
	pkg := importTestPackage(t)
	backend := memory.New()
	t.Cleanup(func() { _ = backend.Close() })
	store := &blockingActivationStore{Store: backend, entered: make(chan struct{}), release: make(chan struct{})}
	service := newImportTestService(t, store, func() time.Time { return serviceTime }, 0x700)
	stage, err := service.StageImport(context.Background(), StageImportRequest{Package: pkg})
	if err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() {
		_, err := service.ActivateImport(context.Background(), stage.ID)
		result <- err
	}()
	<-store.entered
	if _, err := service.ActivateImport(context.Background(), stage.ID); !errors.Is(err, ErrImportStageBusy) {
		t.Fatalf("concurrent ActivateImport() error = %v, want ErrImportStageBusy", err)
	}
	if err := service.DiscardImportStage(context.Background(), stage.ID); !errors.Is(err, ErrImportStageBusy) {
		t.Fatalf("concurrent DiscardImportStage() error = %v, want ErrImportStageBusy", err)
	}
	close(store.release)
	if err := <-result; err != nil {
		t.Fatalf("first ActivateImport() error = %v", err)
	}
}

func importTestPackage(t *testing.T) kpackage.ValidatedPackage {
	t.Helper()
	source := memory.New()
	t.Cleanup(func() { _ = source.Close() })
	service := newImportTestService(t, source, func() time.Time { return serviceTime.Add(-time.Hour) }, 0x10)
	chunkCandidate := testChunkCandidate()
	chunkCandidate.Title = "Portable knowledge"
	createdChunk, err := service.CreateChunk(context.Background(), CreateChunkRequest{Chunk: chunkCandidate})
	if err != nil {
		t.Fatal(err)
	}
	evidence, err := service.CreateEvidence(context.Background(), CreateEvidenceRequest{Evidence: testEvidenceCandidate()})
	if err != nil {
		t.Fatal(err)
	}
	entryCandidate := testEntryCandidate()
	entryCandidate.Body = "Portable fact body\n"
	entryCandidate.EvidenceIDs = []knowledge.EvidenceID{evidence.Evidence.ID}
	entry, err := service.CreateEntry(context.Background(), CreateEntryRequest{ChunkID: createdChunk.Chunk.ID, Entry: entryCandidate})
	if err != nil {
		t.Fatal(err)
	}
	link, err := service.CreateLink(context.Background(), CreateLinkRequest{Link: knowledge.Link{
		Source: knowledge.ObjectRef{Kind: knowledge.ObjectKindEntry, ID: string(entry.Entry.ID)},
		Target: knowledge.ObjectRef{Kind: knowledge.ObjectKindChunk, ID: string(createdChunk.Chunk.ID)},
		Kind:   knowledge.LinkKindPartOf, EvidenceIDs: []knowledge.EvidenceID{evidence.Evidence.ID},
	}})
	if err != nil {
		t.Fatal(err)
	}
	assetData := []byte("portable asset\n")
	assetDigest := sha256.Sum256(assetData)
	return kpackage.ValidatedPackage{
		Manifest: kpackage.Manifest{
			Format: kpackage.Format, SchemaVersion: kpackage.SchemaVersion,
			Package:   kpackage.Identity{ID: "01a02b00-0000-7000-8000-000000000f00", Version: "1.0.0"},
			Publisher: kpackage.Publisher{ID: "publisher:test", Name: "Test publisher"}, License: kpackage.License{Name: "MIT"},
			Files: []kpackage.File{{Path: "assets/note.txt", MediaType: "text/plain; charset=utf-8", SHA256: hex.EncodeToString(assetDigest[:]), Size: int64(len(assetData))}},
			Chunk: kpackage.ManifestChunk{
				ID: string(createdChunk.Chunk.ID), Title: createdChunk.Chunk.Title, Kind: createdChunk.Chunk.Kind,
				Scope: createdChunk.Chunk.Scope, Visibility: createdChunk.Chunk.Visibility, State: createdChunk.Chunk.State,
			},
		},
		Entries: []knowledge.Entry{entry.Entry}, Links: []knowledge.Link{link.Link}, Evidence: []knowledge.Evidence{evidence.Evidence},
		Assets: map[string][]byte{"assets/note.txt": assetData}, SignatureState: kpackage.SignatureStateUnsigned,
	}
}

func newImportTestService(t *testing.T, store knowledgeStore.Store, now func() time.Time, firstID int) *Service {
	t.Helper()
	return newImportTestServiceWithTTL(t, store, now, firstID, 15*time.Minute)
}

func newImportTestServiceWithTTL(t *testing.T, store knowledgeStore.Store, now func() time.Time, firstID int, ttl time.Duration) *Service {
	t.Helper()
	next := firstID
	service, err := New(Config{
		Store: store, Now: now, ImportStageTTL: ttl,
		Actor: func(context.Context) (knowledge.Actor, error) {
			return knowledge.Actor{Kind: knowledge.ActorKindUser, ID: "user:import-test"}, nil
		},
		NewID: func() string {
			value := fmt.Sprintf("01a02b00-0000-7000-8000-%012x", next)
			next++
			return value
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func canonicalStats(t *testing.T, store *memory.Store) knowledgeStore.ScanStats {
	t.Helper()
	stats, err := store.ScanCanonical(context.Background(), func(knowledgeStore.CanonicalRecord) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	return stats
}
