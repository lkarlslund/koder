package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/lkarlslund/koder/internal/memory"
	"github.com/lkarlslund/koder/internal/memory/kpackage"
	memoryStoreAPI "github.com/lkarlslund/koder/internal/memory/store"
	memoryBackend "github.com/lkarlslund/koder/internal/memory/store/memory"
)

var errSyntheticImportWrite = errors.New("synthetic import write failure")

type failingImportStore struct {
	memoryStoreAPI.Store
	failAt int
}

type blockingActivationStore struct {
	memoryStoreAPI.Store
	entered chan struct{}
	release chan struct{}
}

func (s *blockingActivationStore) Update(ctx context.Context, fn func(memoryStoreAPI.WriteTx) error) error {
	close(s.entered)
	select {
	case <-s.release:
	case <-ctx.Done():
		return ctx.Err()
	}
	return s.Store.Update(ctx, fn)
}

func (s *failingImportStore) Update(ctx context.Context, fn func(memoryStoreAPI.WriteTx) error) error {
	return s.Store.Update(ctx, func(tx memoryStoreAPI.WriteTx) error {
		return fn(&failingImportTx{WriteTx: tx, failAt: s.failAt})
	})
}

type failingImportTx struct {
	memoryStoreAPI.WriteTx
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

func (tx *failingImportTx) PutChunk(ctx context.Context, value memory.Chunk, expected uint64) error {
	if err := tx.beforePut(); err != nil {
		return err
	}
	return tx.WriteTx.PutChunk(ctx, value, expected)
}

func (tx *failingImportTx) PutEntry(ctx context.Context, value memory.Entry, expected uint64) error {
	if err := tx.beforePut(); err != nil {
		return err
	}
	return tx.WriteTx.PutEntry(ctx, value, expected)
}

func (tx *failingImportTx) PutLink(ctx context.Context, value memory.Link, expected uint64) error {
	if err := tx.beforePut(); err != nil {
		return err
	}
	return tx.WriteTx.PutLink(ctx, value, expected)
}

func (tx *failingImportTx) PutEvidence(ctx context.Context, value memory.Evidence) error {
	if err := tx.beforePut(); err != nil {
		return err
	}
	return tx.WriteTx.PutEvidence(ctx, value)
}

func (tx *failingImportTx) PutAsset(ctx context.Context, value memoryStoreAPI.PackageAsset) error {
	if err := tx.beforePut(); err != nil {
		return err
	}
	return tx.WriteTx.PutAsset(ctx, value)
}

func TestStageAndActivateImportAtomically(t *testing.T) {
	t.Parallel()
	pkg := importTestPackage(t)
	backend := memoryBackend.New()
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
	if store.updates != 1 || result.Added.Additions != 5 || result.ChunkID != memory.ChunkID(stage.Preview.ChunkID) {
		t.Fatalf("ActivateImport() = %#v, updates=%d", result, store.updates)
	}
	stats := canonicalStats(t, backend)
	if stats.Chunks != 1 || stats.Entries != 1 || stats.Links != 1 || stats.Evidence != 1 {
		t.Fatalf("activated stats = %#v", stats)
	}
	if err := backend.View(context.Background(), func(tx memoryStoreAPI.ReadTx) error {
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
	entry, err := service.Entry(context.Background(), memory.EntryID(stage.Preview.Impacts[1].ID))
	if err != nil {
		t.Fatal(err)
	}
	if entry.Body == "Mutated after staging\n" || entry.Revision.Actor.Kind != memory.ActorKindImport || entry.Revision.Actor.ID != result.Package.ID || entry.Revision.Number != 1 {
		t.Fatalf("activated entry = %#v", entry)
	}
	if _, err := service.ActivateImport(context.Background(), stage.ID); !errors.Is(err, ErrImportStageNotFound) {
		t.Fatalf("second ActivateImport() error = %v, want ErrImportStageNotFound", err)
	}
}

func TestStageImportRequiresReviewAndRejectsPreviewBlockers(t *testing.T) {
	t.Parallel()
	pkg := importTestPackage(t)
	backend := memoryBackend.New()
	t.Cleanup(func() { _ = backend.Close() })
	service := newImportTestService(t, backend, func() time.Time { return serviceTime }, 0x200)
	pkg.Manifest.Chunk.Risk = []memory.RiskClass{memory.RiskClassMedical}
	pkg.Manifest.Chunk.Locale = "en-DK"
	pkg.Manifest.Chunk.Domain = "medicine"
	pkg.Manifest.Chunk.SourcePolicy = "Require current authoritative medical sources."
	pkg.Manifest.Chunk.ReviewAfter = serviceTime.AddDate(0, 1, 0)
	stage, err := service.StageImport(context.Background(), StageImportRequest{Package: pkg})
	if !errors.Is(err, ErrReviewRequired) || stage.Preview.Classification.Decision != memory.ClassificationDecisionReview {
		t.Fatalf("StageImport(review) = %#v, %v", stage, err)
	}
	if _, err := service.StageImport(context.Background(), StageImportRequest{Package: pkg, ReviewApproved: true}); err != nil {
		t.Fatalf("StageImport(approved) error = %v", err)
	}

	conflictBackend := memoryBackend.New()
	t.Cleanup(func() { _ = conflictBackend.Close() })
	conflictService := newImportTestService(t, conflictBackend, func() time.Time { return serviceTime }, 0x300)
	conflicting := packageChunk(pkg.Manifest)
	conflicting.Title = "Different local title"
	conflicting = prepareImportedChunk(conflicting, memory.Actor{Kind: memory.ActorKindSystem, ID: "system:test"}, "01a02b00-0000-7000-8000-000000000301", "local", serviceTime)
	if err := conflictBackend.Update(context.Background(), func(tx memoryStoreAPI.WriteTx) error { return tx.PutChunk(context.Background(), conflicting, 0) }); err != nil {
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
	backend := memoryBackend.New()
	t.Cleanup(func() { _ = backend.Close() })
	service := newImportTestService(t, backend, func() time.Time { return serviceTime }, 0x400)
	stage, err := service.StageImport(context.Background(), StageImportRequest{Package: pkg})
	if err != nil {
		t.Fatal(err)
	}
	concurrent := packageChunk(pkg.Manifest)
	concurrent = prepareImportedChunk(concurrent, memory.Actor{Kind: memory.ActorKindSystem, ID: "system:test"}, "01a02b00-0000-7000-8000-000000000401", "concurrent", serviceTime)
	if err := backend.Update(context.Background(), func(tx memoryStoreAPI.WriteTx) error { return tx.PutChunk(context.Background(), concurrent, 0) }); err != nil {
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
	backend := memoryBackend.New()
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
	if err := backend.View(context.Background(), func(tx memoryStoreAPI.ReadTx) error {
		assets, err := tx.ListAssets(context.Background(), memory.ChunkID(pkg.Manifest.Chunk.ID))
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
	backend := memoryBackend.New()
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
	backend := memoryBackend.New()
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

func TestImportConflictPolicies(t *testing.T) {
	for _, policy := range []ImportConflictPolicy{ImportConflictPolicyMerge, ImportConflictPolicyReplace, ImportConflictPolicyKeepBoth} {
		policy := policy
		t.Run(string(policy), func(t *testing.T) {
			t.Parallel()
			pkg := importTestPackage(t)
			backend := memoryBackend.New()
			t.Cleanup(func() { _ = backend.Close() })
			service := newImportTestService(t, backend, func() time.Time { return serviceTime }, 0x800)
			activateImportTestPackage(t, service, pkg, ImportConflictPolicyUnspecified)

			// Import preparation owns local revision metadata, so a byte-identical
			// package must still be recognized as semantically unchanged.
			unchanged, err := service.PreviewImport(context.Background(), pkg)
			if err != nil || unchanged.Summary.Unchanged != 5 || unchanged.Summary.Conflicts != 0 {
				t.Fatalf("PreviewImport(reimport) = %#v, %v", unchanged, err)
			}

			incoming := conflictingImportTestPackage(pkg)
			stage, err := service.StageImport(context.Background(), StageImportRequest{Package: incoming, ConflictPolicy: policy})
			if err != nil {
				t.Fatalf("StageImport(%s) error = %v", policy, err)
			}
			if !stage.Preview.ReadyToStage || stage.Preview.Summary.Blockers != 0 {
				t.Fatalf("StageImport(%s) preview = %#v", policy, stage.Preview)
			}
			result, err := service.ActivateImport(context.Background(), stage.ID)
			if err != nil {
				t.Fatalf("ActivateImport(%s) error = %v", policy, err)
			}
			if result.ConflictPolicy != policy {
				t.Fatalf("ActivateImport(%s) policy = %q", policy, result.ConflictPolicy)
			}

			switch policy {
			case ImportConflictPolicyMerge:
				if result.KeptLocal != 5 || result.Replaced != 0 || result.Added.Additions != 0 {
					t.Fatalf("merge result = %#v", result)
				}
				assertImportedPackageValues(t, backend, pkg, memory.ChunkID(pkg.Manifest.Chunk.ID))
			case ImportConflictPolicyReplace:
				if result.Replaced != 5 || result.KeptLocal != 0 || result.Added.Additions != 0 {
					t.Fatalf("replace result = %#v", result)
				}
				assertImportedPackageValues(t, backend, incoming, memory.ChunkID(incoming.Manifest.Chunk.ID))
				chunk, err := service.Chunk(context.Background(), memory.ChunkID(incoming.Manifest.Chunk.ID))
				if err != nil || chunk.Revision.Number != 2 {
					t.Fatalf("replaced chunk = %#v, %v", chunk, err)
				}
			case ImportConflictPolicyKeepBoth:
				if result.Replaced != 0 || result.KeptLocal != 0 || result.Added.Additions != 4 || len(result.Remapped) != 5 {
					t.Fatalf("keep-both result = %#v", result)
				}
				if result.ChunkID == memory.ChunkID(pkg.Manifest.Chunk.ID) {
					t.Fatalf("keep-both retained conflicting chunk ID %s", result.ChunkID)
				}
				assertKeepBothImportReferences(t, backend, result)
				stats := canonicalStats(t, backend)
				if stats.Chunks != 2 || stats.Entries != 2 || stats.Links != 2 || stats.Evidence != 1 {
					t.Fatalf("keep-both stats = %#v", stats)
				}
			}
		})
	}
}

func TestImportConflictPolicyRejectsConcurrentConflictChange(t *testing.T) {
	t.Parallel()
	pkg := importTestPackage(t)
	backend := memoryBackend.New()
	t.Cleanup(func() { _ = backend.Close() })
	service := newImportTestService(t, backend, func() time.Time { return serviceTime }, 0x900)
	activateImportTestPackage(t, service, pkg, ImportConflictPolicyUnspecified)
	incoming := conflictingImportTestPackage(pkg)
	stage, err := service.StageImport(context.Background(), StageImportRequest{Package: incoming, ConflictPolicy: ImportConflictPolicyMerge})
	if err != nil {
		t.Fatal(err)
	}
	id := memory.EntryID(pkg.Entries[0].ID)
	if err := backend.Update(context.Background(), func(tx memoryStoreAPI.WriteTx) error {
		entry, err := tx.Entry(context.Background(), id)
		if err != nil {
			return err
		}
		entry.Body = "Concurrent local edit\n"
		entry.Revision.Number++
		entry.Revision.ID = "01a02b00-0000-7000-8000-000000000999"
		entry.Revision.CreatedAt = serviceTime.Add(time.Minute)
		entry.UpdatedAt = serviceTime.Add(time.Minute)
		return tx.PutEntry(context.Background(), entry, entry.Revision.Number-1)
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ActivateImport(context.Background(), stage.ID); !errors.Is(err, ErrImportStageStale) {
		t.Fatalf("ActivateImport(concurrent conflict) error = %v, want ErrImportStageStale", err)
	}
}

func TestKeepBothReusesStoreLevelSemanticIdentity(t *testing.T) {
	t.Parallel()
	pkg := importTestPackage(t)
	backend := memoryBackend.New()
	t.Cleanup(func() { _ = backend.Close() })
	service := newImportTestService(t, backend, func() time.Time { return serviceTime }, 0xb00)
	activateImportTestPackage(t, service, pkg, ImportConflictPolicyUnspecified)

	incoming := pkg.Clone()
	oldLinkID := string(incoming.Links[0].ID)
	incoming.Links[0].ID = "01a02b00-0000-7000-8000-000000000b10"
	stage, err := service.StageImport(context.Background(), StageImportRequest{Package: incoming, ConflictPolicy: ImportConflictPolicyKeepBoth})
	if err != nil {
		t.Fatal(err)
	}
	if len(stage.Remapped) != 1 || stage.Remapped[0].Kind != memoryStoreAPI.RecordKindLink || !stage.Remapped[0].Reused ||
		stage.Remapped[0].FromID != string(incoming.Links[0].ID) || stage.Remapped[0].ToID != oldLinkID {
		t.Fatalf("semantic link remaps = %#v", stage.Remapped)
	}
	result, err := service.ActivateImport(context.Background(), stage.ID)
	if err != nil || result.Added.Additions != 0 || result.Replaced != 0 {
		t.Fatalf("ActivateImport(semantic reuse) = %#v, %v", result, err)
	}
	if stats := canonicalStats(t, backend); stats.Links != 1 {
		t.Fatalf("semantic reuse stats = %#v", stats)
	}
}

func TestConflictPolicyDoesNotResolveRequiredDependency(t *testing.T) {
	t.Parallel()
	pkg := importTestPackage(t)
	pkg.Manifest.Dependencies = []kpackage.Dependency{{
		PackageID: "01a02b00-0000-7000-8000-000000000eee", ChunkID: "01a02b00-0000-7000-8000-000000000eed",
		Version: "1.0.0", Title: "Required missing chunk", Required: true,
	}}
	backend := memoryBackend.New()
	t.Cleanup(func() { _ = backend.Close() })
	service := newImportTestService(t, backend, func() time.Time { return serviceTime }, 0xa00)
	stage, err := service.StageImport(context.Background(), StageImportRequest{Package: pkg, ConflictPolicy: ImportConflictPolicyReplace})
	if !errors.Is(err, ErrImportBlocked) || stage.Preview.Summary.MissingDependencies != 1 || stage.Preview.Summary.Blockers != 1 {
		t.Fatalf("StageImport(required dependency) = %#v, %v", stage, err)
	}
}

func TestConflictPolicyDoesNotOverwriteProtectedLifecycle(t *testing.T) {
	t.Parallel()
	pkg := importTestPackage(t)
	backend := memoryBackend.New()
	t.Cleanup(func() { _ = backend.Close() })
	service := newImportTestService(t, backend, func() time.Time { return serviceTime }, 0xc00)
	activateImportTestPackage(t, service, pkg, ImportConflictPolicyUnspecified)
	if err := backend.Update(context.Background(), func(tx memoryStoreAPI.WriteTx) error {
		entry, err := tx.Entry(context.Background(), pkg.Entries[0].ID)
		if err != nil {
			return err
		}
		entry.State = memory.EntryStateArchived
		entry.Revision.Number++
		entry.Revision.ID = "01a02b00-0000-7000-8000-000000000c10"
		entry.Revision.CreatedAt = serviceTime.Add(time.Minute)
		entry.UpdatedAt = serviceTime.Add(time.Minute)
		return tx.PutEntry(context.Background(), entry, entry.Revision.Number-1)
	}); err != nil {
		t.Fatal(err)
	}
	for _, policy := range []ImportConflictPolicy{ImportConflictPolicyReplace, ImportConflictPolicyMerge} {
		stage, err := service.StageImport(context.Background(), StageImportRequest{Package: pkg, ConflictPolicy: policy})
		if !errors.Is(err, ErrImportBlocked) || stage.Preview.Summary.Blockers != 1 {
			t.Fatalf("StageImport(%s protected lifecycle) = %#v, %v", policy, stage, err)
		}
		if impact, ok := expectedImportImpact(stage.Preview, memoryStoreAPI.RecordKindEntry, string(pkg.Entries[0].ID)); !ok || impact.ConflictResolvable || impact.Reason != "existing_entry_lifecycle_protected" {
			t.Fatalf("protected lifecycle impact = %#v", impact)
		}
	}
}

func TestStageImportRejectsUnknownConflictPolicy(t *testing.T) {
	t.Parallel()
	backend := memoryBackend.New()
	t.Cleanup(func() { _ = backend.Close() })
	service := newImportTestService(t, backend, func() time.Time { return serviceTime }, 0xd00)
	if _, err := service.StageImport(context.Background(), StageImportRequest{
		Package: importTestPackage(t), ConflictPolicy: ImportConflictPolicy("surprise_me"),
	}); !errors.Is(err, ErrImportConflictPolicy) {
		t.Fatalf("StageImport(unknown policy) error = %v, want ErrImportConflictPolicy", err)
	}
}

func TestImportConflictPolicyUsesPolicySpecificAuthority(t *testing.T) {
	t.Parallel()
	pkg := importTestPackage(t)
	backend := memoryBackend.New()
	t.Cleanup(func() { _ = backend.Close() })
	service := newImportTestService(t, backend, func() time.Time { return serviceTime }, 0xe00)
	activateImportTestPackage(t, service, pkg, ImportConflictPolicyUnspecified)
	service.chunkPolicy = denyChunkAction(ChunkPolicyUpdate)
	incoming := conflictingImportTestPackage(pkg)

	merge, err := service.StageImport(context.Background(), StageImportRequest{Package: incoming, ConflictPolicy: ImportConflictPolicyMerge})
	if err != nil {
		t.Fatalf("StageImport(merge with update denied) error = %v", err)
	}
	if _, err := service.ActivateImport(context.Background(), merge.ID); err != nil {
		t.Fatalf("ActivateImport(merge with update denied) error = %v", err)
	}

	replace, err := service.StageImport(context.Background(), StageImportRequest{Package: incoming, ConflictPolicy: ImportConflictPolicyReplace})
	if err != nil {
		t.Fatalf("StageImport(replace preview with update denied) error = %v", err)
	}
	if _, err := service.ActivateImport(context.Background(), replace.ID); !errors.Is(err, ErrChunkPolicyDenied) {
		t.Fatalf("ActivateImport(replace with update denied) error = %v, want ErrChunkPolicyDenied", err)
	}
}

func activateImportTestPackage(t *testing.T, service *Service, pkg kpackage.ValidatedPackage, policy ImportConflictPolicy) ActivateImportResult {
	t.Helper()
	stage, err := service.StageImport(context.Background(), StageImportRequest{Package: pkg, ConflictPolicy: policy})
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.ActivateImport(context.Background(), stage.ID)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func conflictingImportTestPackage(pkg kpackage.ValidatedPackage) kpackage.ValidatedPackage {
	result := pkg.Clone()
	result.Manifest.Chunk.Title = "Incoming replacement chunk"
	result.Entries[0].Body = "Incoming replacement entry\n"
	result.Links[0].Label = "Incoming replacement relationship"
	result.Evidence[0].Quality = memory.EvidenceQualitySecondary
	data := []byte("incoming replacement asset\n")
	digest := sha256.Sum256(data)
	result.Assets["assets/note.txt"] = data
	for index := range result.Manifest.Files {
		if result.Manifest.Files[index].Path == "assets/note.txt" {
			result.Manifest.Files[index].SHA256 = hex.EncodeToString(digest[:])
			result.Manifest.Files[index].Size = int64(len(data))
		}
	}
	return result
}

func assertImportedPackageValues(t *testing.T, backend *memoryBackend.Store, expected kpackage.ValidatedPackage, chunkID memory.ChunkID) {
	t.Helper()
	if err := backend.View(context.Background(), func(tx memoryStoreAPI.ReadTx) error {
		chunk, err := tx.Chunk(context.Background(), chunkID)
		if err != nil {
			return err
		}
		entry, err := tx.Entry(context.Background(), expected.Entries[0].ID)
		if err != nil {
			return err
		}
		link, err := tx.Link(context.Background(), expected.Links[0].ID)
		if err != nil {
			return err
		}
		evidence, err := tx.Evidence(context.Background(), expected.Evidence[0].ID)
		if err != nil {
			return err
		}
		asset, err := tx.Asset(context.Background(), chunkID, "assets/note.txt")
		if err != nil {
			return err
		}
		if chunk.Title != expected.Manifest.Chunk.Title || entry.Body != expected.Entries[0].Body ||
			link.Label != expected.Links[0].Label || evidence.Quality != expected.Evidence[0].Quality ||
			string(asset.Data) != string(expected.Assets["assets/note.txt"]) {
			t.Fatalf("stored package differs: chunk=%q entry=%q link=%q evidence=%q asset=%q", chunk.Title, entry.Body, link.Label, evidence.Quality, asset.Data)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func assertKeepBothImportReferences(t *testing.T, backend *memoryBackend.Store, result ActivateImportResult) {
	t.Helper()
	remapped := make(map[memoryStoreAPI.RecordKind]string)
	for _, item := range result.Remapped {
		remapped[item.Kind] = item.ToID
	}
	if err := backend.View(context.Background(), func(tx memoryStoreAPI.ReadTx) error {
		entry, err := tx.Entry(context.Background(), memory.EntryID(remapped[memoryStoreAPI.RecordKindEntry]))
		if err != nil {
			return err
		}
		link, err := tx.Link(context.Background(), memory.LinkID(remapped[memoryStoreAPI.RecordKindLink]))
		if err != nil {
			return err
		}
		if entry.ChunkID != result.ChunkID || len(entry.EvidenceIDs) != 1 || string(entry.EvidenceIDs[0]) != remapped[memoryStoreAPI.RecordKindEvidence] {
			t.Fatalf("remapped entry = %#v", entry)
		}
		if link.Source.ID != string(entry.ID) || link.Target.ID != string(result.ChunkID) || len(link.EvidenceIDs) != 1 || string(link.EvidenceIDs[0]) != remapped[memoryStoreAPI.RecordKindEvidence] {
			t.Fatalf("remapped link = %#v", link)
		}
		assets, err := tx.ListAssets(context.Background(), result.ChunkID)
		if err != nil || len(assets) != 1 || assets[0].Path != remapped[importImpactAssetKind] {
			t.Fatalf("remapped assets = %#v, %v", assets, err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func importTestPackage(t *testing.T) kpackage.ValidatedPackage {
	t.Helper()
	source := memoryBackend.New()
	t.Cleanup(func() { _ = source.Close() })
	service := newImportTestService(t, source, func() time.Time { return serviceTime.Add(-time.Hour) }, 0x10)
	chunkCandidate := testChunkCandidate()
	chunkCandidate.Title = "Portable memory"
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
	entryCandidate.EvidenceIDs = []memory.EvidenceID{evidence.Evidence.ID}
	entry, err := service.CreateEntry(context.Background(), CreateEntryRequest{ChunkID: createdChunk.Chunk.ID, Entry: entryCandidate})
	if err != nil {
		t.Fatal(err)
	}
	link, err := service.CreateLink(context.Background(), CreateLinkRequest{Link: memory.Link{
		Source: memory.ObjectRef{Kind: memory.ObjectKindEntry, ID: string(entry.Entry.ID)},
		Target: memory.ObjectRef{Kind: memory.ObjectKindChunk, ID: string(createdChunk.Chunk.ID)},
		Kind:   memory.LinkKindPartOf, EvidenceIDs: []memory.EvidenceID{evidence.Evidence.ID},
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
		Entries: []memory.Entry{entry.Entry}, Links: []memory.Link{link.Link}, Evidence: []memory.Evidence{evidence.Evidence},
		Assets: map[string][]byte{"assets/note.txt": assetData}, SignatureState: kpackage.SignatureStateUnsigned,
	}
}

func newImportTestService(t *testing.T, store memoryStoreAPI.Store, now func() time.Time, firstID int) *Service {
	t.Helper()
	return newImportTestServiceWithTTL(t, store, now, firstID, 15*time.Minute)
}

func newImportTestServiceWithTTL(t *testing.T, store memoryStoreAPI.Store, now func() time.Time, firstID int, ttl time.Duration) *Service {
	t.Helper()
	next := firstID
	service, err := New(Config{
		Store: store, Now: now, ImportStageTTL: ttl,
		Actor: func(context.Context) (memory.Actor, error) {
			return memory.Actor{Kind: memory.ActorKindUser, ID: "user:import-test"}, nil
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

func canonicalStats(t *testing.T, store *memoryBackend.Store) memoryStoreAPI.ScanStats {
	t.Helper()
	stats, err := store.ScanCanonical(context.Background(), func(memoryStoreAPI.CanonicalRecord) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	return stats
}
