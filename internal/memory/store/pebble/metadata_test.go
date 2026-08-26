package pebble

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	cockroachpebble "github.com/cockroachdb/pebble"

	memoryStoreAPI "github.com/lkarlslund/koder/internal/memory/store"
)

func TestOpenPersistsAndReusesMetadata(t *testing.T) {
	t.Parallel()
	stateDir := t.TempDir()
	s, err := Open(stateDir)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	first := s.meta
	if err := first.validate(); err != nil {
		t.Fatalf("metadata validate: %v", err)
	}
	health, err := s.Health(context.Background())
	if err != nil {
		t.Fatalf("Health() error = %v", err)
	}
	if health.SchemaVersion != currentSchemaVersion || health.IndexGeneration != initialIndexGeneration {
		t.Fatalf("Health() = %#v", health)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	reopened, err := Open(stateDir)
	if err != nil {
		t.Fatalf("second Open() error = %v", err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	if reopened.meta != first {
		t.Fatalf("reopened metadata = %#v, want %#v", reopened.meta, first)
	}
}

func TestOpenRejectsIncompatibleMetadata(t *testing.T) {
	t.Parallel()
	tests := map[string]func(*metadata){
		"backend":          func(value *metadata) { value.Backend = "other" },
		"key format":       func(value *metadata) { value.KeyFormat = "k2" },
		"encoding":         func(value *metadata) { value.Encoding = "other" },
		"schema version":   func(value *metadata) { value.SchemaVersion++ },
		"index generation": func(value *metadata) { value.IndexGeneration = 0 },
		"created at":       func(value *metadata) { value.CreatedAt = time.Time{} },
		"created at zone": func(value *metadata) {
			value.CreatedAt = value.CreatedAt.In(time.FixedZone("offset", 3600))
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			stateDir := initializedStateDir(t)
			writeMetadata(t, stateDir, mutate)
			if _, err := Open(stateDir); !errors.Is(err, memoryStoreAPI.ErrIncompatible) {
				t.Fatalf("Open() error = %v, want ErrIncompatible", err)
			}
		})
	}
}

func TestOpenRejectsCorruptOrUnknownMetadata(t *testing.T) {
	t.Parallel()
	for name, data := range map[string][]byte{
		"corrupt":       []byte(`{"backend":`),
		"unknown field": []byte(`{"backend":"pebble","unknown":true}`),
		"trailing data": []byte(`{} {}`),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			stateDir := initializedStateDir(t)
			writeRawMetadata(t, stateDir, data)
			if _, err := Open(stateDir); !errors.Is(err, memoryStoreAPI.ErrIncompatible) {
				t.Fatalf("Open() error = %v, want ErrIncompatible", err)
			}
		})
	}
}

func TestOpenDoesNotAdoptNonEmptyDatabaseWithoutMetadata(t *testing.T) {
	t.Parallel()
	stateDir := t.TempDir()
	dir := filepath.Join(stateDir, directoryName)
	db := openRawDB(t, dir)
	if err := db.Set([]byte("foreign/key"), []byte("value"), cockroachpebble.Sync); err != nil {
		t.Fatalf("write foreign key: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close raw database: %v", err)
	}
	if _, err := Open(stateDir); !errors.Is(err, memoryStoreAPI.ErrIncompatible) {
		t.Fatalf("Open() error = %v, want ErrIncompatible", err)
	}
}

func TestMetadataWriteIsDurable(t *testing.T) {
	t.Parallel()
	stateDir := t.TempDir()
	s, err := Open(stateDir)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	want := s.meta
	if err := s.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	db := openRawDB(t, filepath.Join(stateDir, directoryName))
	t.Cleanup(func() { _ = db.Close() })
	data, closer, err := db.Get(metadataKey())
	if err != nil {
		t.Fatalf("read metadata: %v", err)
	}
	defer func() { _ = closer.Close() }()
	got, err := decodeMetadata(data)
	if err != nil {
		t.Fatalf("decode metadata: %v", err)
	}
	if got != want {
		t.Fatalf("durable metadata = %#v, want %#v", got, want)
	}
}

func initializedStateDir(t *testing.T) string {
	t.Helper()
	stateDir := t.TempDir()
	s, err := Open(stateDir)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	return stateDir
}

func writeMetadata(t *testing.T, stateDir string, mutate func(*metadata)) {
	t.Helper()
	db := openRawDB(t, filepath.Join(stateDir, directoryName))
	data, closer, err := db.Get(metadataKey())
	if err != nil {
		t.Fatalf("read metadata: %v", err)
	}
	value, err := decodeMetadata(data)
	_ = closer.Close()
	if err != nil {
		t.Fatalf("decode metadata: %v", err)
	}
	mutate(&value)
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("encode metadata: %v", err)
	}
	if err := db.Set(metadataKey(), encoded, cockroachpebble.Sync); err != nil {
		t.Fatalf("write metadata: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close raw database: %v", err)
	}
}

func writeRawMetadata(t *testing.T, stateDir string, data []byte) {
	t.Helper()
	db := openRawDB(t, filepath.Join(stateDir, directoryName))
	if err := db.Set(metadataKey(), data, cockroachpebble.Sync); err != nil {
		t.Fatalf("write raw metadata: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close raw database: %v", err)
	}
}

func openRawDB(t *testing.T, dir string) *cockroachpebble.DB {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("MkdirAll(): %v", err)
	}
	db, err := cockroachpebble.Open(dir, &cockroachpebble.Options{Logger: quietLogger{}})
	if err != nil {
		t.Fatalf("open raw database: %v", err)
	}
	return db
}
