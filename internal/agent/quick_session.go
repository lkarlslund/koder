package agent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/lkarlslund/koder/internal/chat"
	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/id"
)

type QuickPromotionMode string

const (
	QuickPromotionMoveToNew QuickPromotionMode = "move_to_new_folder"
	QuickPromotionAssign    QuickPromotionMode = "assign_folder"
)

type PromoteQuickRequest struct {
	Mode                  QuickPromotionMode
	ProjectRoot           string
	DiscardGeneratedFiles bool
}

// DisposeQuickSession hard-stops and permanently deletes a quick session.
func (e *Engine) DisposeQuickSession(ctx context.Context, sessionID id.ID) error {
	owner, err := e.LoadSession(ctx, sessionID)
	if err != nil {
		return err
	}
	if owner.Snapshot().Session.Kind != domain.SessionKindQuick {
		return fmt.Errorf("session %s is not a quick session", sessionID)
	}
	if err := owner.Shutdown(ctx, chat.CancelReasonUserInterruptHard); err != nil {
		return err
	}
	return e.DeleteSession(ctx, sessionID)
}

// PromoteQuickSession converts a quick session and transfers or discards its managed workspace.
func (e *Engine) PromoteQuickSession(ctx context.Context, sessionID id.ID, req PromoteQuickRequest) (domain.Session, error) {
	owner, err := e.LoadSession(ctx, sessionID)
	if err != nil {
		return domain.Session{}, err
	}
	session := owner.Snapshot().Session
	source, err := e.managedQuickRoot(session)
	if err != nil {
		return domain.Session{}, err
	}
	if source == "" {
		return domain.Session{}, fmt.Errorf("quick session does not own its project root")
	}
	destination := strings.TrimSpace(req.ProjectRoot)
	switch req.Mode {
	case QuickPromotionMoveToNew:
		destination, err = validateNewProjectRoot(destination)
		if err != nil {
			return domain.Session{}, err
		}
		renamed, err := transferDirectory(source, destination)
		if err != nil {
			return domain.Session{}, fmt.Errorf("move quick chat project: %w", err)
		}
		updated, _, promoteErr := owner.PromoteQuick(ctx, destination)
		if promoteErr != nil {
			var rollbackErr error
			if renamed {
				rollbackErr = os.Rename(destination, source)
			} else {
				rollbackErr = os.RemoveAll(destination)
			}
			return domain.Session{}, errors.Join(promoteErr, rollbackErr)
		}
		if !renamed {
			if err := os.RemoveAll(source); err != nil {
				return updated, fmt.Errorf("remove transferred quick chat project: %w", err)
			}
		}
		return updated, nil
	case QuickPromotionAssign:
		destination, err = validateExistingProjectRoot(destination)
		if err != nil {
			return domain.Session{}, err
		}
		nonEmpty, err := directoryHasEntries(source)
		if err != nil {
			return domain.Session{}, err
		}
		if nonEmpty && !req.DiscardGeneratedFiles {
			return domain.Session{}, fmt.Errorf("generated project folder is not empty; discard confirmation is required")
		}
		staged := source + ".promoting"
		if err := os.Rename(source, staged); err != nil {
			return domain.Session{}, fmt.Errorf("stage generated project folder: %w", err)
		}
		updated, _, promoteErr := owner.PromoteQuick(ctx, destination)
		if promoteErr != nil {
			rollbackErr := os.Rename(staged, source)
			return domain.Session{}, errors.Join(promoteErr, rollbackErr)
		}
		if err := os.RemoveAll(staged); err != nil {
			return updated, fmt.Errorf("remove discarded generated project folder: %w", err)
		}
		return updated, nil
	default:
		return domain.Session{}, fmt.Errorf("unknown quick promotion mode %q", req.Mode)
	}
}

func validateNewProjectRoot(path string) (string, error) {
	if path == "" || !filepath.IsAbs(path) {
		return "", fmt.Errorf("new project folder must be an absolute path")
	}
	path = filepath.Clean(path)
	if _, err := os.Stat(path); err == nil {
		return "", fmt.Errorf("new project folder already exists: %s", path)
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	parent, err := os.Stat(filepath.Dir(path))
	if err != nil {
		return "", fmt.Errorf("stat new project folder parent: %w", err)
	}
	if !parent.IsDir() {
		return "", fmt.Errorf("new project folder parent must be a directory")
	}
	return path, nil
}

func validateExistingProjectRoot(path string) (string, error) {
	if path == "" {
		return "", nil
	}
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("project folder must be an absolute path")
	}
	path = filepath.Clean(path)
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("project folder must be a directory")
	}
	return path, nil
}

func directoryHasEntries(path string) (bool, error) {
	dir, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer dir.Close()
	_, err = dir.Readdirnames(1)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, io.EOF):
		return false, nil
	default:
		return false, err
	}
}

// transferDirectory renames when possible and otherwise copies without deleting source.
// The bool reports whether source was renamed and therefore needs a rollback move.
func transferDirectory(source, destination string) (bool, error) {
	if err := os.Rename(source, destination); err == nil {
		return true, nil
	} else if !errors.Is(err, syscall.EXDEV) {
		return false, err
	}
	if err := copyDirectory(source, destination); err != nil {
		_ = os.RemoveAll(destination)
		return false, err
	}
	return false, nil
}

func copyDirectory(source, destination string) error {
	return filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		target := filepath.Join(destination, rel)
		info, err := entry.Info()
		if err != nil {
			return err
		}
		switch {
		case entry.IsDir():
			return os.MkdirAll(target, info.Mode().Perm())
		case entry.Type()&os.ModeSymlink != 0:
			link, err := os.Readlink(path)
			if err != nil {
				return err
			}
			return os.Symlink(link, target)
		case entry.Type().IsRegular():
			return copyRegularFile(path, target, info.Mode().Perm())
		default:
			return fmt.Errorf("unsupported workspace file type: %s", path)
		}
	})
}

func copyRegularFile(source, destination string, mode fs.FileMode) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	return errors.Join(copyErr, closeErr)
}
