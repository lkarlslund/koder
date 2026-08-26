package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lkarlslund/koder/internal/memory"
	"github.com/lkarlslund/koder/internal/memory/migrationarchive"
	memoryService "github.com/lkarlslund/koder/internal/memory/service"
	memoryStoreAPI "github.com/lkarlslund/koder/internal/memory/store"
	memoryPebble "github.com/lkarlslund/koder/internal/memory/store/pebble"
)

func TestCreateMemoryBackupCreatesValidatedIndependentCheckpoint(t *testing.T) {
	t.Parallel()
	stateDir := t.TempDir()
	store, err := memoryPebble.Open(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(t.TempDir(), "memory-backup")
	info, resolved, err := createMemoryBackup(context.Background(), stateDir, destination)
	if err != nil {
		t.Fatal(err)
	}
	if resolved != destination || info.Backend != "pebble" || info.SchemaVersion == 0 || info.IndexGeneration == 0 {
		t.Fatalf("backup = path %q info %#v", resolved, info)
	}
	if _, err := memoryPebble.ValidateCheckpoint(context.Background(), destination); err != nil {
		t.Fatalf("ValidateCheckpoint() error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(destination, "CURRENT")); err != nil {
		t.Fatalf("checkpoint CURRENT: %v", err)
	}
}

func TestWriteMemoryBackupResultSupportsMachineReadableOutput(t *testing.T) {
	t.Parallel()
	var output bytes.Buffer
	info := memoryPebble.CheckpointInfo{Backend: "pebble", SchemaVersion: 1, IndexGeneration: 7}
	if err := writeMemoryBackupResult(&output, "/backup", info, true); err != nil {
		t.Fatal(err)
	}
	var result map[string]any
	if err := json.Unmarshal(output.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result["path"] != "/backup" || result["backend"] != "pebble" || result["index_generation"] != float64(7) {
		t.Fatalf("JSON result = %#v", result)
	}
}

func TestCreateMemoryBackupRefusesMissingSource(t *testing.T) {
	t.Parallel()
	stateDir := t.TempDir()
	destination := filepath.Join(t.TempDir(), "must-not-exist")
	if _, _, err := createMemoryBackup(context.Background(), stateDir, destination); err == nil {
		t.Fatal("createMemoryBackup(missing source) unexpectedly succeeded")
	}
	if _, err := os.Stat(destination); !os.IsNotExist(err) {
		t.Fatalf("missing-source backup created destination: %v", err)
	}
}

func TestRestoreMemoryBackupRequiresConfirmation(t *testing.T) {
	t.Parallel()
	if _, err := restoreMemoryBackup(context.Background(), "unused", "unused", false); err == nil || !strings.Contains(err.Error(), "--confirm") {
		t.Fatalf("restoreMemoryBackup(unconfirmed) error = %v", err)
	}
}

func TestRestoreMemoryBackupRestoresValidatedCheckpoint(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	liveStateDir := t.TempDir()
	liveStore, err := memoryPebble.Open(liveStateDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := liveStore.Close(); err != nil {
		t.Fatal(err)
	}

	sourceStateDir := t.TempDir()
	sourceStore, err := memoryPebble.Open(sourceStateDir)
	if err != nil {
		t.Fatal(err)
	}
	backupPath := filepath.Join(t.TempDir(), "memory-backup")
	if err := sourceStore.Checkpoint(ctx, backupPath); err != nil {
		t.Fatal(err)
	}
	if err := sourceStore.Close(); err != nil {
		t.Fatal(err)
	}

	result, err := restoreMemoryBackup(ctx, liveStateDir, backupPath, true)
	if err != nil {
		t.Fatal(err)
	}
	if result.SourcePath != backupPath || result.RollbackPath == "" {
		t.Fatalf("restore result = %#v", result)
	}
	if _, err := memoryPebble.ValidateCheckpoint(ctx, result.RollbackPath); err != nil {
		t.Fatalf("rollback checkpoint: %v", err)
	}
}

func TestRestoreMemoryBackupRefusesRunningStore(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	liveStateDir := t.TempDir()
	liveStore, err := memoryPebble.Open(liveStateDir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = liveStore.Close() }()

	sourceStateDir := t.TempDir()
	sourceStore, err := memoryPebble.Open(sourceStateDir)
	if err != nil {
		t.Fatal(err)
	}
	backupPath := filepath.Join(t.TempDir(), "memory-backup")
	if err := sourceStore.Checkpoint(ctx, backupPath); err != nil {
		t.Fatal(err)
	}
	if err := sourceStore.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := restoreMemoryBackup(ctx, liveStateDir, backupPath, true); !errors.Is(err, memoryStoreAPI.ErrConflict) {
		t.Fatalf("restoreMemoryBackup(running) error = %v, want ErrConflict", err)
	}
}

func TestWriteMemoryRestoreResultSupportsHumanAndMachineOutput(t *testing.T) {
	t.Parallel()
	result := memoryPebble.RestoreInfo{
		CheckpointInfo: memoryPebble.CheckpointInfo{Backend: "pebble", SchemaVersion: 1, IndexGeneration: 7},
		SourcePath:     "/backup",
		LivePath:       "/live",
		RollbackPath:   "/rollback",
	}
	var human bytes.Buffer
	if err := writeMemoryRestoreResult(&human, result, false); err != nil {
		t.Fatal(err)
	}
	if output := human.String(); !strings.Contains(output, "/backup") || !strings.Contains(output, "/rollback") {
		t.Fatalf("human output = %q", output)
	}
	var machine bytes.Buffer
	if err := writeMemoryRestoreResult(&machine, result, true); err != nil {
		t.Fatal(err)
	}
	var decoded memoryPebble.RestoreInfo
	if err := json.Unmarshal(machine.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.LivePath != "/live" || decoded.IndexGeneration != 7 {
		t.Fatalf("JSON result = %#v", decoded)
	}
}

func TestMemoryMigrationCommandHelpersRoundTripAndRefuseNonEmptyTarget(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	sourceStateDir := t.TempDir()
	sourceStore, err := memoryPebble.Open(sourceStateDir)
	if err != nil {
		t.Fatal(err)
	}
	service, err := memoryService.New(memoryService.Config{
		Store: sourceStore,
		Actor: memoryService.ContextActorSource(memory.Actor{Kind: memory.ActorKindSystem, ID: "test:migration-command"}),
	})
	if err != nil {
		t.Fatal(err)
	}
	created, err := service.CreateChunk(ctx, memoryService.CreateChunkRequest{Chunk: memory.Chunk{
		Title: "Migration command", Kind: memory.ChunkKindReference,
		Scope: memory.Scope{Kind: memory.ScopeKindGlobal}, Visibility: memory.VisibilityPrivate,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := sourceStore.Close(); err != nil {
		t.Fatal(err)
	}

	destination := filepath.Join(t.TempDir(), "memory.migration.gz")
	exported, resolved, err := createMemoryMigrationExport(ctx, sourceStateDir, destination, false)
	if err != nil {
		t.Fatal(err)
	}
	if resolved != destination || exported.Stats.Chunks != 1 || exported.Stats.Revisions != 1 || exported.Size == 0 {
		t.Fatalf("migration export = %q %#v", resolved, exported)
	}
	if _, _, err := createMemoryMigrationExport(ctx, sourceStateDir, destination, false); !errors.Is(err, memoryStoreAPI.ErrConflict) {
		t.Fatalf("overwrite export error = %v, want ErrConflict", err)
	}

	targetStateDir := t.TempDir()
	imported, source, err := importMemoryMigration(ctx, targetStateDir, destination, true, false)
	if err != nil {
		t.Fatal(err)
	}
	if source != destination || imported.Chunks != 1 || imported.Revisions != 1 {
		t.Fatalf("migration import = %q %#v", source, imported)
	}
	targetStore, err := memoryPebble.OpenExisting(targetStateDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := targetStore.View(ctx, func(tx memoryStoreAPI.ReadTx) error {
		chunk, err := tx.Chunk(ctx, created.Chunk.ID)
		if err == nil && chunk.Title != created.Chunk.Title {
			t.Fatalf("imported chunk = %#v", chunk)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := targetStore.Close(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := importMemoryMigration(ctx, targetStateDir, destination, true, false); !errors.Is(err, memoryStoreAPI.ErrConflict) {
		t.Fatalf("non-empty import error = %v, want ErrConflict", err)
	}
}

func TestMemoryMigrationImportRequiresConfirmationAndPersonalConsent(t *testing.T) {
	t.Parallel()
	if _, _, err := importMemoryMigration(context.Background(), "unused", "unused", false, false); err == nil || !strings.Contains(err.Error(), "--confirm") {
		t.Fatalf("unconfirmed import error = %v", err)
	}
	personal := memory.Chunk{ID: memoryService.PersonalMeChunkID, Kind: memory.ChunkKindPersonal, Scope: memory.Scope{Kind: memory.ScopeKindPersonal}}
	if !migrationContainsPersonal(memoryStoreAPI.MigrationSnapshot{Records: []memoryStoreAPI.CanonicalRecord{{Kind: memoryStoreAPI.RecordKindChunk, Chunk: &personal}}}) {
		t.Fatal("migrationContainsPersonal() = false")
	}
	ordinary := memory.Chunk{ID: "019f132e-4f3a-739a-9ab2-5198dcd19e67", Kind: memory.ChunkKindReference, Scope: memory.Scope{Kind: memory.ScopeKindGlobal}}
	if migrationContainsPersonal(memoryStoreAPI.MigrationSnapshot{Records: []memoryStoreAPI.CanonicalRecord{{Kind: memoryStoreAPI.RecordKindChunk, Chunk: &ordinary}}}) {
		t.Fatal("migrationContainsPersonal(ordinary) = true")
	}
}

func TestWriteMemoryMigrationResultsSupportJSON(t *testing.T) {
	t.Parallel()
	stats := memoryStoreAPI.MigrationStats{ScanStats: memoryStoreAPI.ScanStats{Chunks: 2, Total: 2}, Revisions: 3, Assets: 4}
	var exported bytes.Buffer
	if err := writeMemoryMigrationExportResult(&exported, "/migration", migrationarchive.ExportResult{SHA256: "abc", Size: 12, Stats: stats}, true); err != nil {
		t.Fatal(err)
	}
	var exportResult map[string]any
	if err := json.Unmarshal(exported.Bytes(), &exportResult); err != nil {
		t.Fatal(err)
	}
	if exportResult["path"] != "/migration" || exportResult["sha256"] != "abc" || exportResult["revisions"] != float64(3) {
		t.Fatalf("export JSON = %#v", exportResult)
	}
	var imported bytes.Buffer
	if err := writeMemoryMigrationImportResult(&imported, "/migration", stats, true); err != nil {
		t.Fatal(err)
	}
	var importResult map[string]any
	if err := json.Unmarshal(imported.Bytes(), &importResult); err != nil {
		t.Fatal(err)
	}
	if importResult["path"] != "/migration" || importResult["assets"] != float64(4) {
		t.Fatalf("import JSON = %#v", importResult)
	}
}
