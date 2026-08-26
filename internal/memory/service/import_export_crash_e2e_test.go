package service

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/lkarlslund/koder/internal/memory"
	"github.com/lkarlslund/koder/internal/memory/kpackage"
	memoryStoreAPI "github.com/lkarlslund/koder/internal/memory/store"
	memoryPebble "github.com/lkarlslund/koder/internal/memory/store/pebble"
)

func TestExportImportPebbleRoundTripSurvivesFailedActivationCrash(t *testing.T) {
	ctx := context.Background()
	sourceStore, err := memoryPebble.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sourceService := newImportTestService(t, sourceStore, func() time.Time { return serviceTime }, 0x2000)
	fixture := importTestPackage(t)
	activateImportTestPackage(t, sourceService, fixture, ImportConflictPolicyUnspecified)
	var archive bytes.Buffer
	if _, err := sourceService.ExportPackage(ctx, &archive, ExportPackageRequest{
		ChunkID: memory.ChunkID(fixture.Manifest.Chunk.ID),
	}); err != nil {
		t.Fatalf("export source package: %v", err)
	}
	exported, err := sourceService.ValidateImportArchive(ctx, archive.Bytes())
	if err != nil {
		t.Fatalf("validate source export: %v", err)
	}
	if err := sourceStore.Close(); err != nil {
		t.Fatalf("close source Pebble: %v", err)
	}

	destinationPath := t.TempDir()
	destinationStore, err := memoryPebble.Open(destinationPath)
	if err != nil {
		t.Fatal(err)
	}
	failingStore := &failingImportStore{Store: destinationStore, failAt: 3}
	failingService := newImportTestService(t, failingStore, func() time.Time { return serviceTime.Add(time.Hour) }, 0x2100)
	validated, err := failingService.ValidateImportArchive(ctx, archive.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	stage, err := failingService.StageImport(ctx, StageImportRequest{Package: validated, ReviewApproved: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := failingService.ActivateImport(ctx, stage.ID); !errors.Is(err, errSyntheticImportWrite) {
		t.Fatalf("ActivateImport(injected failure) error = %v", err)
	}
	// Simulate the server disappearing immediately after the failed transaction.
	// The in-memory stage is intentionally lost and only durable Pebble state can
	// participate in recovery.
	if err := destinationStore.Close(); err != nil {
		t.Fatalf("close destination after failed activation: %v", err)
	}

	reopened, err := memoryPebble.Open(destinationPath)
	if err != nil {
		t.Fatalf("reopen destination after simulated crash: %v", err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	recoveredService := newImportTestService(t, reopened, func() time.Time { return serviceTime.Add(2 * time.Hour) }, 0x2200)
	stats, err := reopened.ScanCanonical(ctx, func(memoryStoreAPI.CanonicalRecord) error { return nil })
	if err != nil || stats.Total != 0 {
		t.Fatalf("failed activation survived restart: stats=%#v error=%v", stats, err)
	}
	if err := reopened.View(ctx, func(tx memoryStoreAPI.ReadTx) error {
		assets, err := tx.ListAssets(ctx, memory.ChunkID(exported.Manifest.Chunk.ID))
		if err != nil {
			return err
		}
		if len(assets) != 0 {
			t.Fatalf("failed activation left assets after restart: %#v", assets)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	emptySearch, err := recoveredService.SearchLexical(ctx, LexicalSearchRequest{Query: "Portable fact", Limit: 5})
	if err != nil || len(emptySearch.Matches) != 0 || emptySearch.CorpusDocumentCount != 0 {
		t.Fatalf("failed activation left search index state: %#v, %v", emptySearch, err)
	}
	if _, err := recoveredService.ActivateImport(ctx, stage.ID); !errors.Is(err, ErrImportStageNotFound) {
		t.Fatalf("pre-crash stage survived process memory: %v", err)
	}

	validated, err = recoveredService.ValidateImportArchive(ctx, archive.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	restaged, err := recoveredService.StageImport(ctx, StageImportRequest{Package: validated, ReviewApproved: true})
	if err != nil {
		t.Fatal(err)
	}
	activated, err := recoveredService.ActivateImport(ctx, restaged.ID)
	if err != nil || activated.Added.Additions != 5 {
		t.Fatalf("ActivateImport(after restage) = %#v, %v", activated, err)
	}
	stats, err = reopened.ScanCanonical(ctx, func(memoryStoreAPI.CanonicalRecord) error { return nil })
	if err != nil || stats.Chunks != 1 || stats.Entries != 1 || stats.Links != 1 || stats.Evidence != 1 {
		t.Fatalf("recovered import stats = %#v, %v", stats, err)
	}

	var roundTrip bytes.Buffer
	if _, err := recoveredService.ExportPackage(ctx, &roundTrip, ExportPackageRequest{ChunkID: activated.ChunkID}); err != nil {
		t.Fatal(err)
	}
	reexported, err := recoveredService.ValidateImportArchive(ctx, roundTrip.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	assertPortablePackageContentEqual(t, exported, reexported)
}

func assertPortablePackageContentEqual(t *testing.T, before, after kpackage.ValidatedPackage) {
	t.Helper()
	if before.Manifest.Package != after.Manifest.Package || before.Manifest.Publisher != after.Manifest.Publisher ||
		before.Manifest.License != after.Manifest.License || !reflect.DeepEqual(before.Manifest.Chunk, after.Manifest.Chunk) {
		t.Fatalf("round-trip package metadata differs:\nbefore=%#v\nafter=%#v", before.Manifest, after.Manifest)
	}
	if len(before.Entries) != len(after.Entries) || len(before.Links) != len(after.Links) ||
		len(before.Evidence) != len(after.Evidence) || len(before.Assets) != len(after.Assets) {
		t.Fatalf("round-trip package counts differ: before=%d/%d/%d/%d after=%d/%d/%d/%d",
			len(before.Entries), len(before.Links), len(before.Evidence), len(before.Assets),
			len(after.Entries), len(after.Links), len(after.Evidence), len(after.Assets))
	}
	for index := range before.Entries {
		if !packageEntryContentEqual(before.Entries[index], after.Entries[index]) {
			t.Fatalf("round-trip entry differs:\nbefore=%#v\nafter=%#v", before.Entries[index], after.Entries[index])
		}
	}
	for index := range before.Links {
		if !packageLinkContentEqual(before.Links[index], after.Links[index]) {
			t.Fatalf("round-trip link differs:\nbefore=%#v\nafter=%#v", before.Links[index], after.Links[index])
		}
	}
	for index := range before.Evidence {
		if !packageEvidenceContentEqual(before.Evidence[index], after.Evidence[index]) {
			t.Fatalf("round-trip evidence differs:\nbefore=%#v\nafter=%#v", before.Evidence[index], after.Evidence[index])
		}
	}
	for path, data := range before.Assets {
		if !bytes.Equal(data, after.Assets[path]) {
			t.Fatalf("round-trip asset %q differs", path)
		}
	}
}
