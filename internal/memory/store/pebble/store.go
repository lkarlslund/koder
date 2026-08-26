package pebble

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	cockroachpebble "github.com/cockroachdb/pebble"

	memoryStoreAPI "github.com/lkarlslund/koder/internal/memory/store"
)

const directoryName = "memory-pebble-v1"

// DefaultPath returns the independent Memory database directory below stateDir.
func DefaultPath(stateDir string) (string, error) {
	stateDir = strings.TrimSpace(stateDir)
	if stateDir == "" {
		return "", fmt.Errorf("memory state directory is required")
	}
	abs, err := filepath.Abs(stateDir)
	if err != nil {
		return "", fmt.Errorf("resolve memory state directory: %w", err)
	}
	return filepath.Join(abs, directoryName), nil
}

// Store owns the independent Pebble database used by Koder Memory.
type Store struct {
	mu                     sync.RWMutex
	db                     *cockroachpebble.DB
	dir                    string
	meta                   metadata
	closed                 bool
	indexes                []indexDefinition
	rebuildMu              sync.Mutex
	rebuildStatus          memoryStoreAPI.IndexRebuildStatus
	statusMu               sync.RWMutex
	rebuildTarget          uint64
	rebuildJournal         []indexMutation
	rebuildJournalOverflow bool
}

// Open creates or opens the Memory database below stateDir. It never opens or shares
// Koder's main application database.
func Open(stateDir string) (*Store, error) {
	dir, err := DefaultPath(stateDir)
	if err != nil {
		return nil, fmt.Errorf("open memory pebble: %w", err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create memory pebble directory: %w", err)
	}
	db, err := cockroachpebble.Open(dir, &cockroachpebble.Options{Logger: quietLogger{}})
	if err != nil {
		return nil, fmt.Errorf("open memory pebble: %w", err)
	}
	meta, err := initializeMetadata(db, time.Now())
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	return &Store{
		db:      db,
		dir:     dir,
		meta:    meta,
		indexes: defaultIndexDefinitions(),
		rebuildStatus: memoryStoreAPI.IndexRebuildStatus{
			ActiveGeneration: meta.IndexGeneration,
		},
	}, nil
}

// OpenExisting opens a Memory database without creating an empty one when the
// configured state directory is wrong. Maintenance commands use this safer entrypoint.
func OpenExisting(stateDir string) (*Store, error) {
	dir, err := DefaultPath(stateDir)
	if err != nil {
		return nil, fmt.Errorf("open existing memory pebble: %w", err)
	}
	info, err := os.Stat(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("open existing memory pebble: %w: %s", memoryStoreAPI.ErrNotFound, dir)
		}
		return nil, fmt.Errorf("open existing memory pebble: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("open existing memory pebble: %w: database path is not a directory", memoryStoreAPI.ErrIncompatible)
	}
	return Open(stateDir)
}

// Health reports this independent backend's current lifecycle state.
func (s *Store) Health(ctx context.Context) (memoryStoreAPI.Health, error) {
	if err := ctx.Err(); err != nil {
		return memoryStoreAPI.Health{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return memoryStoreAPI.Health{
		Backend:         backendName,
		Path:            s.dir,
		Open:            !s.closed,
		SchemaVersion:   s.meta.SchemaVersion,
		IndexGeneration: s.meta.IndexGeneration,
	}, nil
}

// Close releases the Memory database without affecting Koder's main store.
func (s *Store) Close() error {
	s.rebuildMu.Lock()
	defer s.rebuildMu.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	if err := s.db.Close(); err != nil && !errors.Is(err, cockroachpebble.ErrClosed) {
		return fmt.Errorf("close memory pebble: %w", err)
	}
	return nil
}

type quietLogger struct{}

func (quietLogger) Infof(string, ...interface{}) {}

func (quietLogger) Fatalf(format string, args ...interface{}) {
	log.Printf("memory pebble fatal: "+format, args...)
}
