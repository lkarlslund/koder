package pebble

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	cockroachpebble "github.com/cockroachdb/pebble"

	knowledgeStore "github.com/lkarlslund/koder/internal/knowledge/store"
)

// CheckpointInfo identifies a validated Knowledge checkpoint without exposing any
// canonical content or backend-private keys.
type CheckpointInfo struct {
	Backend         string    `json:"backend"`
	SchemaVersion   uint32    `json:"schema_version"`
	IndexGeneration uint64    `json:"index_generation"`
	CreatedAt       time.Time `json:"created_at"`
}

// Checkpoint creates and validates an independent Pebble snapshot at destination.
// Destination must not already exist and must not be inside the live database.
func (s *Store) Checkpoint(ctx context.Context, destination string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	destination = strings.TrimSpace(destination)
	if destination == "" {
		return fmt.Errorf("checkpoint knowledge pebble: destination is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return knowledgeStore.ErrClosed
	}
	inside, err := pathWithin(s.dir, destination)
	if err != nil {
		return fmt.Errorf("checkpoint knowledge pebble: resolve destination: %w", err)
	}
	if inside {
		return fmt.Errorf("checkpoint knowledge pebble: destination must be outside the live database")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := s.db.Checkpoint(destination, cockroachpebble.WithFlushedWAL()); err != nil {
		return fmt.Errorf("checkpoint knowledge pebble: %w", err)
	}
	if err := ctx.Err(); err != nil {
		_ = os.RemoveAll(destination)
		return err
	}
	if _, err := ValidateCheckpoint(ctx, destination); err != nil {
		_ = os.RemoveAll(destination)
		return fmt.Errorf("validate new knowledge checkpoint: %w", err)
	}
	return nil
}

// ValidateCheckpoint opens a prospective restore source read-only, validates Pebble's
// table invariants, and verifies Koder Knowledge's backend identity and format metadata.
// It never creates or modifies the candidate directory.
func ValidateCheckpoint(ctx context.Context, directory string) (CheckpointInfo, error) {
	if err := ctx.Err(); err != nil {
		return CheckpointInfo{}, err
	}
	directory = strings.TrimSpace(directory)
	if directory == "" {
		return CheckpointInfo{}, fmt.Errorf("%w: checkpoint directory is required", knowledgeStore.ErrIncompatible)
	}
	db, err := cockroachpebble.Open(directory, &cockroachpebble.Options{
		ReadOnly:         true,
		ErrorIfNotExists: true,
		Logger:           quietLogger{},
	})
	if err != nil {
		return CheckpointInfo{}, fmt.Errorf("%w: open checkpoint: %v", knowledgeStore.ErrIncompatible, err)
	}
	defer func() { _ = db.Close() }()
	if err := ctx.Err(); err != nil {
		return CheckpointInfo{}, err
	}
	if err := db.CheckLevels(nil); err != nil {
		return CheckpointInfo{}, fmt.Errorf("%w: checkpoint table validation: %v", knowledgeStore.ErrIncompatible, err)
	}
	value, err := readMetadata(db)
	if err != nil {
		return CheckpointInfo{}, fmt.Errorf("%w: checkpoint identity: %v", knowledgeStore.ErrIncompatible, err)
	}
	if err := value.validate(); err != nil {
		return CheckpointInfo{}, err
	}
	if err := validateCheckpointKeyspace(db); err != nil {
		return CheckpointInfo{}, err
	}
	if err := ctx.Err(); err != nil {
		return CheckpointInfo{}, err
	}
	return CheckpointInfo{
		Backend:         value.Backend,
		SchemaVersion:   value.SchemaVersion,
		IndexGeneration: value.IndexGeneration,
		CreatedAt:       value.CreatedAt,
	}, nil
}

func validateCheckpointKeyspace(db *cockroachpebble.DB) error {
	iter, err := db.NewIter(nil)
	if err != nil {
		return fmt.Errorf("%w: inspect checkpoint keyspace: %v", knowledgeStore.ErrIncompatible, err)
	}
	defer func() { _ = iter.Close() }()
	for iter.First(); iter.Valid(); iter.Next() {
		if !bytes.HasPrefix(iter.Key(), []byte(keyFormatPrefix)) {
			return fmt.Errorf("%w: checkpoint contains a foreign keyspace", knowledgeStore.ErrIncompatible)
		}
	}
	if err := iter.Error(); err != nil {
		return fmt.Errorf("%w: inspect checkpoint keyspace: %v", knowledgeStore.ErrIncompatible, err)
	}
	return nil
}

func pathWithin(parent, candidate string) (bool, error) {
	parent, err := resolvePath(parent)
	if err != nil {
		return false, err
	}
	candidate, err = resolvePath(candidate)
	if err != nil {
		return false, err
	}
	relative, err := filepath.Rel(parent, candidate)
	if err != nil {
		return false, err
	}
	return relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))), nil
}

func resolvePath(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	current := absolute
	var suffix []string
	for {
		resolved, err := filepath.EvalSymlinks(current)
		if err == nil {
			for index := len(suffix) - 1; index >= 0; index-- {
				resolved = filepath.Join(resolved, suffix[index])
			}
			return filepath.Clean(resolved), nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		parent := filepath.Dir(current)
		if parent == current {
			return absolute, nil
		}
		suffix = append(suffix, filepath.Base(current))
		current = parent
	}
}
