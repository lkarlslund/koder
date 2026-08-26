package pebble

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	cockroachpebble "github.com/cockroachdb/pebble"

	memoryStoreAPI "github.com/lkarlslund/koder/internal/memory/store"
)

func TestOpenUsesIndependentMemoryDirectory(t *testing.T) {
	t.Parallel()
	stateDir := t.TempDir()
	s, err := Open(stateDir)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	wantDir := filepath.Join(stateDir, "memory-pebble-v1")
	health, err := s.Health(context.Background())
	if err != nil {
		t.Fatalf("Health() error = %v", err)
	}
	if health.Backend != "pebble" || health.Path != wantDir || !health.Open {
		t.Fatalf("Health() = %#v, want open Pebble at %q", health, wantDir)
	}
	if info, err := os.Stat(wantDir); err != nil || !info.IsDir() {
		t.Fatalf("memory directory Stat() = %#v, %v", info, err)
	}
	if _, err := os.Stat(filepath.Join(stateDir, "store-pebble-v7")); !os.IsNotExist(err) {
		t.Fatalf("Open() unexpectedly created or touched the main store: %v", err)
	}
}

func TestMemoryAndMainPebbleLocksAreIndependent(t *testing.T) {
	t.Parallel()
	stateDir := t.TempDir()
	mainDir := filepath.Join(stateDir, "store-pebble-v7")
	mainDB, err := cockroachpebble.Open(mainDir, &cockroachpebble.Options{Logger: quietLogger{}})
	if err != nil {
		t.Fatalf("open representative main database: %v", err)
	}
	t.Cleanup(func() { _ = mainDB.Close() })

	memoryDB, err := Open(stateDir)
	if err != nil {
		t.Fatalf("Open() while main database is locked error = %v", err)
	}
	t.Cleanup(func() { _ = memoryDB.Close() })

	if memoryDB.dir == mainDir {
		t.Fatalf("memory directory %q equals main directory", memoryDB.dir)
	}
}

func TestOpenRequiresUsableStateDirectory(t *testing.T) {
	t.Parallel()
	if _, err := Open(""); err == nil {
		t.Fatal("Open(\"\") unexpectedly succeeded")
	}
	root := t.TempDir()
	file := filepath.Join(root, "state-file")
	if err := os.WriteFile(file, []byte("not a directory"), 0o600); err != nil {
		t.Fatalf("WriteFile(): %v", err)
	}
	if _, err := Open(file); err == nil {
		t.Fatal("Open(file) unexpectedly succeeded")
	}
}

func TestOpenExistingNeverCreatesMissingMemoryStore(t *testing.T) {
	t.Parallel()
	stateDir := t.TempDir()
	databasePath, err := DefaultPath(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := OpenExisting(stateDir); !errors.Is(err, memoryStoreAPI.ErrNotFound) {
		t.Fatalf("OpenExisting(missing) error = %v", err)
	}
	if _, err := os.Stat(databasePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("OpenExisting created %s: %v", databasePath, err)
	}
	created, err := Open(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := created.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenExisting(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
}

func TestCloseUpdatesHealth(t *testing.T) {
	t.Parallel()
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	health, err := s.Health(context.Background())
	if err != nil {
		t.Fatalf("Health() error = %v", err)
	}
	if health.Open {
		t.Fatalf("Health() after Close() = %#v", health)
	}
}

func TestCloseIsIdempotentAndReleasesLock(t *testing.T) {
	t.Parallel()
	stateDir := t.TempDir()
	first, err := Open(stateDir)
	if err != nil {
		t.Fatalf("first Open() error = %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("first Close() error = %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("second Close() error = %v", err)
	}
	second, err := Open(stateDir)
	if err != nil {
		t.Fatalf("Open() after Close() error = %v", err)
	}
	if err := second.Close(); err != nil {
		t.Fatalf("close reopened store: %v", err)
	}
}

func TestConcurrentOpenCannotShareMemoryLock(t *testing.T) {
	t.Parallel()
	stateDir := t.TempDir()
	first, err := Open(stateDir)
	if err != nil {
		t.Fatalf("first Open() error = %v", err)
	}
	t.Cleanup(func() { _ = first.Close() })
	if second, err := Open(stateDir); err == nil {
		_ = second.Close()
		t.Fatal("second Open() unexpectedly shared the database lock")
	}
	health, err := first.Health(context.Background())
	if err != nil || !health.Open {
		t.Fatalf("first store after rejected Open() Health() = %#v, %v", health, err)
	}
}

func TestFailedOpenReleasesMemoryLock(t *testing.T) {
	t.Parallel()
	stateDir := initializedStateDir(t)
	writeMetadata(t, stateDir, func(value *metadata) { value.SchemaVersion++ })
	if _, err := Open(stateDir); !errors.Is(err, memoryStoreAPI.ErrIncompatible) {
		t.Fatalf("Open() error = %v, want ErrIncompatible", err)
	}
	raw := openRawDB(t, filepath.Join(stateDir, directoryName))
	if err := raw.Close(); err != nil {
		t.Fatalf("close database after failed Open(): %v", err)
	}
}

func TestHealthHonorsCancellation(t *testing.T) {
	t.Parallel()
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.Health(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Health() error = %v, want context.Canceled", err)
	}
}
