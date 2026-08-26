package store_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/lkarlslund/koder/internal/memory"
	memoryService "github.com/lkarlslund/koder/internal/memory/service"
	memoryStoreAPI "github.com/lkarlslund/koder/internal/memory/store"
	memoryBackend "github.com/lkarlslund/koder/internal/memory/store/memory"
	"github.com/lkarlslund/koder/internal/memory/store/pebble"
)

const (
	migrationChunkID    memory.ChunkID    = "019f132e-4f3a-739a-9ab2-5198dcd19e67"
	migrationEntryID    memory.EntryID    = "01a01f76-1ff6-7c1d-967a-66ad5703dd33"
	migrationLinkID     memory.LinkID     = "01a020a6-84d5-7b03-a995-bb2cfb4528b0"
	migrationEvidenceID memory.EvidenceID = "01a01688-fc6b-7a53-a907-4f903461820e"
)

var migrationTime = time.Date(2026, 8, 22, 18, 0, 0, 0, time.UTC)

func TestMigrationSnapshotMovesExactCanonicalStateAcrossBackends(t *testing.T) {
	for _, test := range []struct {
		name   string
		source func(*testing.T) memoryStoreAPI.Store
		target func(*testing.T) memoryStoreAPI.Store
	}{
		{name: "memory to pebble", source: openMigrationMemory, target: openMigrationPebble},
		{name: "pebble to memory", source: openMigrationPebble, target: openMigrationMemory},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			source := test.source(t)
			seedMigrationStore(t, source)
			sourceMaintenance, ok := source.(memoryStoreAPI.MaintenanceStore)
			if !ok {
				t.Fatal("source does not implement MaintenanceStore")
			}
			snapshot, stats, err := memoryStoreAPI.ExportMigrationSnapshot(ctx, sourceMaintenance)
			if err != nil {
				t.Fatalf("ExportMigrationSnapshot() error = %v", err)
			}
			if stats.Chunks != 1 || stats.Entries != 1 || stats.Links != 1 || stats.Evidence != 1 || stats.Revisions != 4 || stats.Assets != 1 {
				t.Fatalf("export stats = %#v", stats)
			}

			target := test.target(t)
			imported, err := memoryStoreAPI.ImportMigrationSnapshot(ctx, target, snapshot)
			if err != nil {
				t.Fatalf("ImportMigrationSnapshot() error = %v", err)
			}
			if imported != stats {
				t.Fatalf("import stats = %#v, want %#v", imported, stats)
			}
			targetSnapshot, _, err := memoryStoreAPI.ExportMigrationSnapshot(ctx, target.(memoryStoreAPI.MaintenanceStore))
			if err != nil {
				t.Fatalf("ExportMigrationSnapshot(target) error = %v", err)
			}
			if !reflect.DeepEqual(targetSnapshot, snapshot) {
				t.Fatalf("target snapshot differs\n got: %#v\nwant: %#v", targetSnapshot, snapshot)
			}
			if _, err := memoryStoreAPI.ImportMigrationSnapshot(ctx, target, snapshot); !errors.Is(err, memoryStoreAPI.ErrConflict) {
				t.Fatalf("second import error = %v, want ErrConflict", err)
			}
		})
	}
}

func TestMemoryReplacementBackendServesMigratedPebbleMemory(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	source := openMigrationPebble(t)
	seedMigrationStore(t, source)
	snapshot, _, err := memoryStoreAPI.ExportMigrationSnapshot(ctx, source.(memoryStoreAPI.MaintenanceStore))
	if err != nil {
		t.Fatalf("ExportMigrationSnapshot() error = %v", err)
	}

	replacement := openMigrationMemory(t)
	if _, err := memoryStoreAPI.ImportMigrationSnapshot(ctx, replacement, snapshot); err != nil {
		t.Fatalf("ImportMigrationSnapshot() error = %v", err)
	}
	service, err := memoryService.New(memoryService.Config{
		Store: replacement,
		Actor: memoryService.ContextActorSource(memory.Actor{
			Kind: memory.ActorKindSystem,
			ID:   "system:replacement-contract",
		}),
		Now: func() time.Time { return migrationTime.Add(3 * time.Hour) },
	})
	if err != nil {
		t.Fatalf("New(replacement backend) error = %v", err)
	}
	result, err := service.SearchLexical(ctx, memoryService.LexicalSearchRequest{Query: "backend-neutral content"})
	if err != nil {
		t.Fatalf("SearchLexical(replacement backend) error = %v", err)
	}
	if len(result.Matches) != 1 || result.Matches[0].EntryID != migrationEntryID || result.Matches[0].Document.Title != "Portable fact" {
		t.Fatalf("SearchLexical(replacement backend) = %#v", result)
	}
}

