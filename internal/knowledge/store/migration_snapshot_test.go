package store_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/lkarlslund/koder/internal/knowledge"
	knowledgeService "github.com/lkarlslund/koder/internal/knowledge/service"
	knowledgeStore "github.com/lkarlslund/koder/internal/knowledge/store"
	"github.com/lkarlslund/koder/internal/knowledge/store/memory"
	"github.com/lkarlslund/koder/internal/knowledge/store/pebble"
)

const (
	migrationChunkID    knowledge.ChunkID    = "019f132e-4f3a-739a-9ab2-5198dcd19e67"
	migrationEntryID    knowledge.EntryID    = "01a01f76-1ff6-7c1d-967a-66ad5703dd33"
	migrationLinkID     knowledge.LinkID     = "01a020a6-84d5-7b03-a995-bb2cfb4528b0"
	migrationEvidenceID knowledge.EvidenceID = "01a01688-fc6b-7a53-a907-4f903461820e"
)

var migrationTime = time.Date(2026, 8, 22, 18, 0, 0, 0, time.UTC)

func TestMigrationSnapshotMovesExactCanonicalStateAcrossBackends(t *testing.T) {
	for _, test := range []struct {
		name   string
		source func(*testing.T) knowledgeStore.Store
		target func(*testing.T) knowledgeStore.Store
	}{
		{name: "memory to pebble", source: openMigrationMemory, target: openMigrationPebble},
		{name: "pebble to memory", source: openMigrationPebble, target: openMigrationMemory},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			source := test.source(t)
			seedMigrationStore(t, source)
			sourceMaintenance, ok := source.(knowledgeStore.MaintenanceStore)
			if !ok {
				t.Fatal("source does not implement MaintenanceStore")
			}
			snapshot, stats, err := knowledgeStore.ExportMigrationSnapshot(ctx, sourceMaintenance)
			if err != nil {
				t.Fatalf("ExportMigrationSnapshot() error = %v", err)
			}
			if stats.Chunks != 1 || stats.Entries != 1 || stats.Links != 1 || stats.Evidence != 1 || stats.Revisions != 4 || stats.Assets != 1 {
				t.Fatalf("export stats = %#v", stats)
			}

			target := test.target(t)
			imported, err := knowledgeStore.ImportMigrationSnapshot(ctx, target, snapshot)
			if err != nil {
				t.Fatalf("ImportMigrationSnapshot() error = %v", err)
			}
			if imported != stats {
				t.Fatalf("import stats = %#v, want %#v", imported, stats)
			}
			targetSnapshot, _, err := knowledgeStore.ExportMigrationSnapshot(ctx, target.(knowledgeStore.MaintenanceStore))
			if err != nil {
				t.Fatalf("ExportMigrationSnapshot(target) error = %v", err)
			}
			if !reflect.DeepEqual(targetSnapshot, snapshot) {
				t.Fatalf("target snapshot differs\n got: %#v\nwant: %#v", targetSnapshot, snapshot)
			}
			if _, err := knowledgeStore.ImportMigrationSnapshot(ctx, target, snapshot); !errors.Is(err, knowledgeStore.ErrConflict) {
				t.Fatalf("second import error = %v, want ErrConflict", err)
			}
		})
	}
}

func TestMemoryReplacementBackendServesMigratedPebbleKnowledge(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	source := openMigrationPebble(t)
	seedMigrationStore(t, source)
	snapshot, _, err := knowledgeStore.ExportMigrationSnapshot(ctx, source.(knowledgeStore.MaintenanceStore))
	if err != nil {
		t.Fatalf("ExportMigrationSnapshot() error = %v", err)
	}

	replacement := openMigrationMemory(t)
	if _, err := knowledgeStore.ImportMigrationSnapshot(ctx, replacement, snapshot); err != nil {
		t.Fatalf("ImportMigrationSnapshot() error = %v", err)
	}
	service, err := knowledgeService.New(knowledgeService.Config{
		Store: replacement,
		Actor: knowledgeService.ContextActorSource(knowledge.Actor{
			Kind: knowledge.ActorKindSystem,
			ID:   "system:replacement-contract",
		}),
		Now: func() time.Time { return migrationTime.Add(3 * time.Hour) },
	})
	if err != nil {
		t.Fatalf("New(replacement backend) error = %v", err)
	}
	result, err := service.SearchLexical(ctx, knowledgeService.LexicalSearchRequest{Query: "backend-neutral content"})
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
	snapshot, _, err := knowledgeStore.ExportMigrationSnapshot(context.Background(), source.(knowledgeStore.MaintenanceStore))
	if err != nil {
		t.Fatal(err)
	}

	missingHistory := snapshot
	missingHistory.Revisions = append([]knowledgeStore.CanonicalRecord(nil), snapshot.Revisions[1:]...)
	if err := knowledgeStore.ValidateMigrationSnapshot(missingHistory); err == nil {
		t.Fatal("ValidateMigrationSnapshot(missing history) unexpectedly succeeded")
	}

	missingChunk := snapshot
	missingChunk.Records = append([]knowledgeStore.CanonicalRecord(nil), snapshot.Records[1:]...)
	if err := knowledgeStore.ValidateMigrationSnapshot(missingChunk); err == nil {
		t.Fatal("ValidateMigrationSnapshot(missing chunk) unexpectedly succeeded")
	}
}

