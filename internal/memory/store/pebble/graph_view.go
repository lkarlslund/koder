package pebble

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"

	cockroachpebble "github.com/cockroachdb/pebble"

	"github.com/lkarlslund/koder/internal/memory"
	memoryStoreAPI "github.com/lkarlslund/koder/internal/memory/store"
)

var _ memoryStoreAPI.GraphViewStore = (*Store)(nil)

func decodeGraphView(data []byte) (memoryStoreAPI.SavedGraphView, error) {
	var view memoryStoreAPI.SavedGraphView
	if err := json.Unmarshal(data, &view); err != nil {
		return view, fmt.Errorf("decode graph view: %w", err)
	}
	if err := view.Validate(); err != nil {
		return view, fmt.Errorf("decode graph view: %w", err)
	}
	return view, nil
}

func readGraphView(reader interface {
	Get([]byte) ([]byte, io.Closer, error)
}, owner memory.Actor, id string) (memoryStoreAPI.SavedGraphView, error) {
	data, closer, err := reader.Get(graphViewKey(memoryStoreAPI.GraphViewOwnerKey(owner), id))
	if errors.Is(err, cockroachpebble.ErrNotFound) {
		return memoryStoreAPI.SavedGraphView{}, fmt.Errorf("%w: graph view", memoryStoreAPI.ErrNotFound)
	}
	if err != nil {
		return memoryStoreAPI.SavedGraphView{}, fmt.Errorf("read graph view: %w", err)
	}
	defer func() { _ = closer.Close() }()
	view, err := decodeGraphView(data)
	if err != nil {
		return memoryStoreAPI.SavedGraphView{}, err
	}
	if memoryStoreAPI.GraphViewOwnerKey(view.Owner) != memoryStoreAPI.GraphViewOwnerKey(owner) || view.ID != id {
		return memoryStoreAPI.SavedGraphView{}, fmt.Errorf("%w: graph view identity mismatch", memoryStoreAPI.ErrIncompatible)
	}
	return view, nil
}

func (s *Store) ListGraphViews(ctx context.Context, owner memory.Actor) ([]memoryStoreAPI.SavedGraphView, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := owner.Validate(); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return nil, memoryStoreAPI.ErrClosed
	}
	prefix := graphViewOwnerPrefix(memoryStoreAPI.GraphViewOwnerKey(owner))
	lower, upper := prefixBounds(prefix)
	iter, err := s.db.NewIter(&cockroachpebble.IterOptions{LowerBound: lower, UpperBound: upper})
	if err != nil {
		return nil, fmt.Errorf("list graph views: %w", err)
	}
	defer func() { _ = iter.Close() }()
	views := make([]memoryStoreAPI.SavedGraphView, 0)
	for iter.First(); iter.Valid(); iter.Next() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		view, err := decodeGraphView(iter.Value())
		if err != nil {
			return nil, err
		}
		if memoryStoreAPI.GraphViewOwnerKey(view.Owner) != memoryStoreAPI.GraphViewOwnerKey(owner) {
			return nil, fmt.Errorf("%w: graph view owner mismatch", memoryStoreAPI.ErrIncompatible)
		}
		views = append(views, view)
	}
	if err := iter.Error(); err != nil {
		return nil, fmt.Errorf("list graph views: %w", err)
	}
	slices.SortFunc(views, func(left, right memoryStoreAPI.SavedGraphView) int {
		return strings.Compare(strings.ToLower(left.Name), strings.ToLower(right.Name))
	})
	return views, nil
}

func (s *Store) GraphView(ctx context.Context, owner memory.Actor, id string) (memoryStoreAPI.SavedGraphView, error) {
	if err := ctx.Err(); err != nil {
		return memoryStoreAPI.SavedGraphView{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return memoryStoreAPI.SavedGraphView{}, memoryStoreAPI.ErrClosed
	}
	return readGraphView(s.db, owner, id)
}

func (s *Store) PutGraphView(ctx context.Context, view memoryStoreAPI.SavedGraphView, expectedRevision uint64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := view.Validate(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return memoryStoreAPI.ErrClosed
	}
	current, err := readGraphView(s.db, view.Owner, view.ID)
	exists := err == nil
	if err != nil && !errors.Is(err, memoryStoreAPI.ErrNotFound) {
		return err
	}
	if (!exists && (expectedRevision != 0 || view.Revision != 1)) || (exists && (current.Revision != expectedRevision || view.Revision != expectedRevision+1)) {
		return fmt.Errorf("%w: graph view", memoryStoreAPI.ErrConflict)
	}
	views, err := listGraphViewsLocked(ctx, s.db, view.Owner)
	if err != nil {
		return err
	}
	for _, other := range views {
		if other.ID != view.ID && strings.EqualFold(strings.TrimSpace(other.Name), strings.TrimSpace(view.Name)) {
			return fmt.Errorf("%w: graph view name", memoryStoreAPI.ErrConflict)
		}
	}
	encoded, err := json.Marshal(view)
	if err != nil {
		return fmt.Errorf("encode graph view: %w", err)
	}
	if err := s.db.Set(graphViewKey(memoryStoreAPI.GraphViewOwnerKey(view.Owner), view.ID), encoded, cockroachpebble.Sync); err != nil {
		return fmt.Errorf("write graph view: %w", err)
	}
	return nil
}

func listGraphViewsLocked(ctx context.Context, db *cockroachpebble.DB, owner memory.Actor) ([]memoryStoreAPI.SavedGraphView, error) {
	prefix := graphViewOwnerPrefix(memoryStoreAPI.GraphViewOwnerKey(owner))
	lower, upper := prefixBounds(prefix)
	iter, err := db.NewIter(&cockroachpebble.IterOptions{LowerBound: lower, UpperBound: upper})
	if err != nil {
		return nil, fmt.Errorf("list graph views: %w", err)
	}
	defer func() { _ = iter.Close() }()
	var views []memoryStoreAPI.SavedGraphView
	for iter.First(); iter.Valid(); iter.Next() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		view, err := decodeGraphView(iter.Value())
		if err != nil {
			return nil, err
		}
		views = append(views, view)
	}
	if err := iter.Error(); err != nil {
		return nil, fmt.Errorf("list graph views: %w", err)
	}
	return views, nil
}

func (s *Store) DeleteGraphView(ctx context.Context, owner memory.Actor, id string, expectedRevision uint64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return memoryStoreAPI.ErrClosed
	}
	current, err := readGraphView(s.db, owner, id)
	if err != nil {
		return err
	}
	if expectedRevision == 0 || current.Revision != expectedRevision {
		return fmt.Errorf("%w: graph view", memoryStoreAPI.ErrConflict)
	}
	if err := s.db.Delete(graphViewKey(memoryStoreAPI.GraphViewOwnerKey(owner), id), cockroachpebble.Sync); err != nil {
		return fmt.Errorf("delete graph view: %w", err)
	}
	return nil
}