func TestValidateMigrationSnapshotRejectsIncompleteHistoryAndReferences(t *testing.T) {
	t.Parallel()
	source := openMigrationMemory(t)
	seedMigrationStore(t, source)
	snapshot, _, err := memoryStoreAPI.ExportMigrationSnapshot(context.Background(), source.(memoryStoreAPI.MaintenanceStore))
	if err != nil {
		t.Fatal(err)
	}

	missingHistory := snapshot
	missingHistory.Revisions = append([]memoryStoreAPI.CanonicalRecord(nil), snapshot.Revisions[1:]...)
	if err := memoryStoreAPI.ValidateMigrationSnapshot(missingHistory); err == nil {
		t.Fatal("ValidateMigrationSnapshot(missing history) unexpectedly succeeded")
	}

	missingChunk := snapshot
	missingChunk.Records = append([]memoryStoreAPI.CanonicalRecord(nil), snapshot.Records[1:]...)
	if err := memoryStoreAPI.ValidateMigrationSnapshot(missingChunk); err == nil {
		t.Fatal("ValidateMigrationSnapshot(missing chunk) unexpectedly succeeded")
	}
}

func openMigrationMemory(t *testing.T) memoryStoreAPI.Store {
	t.Helper()
	store := memoryBackend.New()
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func openMigrationPebble(t *testing.T) memoryStoreAPI.Store {
	t.Helper()
	store, err := pebble.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func seedMigrationStore(t *testing.T, store memoryStoreAPI.Store) {
	t.Helper()
	ctx := context.Background()
	actor := memory.Actor{Kind: memory.ActorKindSystem, ID: "test:migration"}
	revision := func(number uint64) memory.Revision {
		return memory.Revision{
			Number: number, ID: memory.RevisionID([]string{
				"", "01a01688-fc5d-7f7d-8bb8-de2449770001", "01a01688-fc5d-7f7d-8bb8-de2449770002",
			}[number]), Actor: actor, CreatedAt: migrationTime.Add(time.Duration(number-1) * time.Minute),
		}
	}
	chunk := memory.Chunk{
		ID: migrationChunkID, Title: "Portable graph", Kind: memory.ChunkKindReference,
		Scope: memory.Scope{Kind: memory.ScopeKindGlobal}, Visibility: memory.VisibilityInstallation,
		State: memory.ChunkStateActive, SchemaVersion: 1, Revision: revision(1),
		CreatedAt: migrationTime, UpdatedAt: migrationTime,
	}
	updatedChunk := chunk
	updatedChunk.Description = "Second exact revision"
	updatedChunk.Revision = revision(2)
	updatedChunk.UpdatedAt = migrationTime.Add(time.Minute)
	evidence := memory.Evidence{
		ID: migrationEvidenceID, Type: memory.EvidenceTypeObservation, Quality: memory.EvidenceQualityPrimary,
		Source: memory.Source{ID: "observation:migration"}, Actor: actor, CreatedAt: migrationTime,
	}
	entry := memory.Entry{
		ID: migrationEntryID, ChunkID: migrationChunkID, Kind: memory.EntryKindFact,
		Title: "Portable fact", Body: "Exact backend-neutral content.", Scope: memory.Scope{Kind: memory.ScopeKindGlobal},
		EvidenceIDs:  []memory.EvidenceID{migrationEvidenceID},
		Verification: memory.Verification{Status: memory.VerificationStatusUnverified},
		State:        memory.EntryStateActive, Revision: revision(1), CreatedAt: migrationTime, UpdatedAt: migrationTime,
	}
	link := memory.Link{
		ID:     migrationLinkID,
		Source: memory.ObjectRef{Kind: memory.ObjectKindChunk, ID: string(migrationChunkID)},
		Target: memory.ObjectRef{Kind: memory.ObjectKindEntry, ID: string(migrationEntryID)},
		Kind:   memory.LinkKindRelatedTo, State: memory.LinkStateActive,
		EvidenceIDs: []memory.EvidenceID{migrationEvidenceID}, Revision: revision(1),
		CreatedAt: migrationTime, UpdatedAt: migrationTime,
	}
	data := []byte("portable asset\n")
	digest := sha256.Sum256(data)
	asset := memoryStoreAPI.PackageAsset{
		ChunkID: migrationChunkID, Path: "assets/note.txt", MediaType: "text/plain; charset=utf-8",
		SHA256: hex.EncodeToString(digest[:]), Data: data,
	}
	if err := store.Update(ctx, func(tx memoryStoreAPI.WriteTx) error {
		if err := tx.PutChunk(ctx, chunk, 0); err != nil {
			return err
		}
		if err := tx.PutChunk(ctx, updatedChunk, 1); err != nil {
			return err
		}
		if err := tx.PutEvidence(ctx, evidence); err != nil {
			return err
		}
		if err := tx.PutEntry(ctx, entry, 0); err != nil {
			return err
		}
		if err := tx.PutLink(ctx, link, 0); err != nil {
			return err
		}
		if err := tx.PutAsset(ctx, asset); err != nil {
			return err
		}
		return tx.TouchChunk(ctx, migrationChunkID, migrationTime.Add(2*time.Hour))
	}); err != nil {
		t.Fatal(err)
	}
}