func openMigrationMemory(t *testing.T) knowledgeStore.Store {
	t.Helper()
	store := memory.New()
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func openMigrationPebble(t *testing.T) knowledgeStore.Store {
	t.Helper()
	store, err := pebble.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func seedMigrationStore(t *testing.T, store knowledgeStore.Store) {
	t.Helper()
	ctx := context.Background()
	actor := knowledge.Actor{Kind: knowledge.ActorKindSystem, ID: "test:migration"}
	revision := func(number uint64) knowledge.Revision {
		return knowledge.Revision{
			Number: number, ID: knowledge.RevisionID([]string{
				"", "01a01688-fc5d-7f7d-8bb8-de2449770001", "01a01688-fc5d-7f7d-8bb8-de2449770002",
			}[number]), Actor: actor, CreatedAt: migrationTime.Add(time.Duration(number-1) * time.Minute),
		}
	}
	chunk := knowledge.Chunk{
		ID: migrationChunkID, Title: "Portable graph", Kind: knowledge.ChunkKindReference,
		Scope: knowledge.Scope{Kind: knowledge.ScopeKindGlobal}, Visibility: knowledge.VisibilityInstallation,
		State: knowledge.ChunkStateActive, SchemaVersion: 1, Revision: revision(1),
		CreatedAt: migrationTime, UpdatedAt: migrationTime,
	}
	updatedChunk := chunk
	updatedChunk.Description = "Second exact revision"
	updatedChunk.Revision = revision(2)
	updatedChunk.UpdatedAt = migrationTime.Add(time.Minute)
	evidence := knowledge.Evidence{
		ID: migrationEvidenceID, Type: knowledge.EvidenceTypeObservation, Quality: knowledge.EvidenceQualityPrimary,
		Source: knowledge.Source{ID: "observation:migration"}, Actor: actor, CreatedAt: migrationTime,
	}
	entry := knowledge.Entry{
		ID: migrationEntryID, ChunkID: migrationChunkID, Kind: knowledge.EntryKindFact,
		Title: "Portable fact", Body: "Exact backend-neutral content.", Scope: knowledge.Scope{Kind: knowledge.ScopeKindGlobal},
		EvidenceIDs:  []knowledge.EvidenceID{migrationEvidenceID},
		Verification: knowledge.Verification{Status: knowledge.VerificationStatusUnverified},
		State:        knowledge.EntryStateActive, Revision: revision(1), CreatedAt: migrationTime, UpdatedAt: migrationTime,
	}
	link := knowledge.Link{
		ID:     migrationLinkID,
		Source: knowledge.ObjectRef{Kind: knowledge.ObjectKindChunk, ID: string(migrationChunkID)},
		Target: knowledge.ObjectRef{Kind: knowledge.ObjectKindEntry, ID: string(migrationEntryID)},
		Kind:   knowledge.LinkKindRelatedTo, State: knowledge.LinkStateActive,
		EvidenceIDs: []knowledge.EvidenceID{migrationEvidenceID}, Revision: revision(1),
		CreatedAt: migrationTime, UpdatedAt: migrationTime,
	}
	data := []byte("portable asset\n")
	digest := sha256.Sum256(data)
	asset := knowledgeStore.PackageAsset{
		ChunkID: migrationChunkID, Path: "assets/note.txt", MediaType: "text/plain; charset=utf-8",
		SHA256: hex.EncodeToString(digest[:]), Data: data,
	}
	if err := store.Update(ctx, func(tx knowledgeStore.WriteTx) error {
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
