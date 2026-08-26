package pebble

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	cockroachpebble "github.com/cockroachdb/pebble"
	"github.com/cockroachdb/pebble/vfs"

	memoryStoreAPI "github.com/lkarlslund/koder/internal/memory/store"
)

type RestoreInfo struct {
	CheckpointInfo
	SourcePath   string    `json:"source_path"`
	LivePath     string    `json:"live_path"`
	RollbackPath string    `json:"rollback_path"`
	RestoredAt   time.Time `json:"restored_at"`
}

// RestoreCheckpoint replaces an offline existing Memory store with a validated
// checkpoint. The old store remains beside it as a validated rollback directory.
func RestoreCheckpoint(ctx context.Context, stateDir, source string) (RestoreInfo, error) {
	if err := ctx.Err(); err != nil {
		return RestoreInfo{}, err
	}
	livePath, err := DefaultPath(stateDir)
	if err != nil {
		return RestoreInfo{}, err
	}
	sourcePath, err := filepath.Abs(strings.TrimSpace(source))
	if err != nil || strings.TrimSpace(source) == "" {
		return RestoreInfo{}, fmt.Errorf("restore memory checkpoint: source directory is required")
	}
	same, err := sameResolvedPath(livePath, sourcePath)
	if err != nil {
		return RestoreInfo{}, fmt.Errorf("restore memory checkpoint: resolve paths: %w", err)
	}
	if same {
		return RestoreInfo{}, fmt.Errorf("restore memory checkpoint: source must differ from the live database")
	}
	insideLive, err := pathWithin(livePath, sourcePath)
	if err != nil {
		return RestoreInfo{}, fmt.Errorf("restore memory checkpoint: resolve source containment: %w", err)
	}
	liveInsideSource, err := pathWithin(sourcePath, livePath)
	if err != nil {
		return RestoreInfo{}, fmt.Errorf("restore memory checkpoint: resolve live containment: %w", err)
	}
	if insideLive || liveInsideSource {
		return RestoreInfo{}, fmt.Errorf("restore memory checkpoint: source and live database must not contain one another")
	}
	_, err = ValidateCheckpoint(ctx, sourcePath)
	if err != nil {
		return RestoreInfo{}, fmt.Errorf("restore memory checkpoint: validate source: %w", err)
	}
	liveStat, err := os.Stat(livePath)
	if err != nil {
		if os.IsNotExist(err) {
			return RestoreInfo{}, fmt.Errorf("restore memory checkpoint: %w: live Memory store does not exist", memoryStoreAPI.ErrNotFound)
		}
		return RestoreInfo{}, fmt.Errorf("restore memory checkpoint: inspect live store: %w", err)
	}
	if !liveStat.IsDir() {
		return RestoreInfo{}, fmt.Errorf("restore memory checkpoint: live Memory store is not a directory")
	}
	lock, err := cockroachpebble.LockDirectory(livePath, vfs.Default)
	if err != nil {
		return RestoreInfo{}, fmt.Errorf("%w: Memory is in use; stop Koder before restoring", memoryStoreAPI.ErrConflict)
	}
	defer func() { _ = lock.Close() }()
	// Validate through the lock that remains held for the entire swap. This both
	// verifies the current database and prevents Koder from starting mid-restore.
	if _, err := validateCheckpointWithLock(ctx, livePath, lock); err != nil {
		return RestoreInfo{}, fmt.Errorf("restore memory checkpoint: validate current store: %w", err)
	}

	parent := filepath.Dir(livePath)
	stagePath, err := os.MkdirTemp(parent, ".memory-restore-stage-")
	if err != nil {
		return RestoreInfo{}, fmt.Errorf("restore memory checkpoint: create staging directory: %w", err)
	}
	stageOwned := true
	defer func() {
		if stageOwned {
			_ = os.RemoveAll(stagePath)
		}
	}()
	if err := copyCheckpointTree(ctx, sourcePath, stagePath); err != nil {
		return RestoreInfo{}, fmt.Errorf("restore memory checkpoint: stage source: %w", err)
	}
	if _, err := ValidateCheckpoint(ctx, stagePath); err != nil {
		return RestoreInfo{}, fmt.Errorf("restore memory checkpoint: validate staged source: %w", err)
	}
	// Keep the lock across both renames and post-swap validation by making the
	// staged LOCK name refer to the same locked inode as the current database.
	if err := os.Remove(filepath.Join(stagePath, "LOCK")); err != nil && !os.IsNotExist(err) {
		return RestoreInfo{}, fmt.Errorf("restore memory checkpoint: prepare staged lock: %w", err)
	}
	if err := os.Link(filepath.Join(livePath, "LOCK"), filepath.Join(stagePath, "LOCK")); err != nil {
		return RestoreInfo{}, fmt.Errorf("restore memory checkpoint: preserve database lock: %w", err)
	}
	rollbackPath, err := nextSiblingPath(livePath + ".rollback-" + time.Now().UTC().Format("20060102T150405.000000000Z"))
	if err != nil {
		return RestoreInfo{}, fmt.Errorf("restore memory checkpoint: select rollback path: %w", err)
	}
	if err := os.Rename(livePath, rollbackPath); err != nil {
		return RestoreInfo{}, fmt.Errorf("restore memory checkpoint: retain rollback: %w", err)
	}
	if err := os.Rename(stagePath, livePath); err != nil {
		rollbackErr := os.Rename(rollbackPath, livePath)
		if rollbackErr != nil {
			return RestoreInfo{}, fmt.Errorf("restore memory checkpoint: activate staged store: %v; rollback failed: %w", err, rollbackErr)
		}
		return RestoreInfo{}, fmt.Errorf("restore memory checkpoint: activate staged store: %w", err)
	}
	stageOwned = false
	if err := syncDirectory(parent); err != nil {
		return RestoreInfo{}, rollbackRestoreSwap(livePath, rollbackPath, "sync restored store", err)
	}
	validated, err := validateCheckpointWithLock(ctx, livePath, lock)
	if err != nil {
		return RestoreInfo{}, rollbackRestoreSwap(livePath, rollbackPath, "validate restored store", err)
	}
	return RestoreInfo{
		CheckpointInfo: validated, SourcePath: sourcePath, LivePath: livePath,
		RollbackPath: rollbackPath, RestoredAt: time.Now().UTC().Round(0),
	}, nil
}

