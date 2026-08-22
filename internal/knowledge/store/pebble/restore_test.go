package pebble

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	knowledgeStore "github.com/lkarlslund/koder/internal/knowledge/store"
)

func TestRestoreCheckpointReplacesLiveStoreAndRetainsRollback(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	liveStateDir := t.TempDir()
	putRestoreTestChunk(t, liveStateDir, 1)

	sourceStateDir := t.TempDir()
	putRestoreTestChunk(t, sourceStateDir, 2)
	sourceStore, err := OpenExisting(sourceStateDir)
	if err != nil {
		t.Fatal(err)
	}
	backupPath := filepath.Join(t.TempDir(), "knowledge-backup")
	if err := sourceStore.Checkpoint(ctx, backupPath); err != nil {
		t.Fatal(err)
	}
	if err := sourceStore.Close(); err != nil {
		t.Fatal(err)
	}

	result, err := RestoreCheckpoint(ctx, liveStateDir, backupPath)
	if err != nil {
		t.Fatalf("RestoreCheckpoint() error = %v", err)
	}
	livePath, err := DefaultPath(liveStateDir)
	if err != nil {
		t.Fatal(err)
	}
	if result.SourcePath != backupPath || result.LivePath != livePath || result.RollbackPath == "" || result.RestoredAt.IsZero() {
		t.Fatalf("RestoreCheckpoint() = %#v", result)
	}
	if _, err := ValidateCheckpoint(ctx, result.RollbackPath); err != nil {
		t.Fatalf("retained rollback is invalid: %v", err)
	}
	if _, err := os.Stat(filepath.Join(result.RollbackPath, "CURRENT")); err != nil {
		t.Fatalf("rollback CURRENT: %v", err)
	}

	restored, err := OpenExisting(liveStateDir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = restored.Close() }()
	if err := restored.View(ctx, func(tx knowledgeStore.ReadTx) error {
		chunk, err := tx.Chunk(ctx, txChunkID)
		if err != nil {
			return err
		}
		if chunk.Revision.Number != 2 || chunk.Title != txChunk(2).Title {
			t.Fatalf("restored chunk = %#v, want revision 2", chunk)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestRestoreCheckpointRefusesLiveStoreAndLeavesItUnchanged(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	liveStateDir := t.TempDir()
	liveStore, err := Open(liveStateDir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = liveStore.Close() }()
	if err := liveStore.Update(ctx, func(tx knowledgeStore.WriteTx) error {
		return tx.PutChunk(ctx, txChunk(1), 0)
	}); err != nil {
		t.Fatal(err)
	}

	sourceStateDir := t.TempDir()
	putRestoreTestChunk(t, sourceStateDir, 2)
	sourceStore, err := OpenExisting(sourceStateDir)
	if err != nil {
		t.Fatal(err)
	}
	backupPath := filepath.Join(t.TempDir(), "knowledge-backup")
	if err := sourceStore.Checkpoint(ctx, backupPath); err != nil {
		t.Fatal(err)
	}
	if err := sourceStore.Close(); err != nil {
		t.Fatal(err)
	}

	if _, err := RestoreCheckpoint(ctx, liveStateDir, backupPath); !errors.Is(err, knowledgeStore.ErrConflict) {
		t.Fatalf("RestoreCheckpoint(live) error = %v, want ErrConflict", err)
	}
	if err := liveStore.View(ctx, func(tx knowledgeStore.ReadTx) error {
		chunk, err := tx.Chunk(ctx, txChunkID)
		if err != nil {
			return err
		}
		if chunk.Revision.Number != 1 {
			t.Fatalf("live chunk changed to revision %d", chunk.Revision.Number)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestRestoreCheckpointRejectsInvalidAndNestedSourcesWithoutChangingLive(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	liveStateDir := t.TempDir()
	putRestoreTestChunk(t, liveStateDir, 1)
	livePath, err := DefaultPath(liveStateDir)
	if err != nil {
		t.Fatal(err)
	}

	invalidPath := filepath.Join(t.TempDir(), "invalid")
	if err := os.Mkdir(invalidPath, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := RestoreCheckpoint(ctx, liveStateDir, invalidPath); !errors.Is(err, knowledgeStore.ErrIncompatible) {
		t.Fatalf("RestoreCheckpoint(invalid) error = %v, want ErrIncompatible", err)
	}
	if _, err := RestoreCheckpoint(ctx, liveStateDir, filepath.Join(livePath, "nested")); err == nil {
		t.Fatal("RestoreCheckpoint(nested) unexpectedly succeeded")
	}
	assertRestoreTestChunkRevision(t, liveStateDir, 1)
}

func TestRestoreCheckpointHonorsCancellationAndRequiresExistingLiveStore(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := RestoreCheckpoint(ctx, "unused", "unused"); !errors.Is(err, context.Canceled) {
		t.Fatalf("RestoreCheckpoint(canceled) error = %v, want context.Canceled", err)
	}

	sourceStateDir := t.TempDir()
	putRestoreTestChunk(t, sourceStateDir, 1)
	sourcePath, err := DefaultPath(sourceStateDir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := RestoreCheckpoint(context.Background(), t.TempDir(), sourcePath); !errors.Is(err, knowledgeStore.ErrNotFound) {
		t.Fatalf("RestoreCheckpoint(missing live) error = %v, want ErrNotFound", err)
	}
}

func TestRollbackRestoreSwapRestoresPreviousStoreWithoutDeletingFailedRestore(t *testing.T) {
	t.Parallel()
	parent := t.TempDir()
	livePath := filepath.Join(parent, "knowledge-pebble-v1")
	rollbackPath := filepath.Join(parent, "knowledge-pebble-v1.rollback-test")
	if err := os.Mkdir(livePath, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(livePath, "failed-marker"), []byte("failed"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(rollbackPath, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rollbackPath, "previous-marker"), []byte("previous"), 0o600); err != nil {
		t.Fatal(err)
	}

	err := rollbackRestoreSwap(livePath, rollbackPath, "test validation", errors.New("injected failure"))
	if err == nil || !strings.Contains(err.Error(), "previous store restored") {
		t.Fatalf("rollbackRestoreSwap() error = %v", err)
	}
	if got, err := os.ReadFile(filepath.Join(livePath, "previous-marker")); err != nil || string(got) != "previous" {
		t.Fatalf("restored previous marker = %q, %v", got, err)
	}
	failedMatches, err := filepath.Glob(livePath + ".failed-restore-*")
	if err != nil {
		t.Fatal(err)
	}
	if len(failedMatches) != 1 {
		t.Fatalf("failed restore paths = %v, want one", failedMatches)
	}
	if got, err := os.ReadFile(filepath.Join(failedMatches[0], "failed-marker")); err != nil || string(got) != "failed" {
		t.Fatalf("retained failed marker = %q, %v", got, err)
	}
}

func putRestoreTestChunk(t *testing.T, stateDir string, revision uint64) {
	t.Helper()
	store, err := Open(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Update(context.Background(), func(tx knowledgeStore.WriteTx) error {
		if err := tx.PutChunk(context.Background(), txChunk(1), 0); err != nil {
			return err
		}
		for current := uint64(2); current <= revision; current++ {
			if err := tx.PutChunk(context.Background(), txChunk(current), current-1); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
}

func assertRestoreTestChunkRevision(t *testing.T, stateDir string, revision uint64) {
	t.Helper()
	store, err := OpenExisting(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	if err := store.View(context.Background(), func(tx knowledgeStore.ReadTx) error {
		chunk, err := tx.Chunk(context.Background(), txChunkID)
		if err == nil && chunk.Revision.Number != revision {
			t.Fatalf("chunk revision = %d, want %d", chunk.Revision.Number, revision)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
}
