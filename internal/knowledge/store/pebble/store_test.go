package pebble

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	cockroachpebble "github.com/cockroachdb/pebble"
)

func TestOpenUsesIndependentKnowledgeDirectory(t *testing.T) {
	t.Parallel()
	stateDir := t.TempDir()
	s, err := Open(stateDir)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	wantDir := filepath.Join(stateDir, "knowledge-pebble-v1")
	health, err := s.Health(context.Background())
	if err != nil {
		t.Fatalf("Health() error = %v", err)
	}
	if health.Backend != "pebble" || health.Path != wantDir || !health.Open {
		t.Fatalf("Health() = %#v, want open Pebble at %q", health, wantDir)
	}
	if info, err := os.Stat(wantDir); err != nil || !info.IsDir() {
		t.Fatalf("knowledge directory Stat() = %#v, %v", info, err)
	}
	if _, err := os.Stat(filepath.Join(stateDir, "store-pebble-v7")); !os.IsNotExist(err) {
		t.Fatalf("Open() unexpectedly created or touched the main store: %v", err)
	}
}

func TestKnowledgeAndMainPebbleLocksAreIndependent(t *testing.T) {
	t.Parallel()
	stateDir := t.TempDir()
	mainDir := filepath.Join(stateDir, "store-pebble-v7")
	mainDB, err := cockroachpebble.Open(mainDir, &cockroachpebble.Options{Logger: quietLogger{}})
	if err != nil {
		t.Fatalf("open representative main database: %v", err)
	}
	t.Cleanup(func() { _ = mainDB.Close() })

	knowledgeDB, err := Open(stateDir)
	if err != nil {
		t.Fatalf("Open() while main database is locked error = %v", err)
	}
	t.Cleanup(func() { _ = knowledgeDB.Close() })

	if knowledgeDB.dir == mainDir {
		t.Fatalf("knowledge directory %q equals main directory", knowledgeDB.dir)
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