func validateCheckpointWithLock(ctx context.Context, directory string, lock *cockroachpebble.Lock) (CheckpointInfo, error) {
	if err := ctx.Err(); err != nil {
		return CheckpointInfo{}, err
	}
	db, err := cockroachpebble.Open(directory, &cockroachpebble.Options{
		ReadOnly: true, ErrorIfNotExists: true, Logger: quietLogger{}, Lock: lock,
	})
	if err != nil {
		return CheckpointInfo{}, fmt.Errorf("%w: open checkpoint: %v", memoryStoreAPI.ErrIncompatible, err)
	}
	defer func() { _ = db.Close() }()
	if err := db.CheckLevels(nil); err != nil {
		return CheckpointInfo{}, fmt.Errorf("%w: checkpoint table validation: %v", memoryStoreAPI.ErrIncompatible, err)
	}
	value, err := readMetadata(db)
	if err != nil {
		return CheckpointInfo{}, fmt.Errorf("%w: checkpoint identity: %v", memoryStoreAPI.ErrIncompatible, err)
	}
	if err := value.validate(); err != nil {
		return CheckpointInfo{}, err
	}
	if err := validateCheckpointKeyspace(db); err != nil {
		return CheckpointInfo{}, err
	}
	return CheckpointInfo{Backend: value.Backend, SchemaVersion: value.SchemaVersion, IndexGeneration: value.IndexGeneration, CreatedAt: value.CreatedAt}, ctx.Err()
}

func copyCheckpointTree(ctx context.Context, source, destination string) error {
	entries, err := os.ReadDir(source)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		sourcePath := filepath.Join(source, entry.Name())
		destinationPath := filepath.Join(destination, entry.Name())
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("checkpoint contains symbolic link %q", entry.Name())
		}
		if info.IsDir() {
			if err := os.Mkdir(destinationPath, info.Mode().Perm()); err != nil {
				return err
			}
			if err := copyCheckpointTree(ctx, sourcePath, destinationPath); err != nil {
				return err
			}
			if err := syncDirectory(destinationPath); err != nil {
				return err
			}
			continue
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("checkpoint contains unsupported file %q", entry.Name())
		}
		if err := copyCheckpointFile(ctx, sourcePath, destinationPath, info.Mode().Perm()); err != nil {
			return err
		}
	}
	return syncDirectory(destination)
}

func copyCheckpointFile(ctx context.Context, source, destination string, mode os.FileMode) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer func() { _ = input.Close() }()
	output, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	closed := false
	defer func() {
		if !closed {
			_ = output.Close()
		}
	}()
	if _, err := io.Copy(output, &contextReader{ctx: ctx, reader: input}); err != nil {
		return err
	}
	if err := output.Sync(); err != nil {
		return err
	}
	if err := output.Close(); err != nil {
		return err
	}
	closed = true
	return nil
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r *contextReader) Read(buffer []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(buffer)
}

func rollbackRestoreSwap(livePath, rollbackPath, operation string, cause error) error {
	failedPath, pathErr := nextSiblingPath(livePath + ".failed-restore-" + time.Now().UTC().Format("20060102T150405.000000000Z"))
	if pathErr != nil {
		return fmt.Errorf("restore memory checkpoint: %s: %v; select failed-restore path: %w", operation, cause, pathErr)
	}
	if err := os.Rename(livePath, failedPath); err != nil {
		return fmt.Errorf("restore memory checkpoint: %s: %v; move failed restore aside: %w", operation, cause, err)
	}
	if err := os.Rename(rollbackPath, livePath); err != nil {
		return fmt.Errorf("restore memory checkpoint: %s: %v; automatic rollback failed: %w", operation, cause, err)
	}
	_ = syncDirectory(filepath.Dir(livePath))
	return fmt.Errorf("restore memory checkpoint: %s: %w; previous store restored", operation, cause)
}

func nextSiblingPath(base string) (string, error) {
	for suffix := 0; ; suffix++ {
		candidate := base
		if suffix != 0 {
			candidate = fmt.Sprintf("%s-%d", base, suffix)
		}
		_, err := os.Lstat(candidate)
		if os.IsNotExist(err) {
			return candidate, nil
		}
		if err != nil {
			return "", err
		}
	}
}

func sameResolvedPath(left, right string) (bool, error) {
	left, err := resolvePath(left)
	if err != nil {
		return false, err
	}
	right, err = resolvePath(right)
	if err != nil {
		return false, err
	}
	return left == right, nil
}

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = directory.Close() }()
	return directory.Sync()
}
