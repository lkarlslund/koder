package store

import (
	"context"
	"errors"
	"os"
	"strings"
	"time"
)

const globalRuntimeStateID = "global"

// GlobalRuntimeState loads global runtime preferences.
func (s *Store) GlobalRuntimeState(ctx context.Context) (RuntimeState, error) {
	state, err := s.RuntimeStates().Get(ctx, globalRuntimeStateID)
	if err != nil {
		if isMissingRuntimeState(err) {
			return RuntimeState{ID: globalRuntimeStateID}, nil
		}
		return RuntimeState{}, err
	}
	return state, nil
}

// SetLastWebBind stores the last successful reusable web UI bind address.
func (s *Store) SetLastWebBind(ctx context.Context, bind string) error {
	state, err := s.GlobalRuntimeState(ctx)
	if err != nil {
		return err
	}
	state.ID = globalRuntimeStateID
	state.LastWebBind = strings.TrimSpace(bind)
	state.UpdatedAt = time.Now().UTC()
	return s.RuntimeStates().Put(ctx, state)
}

func isMissingRuntimeState(err error) bool {
	return errors.Is(err, os.ErrNotExist) || strings.Contains(err.Error(), "not found")
}
