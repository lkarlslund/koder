package store

import (
	"context"
	"path/filepath"
	"strings"
	"time"
)

// GetWorkspaceState loads renderer and workspace preferences for a workdir.
func (s *Store) GetWorkspaceState(ctx context.Context, workdir string) (WorkspaceState, error) {
	workdir = cleanWorkspacePath(workdir)
	items, err := s.WorkspaceStates().List(ctx, ByIndex[WorkspaceState]("workdir", workdir))
	if err != nil {
		return WorkspaceState{}, err
	}
	if len(items) == 0 {
		return WorkspaceState{Workdir: workdir}, nil
	}
	return items[0], nil
}

// SetWorkspaceWebBind stores the reusable web UI bind address for a workdir.
func (s *Store) SetWorkspaceWebBind(ctx context.Context, workdir string, bind string) error {
	state, err := s.GetWorkspaceState(ctx, workdir)
	if err != nil {
		return err
	}
	state.Workdir = cleanWorkspacePath(workdir)
	state.WebBind = strings.TrimSpace(bind)
	state.UpdatedAt = time.Now().UTC()
	if state.ID == 0 {
		_, err = s.WorkspaceStates().Insert(ctx, state)
		return err
	}
	return s.WorkspaceStates().Put(ctx, state)
}

func cleanWorkspacePath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	return filepath.Clean(path)
}
