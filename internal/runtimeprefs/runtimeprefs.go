package runtimeprefs

import (
	"context"
	"errors"
	"os"
	"strings"
	"time"

	"github.com/lkarlslund/koder/internal/store"
)

const globalStateID = "global"

type State struct {
	ID          string    `json:"id"`
	LastWebBind string    `json:"last_web_bind"`
	UpdatedAt   time.Time `json:"updated_at"`
}

func Collection(st *store.Store) store.Collection[State] {
	return store.NewCollection(st, store.CollectionSpec[State]{
		Namespace: "runtime-states",
		GetID:     func(v State) string { return v.ID },
		SetID:     func(v *State, id string) { v.ID = id },
	})
}

func Global(ctx context.Context, st *store.Store) (State, error) {
	state, err := Collection(st).Get(ctx, globalStateID)
	if err != nil {
		if isMissing(err) {
			return State{ID: globalStateID}, nil
		}
		return State{}, err
	}
	return state, nil
}

func SetLastWebBind(ctx context.Context, st *store.Store, bind string) error {
	state, err := Global(ctx, st)
	if err != nil {
		return err
	}
	state.ID = globalStateID
	state.LastWebBind = strings.TrimSpace(bind)
	state.UpdatedAt = time.Now().UTC()
	return Collection(st).Put(ctx, state)
}

func isMissing(err error) bool {
	return errors.Is(err, os.ErrNotExist) || strings.Contains(err.Error(), "not found")
}
