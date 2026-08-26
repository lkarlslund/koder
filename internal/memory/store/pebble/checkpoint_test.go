package pebble

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	cockroachpebble "github.com/cockroachdb/pebble"

	memoryStoreAPI "github.com/lkarlslund/koder/internal/memory/store"
)

func TestCheckpointCreatesValidatedRestorableDatabase(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	stateDir := t.TempDir()
	s, err := Open(stateDir)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	chunk := txChunk(1)
	if err := s.Update(ctx, func(tx memoryStoreAPI.WriteTx) error { return tx.PutChunk(ctx, chunk, 0) }); err != nil {
		t.Fatalf("put chunk: %v", err)
	}

	restoreStateDir := t.TempDir()
	checkpointDir := filepath.Join(restoreStateDir, directoryName)
	if err := s.Checkpoint(ctx, checkpointDir); err != nil {
		t.Fatalf("Checkpoint() error = %v", err)
	}
	info, err := ValidateCheckpoint(ctx, checkpointDir)
	if err != nil {
		t.Fatalf("ValidateCheckpoint() error = %v", err)
	}
	if info.Backend != backendName || info.SchemaVersion != currentSchemaVersion || info.IndexGeneration != initialIndexGeneration || info.CreatedAt.IsZero() {
		t.Fatalf("ValidateCheckpoint() = %#v", info)
	}

	restored, err := Open(restoreStateDir)
	if err != nil {
		t.Fatalf("Open(restored checkpoint) error = %v", err)
	}
	t.Cleanup(func() { _ = restored.Close() })
	if err := restored.View(ctx, func(tx memoryStoreAPI.ReadTx) error {
		got, err := tx.Chunk(ctx, chunk.ID)
		if err != nil {
			return err
		}
		if got.Title != chunk.Title || got.Revision != chunk.Revision {
			t.Fatalf("restored chunk = %#v, want %#v", got, chunk)
		}
		return nil
	}); err != nil {
		t.Fatalf("read restored checkpoint: %v", err)
	}
}

func TestCheckpointRefusesExistingOrNestedDestination(t *testing.T) {
	t.Parallel()
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	existing := filepath.Join(t.TempDir(), "existing")
	if err := os.Mkdir(existing, 0o700); err != nil {
		t.Fatalf("Mkdir() error = %v", err)
	}
	marker := filepath.Join(existing, "keep")
	if err := os.WriteFile(marker, []byte("preserve"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if err := s.Checkpoint(context.Background(), existing); err == nil {
		t.Fatal("Checkpoint(existing) unexpectedly succeeded")
	}
	if got, err := os.ReadFile(marker); err != nil || string(got) != "preserve" {
		t.Fatalf("existing destination changed: %q, %v", got, err)
	}

	if err := s.Checkpoint(context.Background(), filepath.Join(s.dir, "nested")); err == nil {
		t.Fatal("Checkpoint(nested) unexpectedly succeeded")
	}
	symlink := filepath.Join(t.TempDir(), "database-link")
	if err := os.Symlink(s.dir, symlink); err != nil {
		t.Fatalf("Symlink() error = %v", err)
	}
	if err := s.Checkpoint(context.Background(), filepath.Join(symlink, "nested-through-symlink")); err == nil {
		t.Fatal("Checkpoint(nested through symlink) unexpectedly succeeded")
	}
}

func TestValidateCheckpointRejectsWrongIdentityAndSchema(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	t.Run("foreign keyspace", func(t *testing.T) {
		checkpointDir := filepath.Join(t.TempDir(), "checkpoint")
		if err := s.Checkpoint(ctx, checkpointDir); err != nil {
			t.Fatalf("Checkpoint() error = %v", err)
		}
		db := openWritableCheckpoint(t, checkpointDir)
		if err := db.Set([]byte("foreign/key"), []byte("value"), cockroachpebble.Sync); err != nil {
			t.Fatalf("Set(foreign key) error = %v", err)
		}
		if err := db.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
		if _, err := ValidateCheckpoint(ctx, checkpointDir); !errors.Is(err, memoryStoreAPI.ErrIncompatible) {
			t.Fatalf("ValidateCheckpoint() error = %v, want ErrIncompatible", err)
		}
	})

	t.Run("future schema", func(t *testing.T) {
		checkpointDir := filepath.Join(t.TempDir(), "checkpoint")
		if err := s.Checkpoint(ctx, checkpointDir); err != nil {
			t.Fatalf("Checkpoint() error = %v", err)
		}
		db := openWritableCheckpoint(t, checkpointDir)
		value, err := readMetadata(db)
		if err != nil {
			t.Fatalf("readMetadata() error = %v", err)
		}
		value.SchemaVersion++
		encoded, err := json.Marshal(value)
		if err != nil {
			t.Fatalf("Marshal() error = %v", err)
		}
		if err := db.Set(metadataKey(), encoded, cockroachpebble.Sync); err != nil {
			t.Fatalf("Set(metadata) error = %v", err)
		}
		if err := db.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
		if _, err := ValidateCheckpoint(ctx, checkpointDir); !errors.Is(err, memoryStoreAPI.ErrIncompatible) {
			t.Fatalf("ValidateCheckpoint() error = %v, want ErrIncompatible", err)
		}
	})
}

func TestValidateCheckpointRejectsMissingDatabaseAndHonorsCancellation(t *testing.T) {
	t.Parallel()
	if _, err := ValidateCheckpoint(context.Background(), filepath.Join(t.TempDir(), "missing")); !errors.Is(err, memoryStoreAPI.ErrIncompatible) {
		t.Fatalf("ValidateCheckpoint(missing) error = %v, want ErrIncompatible", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := ValidateCheckpoint(ctx, "unused"); !errors.Is(err, context.Canceled) {
		t.Fatalf("ValidateCheckpoint(canceled) error = %v, want context.Canceled", err)
	}
}

func TestCheckpointHonorsCancellation(t *testing.T) {
	t.Parallel()
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := s.Checkpoint(ctx, filepath.Join(t.TempDir(), "checkpoint")); !errors.Is(err, context.Canceled) {
		t.Fatalf("Checkpoint() error = %v, want context.Canceled", err)
	}
}

func openWritableCheckpoint(t *testing.T, directory string) *cockroachpebble.DB {
	t.Helper()
	db, err := cockroachpebble.Open(directory, &cockroachpebble.Options{Logger: quietLogger{}, ErrorIfNotExists: true})
	if err != nil {
		t.Fatalf("open writable checkpoint: %v", err)
	}
	return db
}
