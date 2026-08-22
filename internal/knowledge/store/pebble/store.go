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

	knowledgeStore "github.com/lkarlslund/koder/internal/knowledge/store"
)

const directoryName = "knowledge-pebble-v1"

// Store owns the independent Pebble database used by Koder Knowledge.
type Store struct {
	mu     sync.RWMutex
	db     *cockroachpebble.DB
	dir    string
	meta   metadata
	closed bool
}

// Open creates or opens the Knowledge database below stateDir. It never opens or shares
// Koder's main application database.
func Open(stateDir string) (*Store, error) {
	if strings.TrimSpace(stateDir) == "" {
		return nil, fmt.Errorf("open knowledge pebble: state directory is required")
	}
	dir := filepath.Join(stateDir, directoryName)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create knowledge pebble directory: %w", err)
	}
	db, err := cockroachpebble.Open(dir, &cockroachpebble.Options{Logger: quietLogger{}})
	if err != nil {
		return nil, fmt.Errorf("open knowledge pebble: %w", err)
	}
	meta, err := initializeMetadata(db, time.Now())
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	return &Store{db: db, dir: dir, meta: meta}, nil
}

// Health reports this independent backend's current lifecycle state.
func (s *Store) Health(ctx context.Context) (knowledgeStore.Health, error) {
	if err := ctx.Err(); err != nil {
		return knowledgeStore.Health{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return knowledgeStore.Health{
		Backend:         backendName,
		Path:            s.dir,
		Open:            !s.closed,
		SchemaVersion:   s.meta.SchemaVersion,
		IndexGeneration: s.meta.IndexGeneration,
	}, nil
}

// Close releases the Knowledge database without affecting Koder's main store.
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	if err := s.db.Close(); err != nil && !errors.Is(err, cockroachpebble.ErrClosed) {
		return fmt.Errorf("close knowledge pebble: %w", err)
	}
	return nil
}

type quietLogger struct{}

func (quietLogger) Infof(string, ...interface{}) {}

func (quietLogger) Fatalf(format string, args ...interface{}) {
	log.Printf("knowledge pebble fatal: "+format, args...)
}
