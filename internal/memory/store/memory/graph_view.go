package memory

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/lkarlslund/koder/internal/memory"
	memoryStoreAPI "github.com/lkarlslund/koder/internal/memory/store"
)

var _ memoryStoreAPI.GraphViewStore = (*Store)(nil)

func graphViewMapKey(owner memory.Actor, id string) string {
	return memoryStoreAPI.GraphViewOwnerKey(owner) + "\x00" + id
}

func cloneGraphView(view memoryStoreAPI.SavedGraphView) memoryStoreAPI.SavedGraphView {
	view.State.HiddenNodes = slices.Clone(view.State.HiddenNodes)
	view.State.HiddenEdges = slices.Clone(view.State.HiddenEdges)
	view.State.PinnedNodes = slices.Clone(view.State.PinnedNodes)
	view.State.Frontier = slices.Clone(view.State.Frontier)
	if view.State.Root != nil {
		root := *view.State.Root
		view.State.Root = &root
	}
	return view
}

func (s *Store) ListGraphViews(ctx context.Context, owner memory.Actor) ([]memoryStoreAPI.SavedGraphView, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return nil, memoryStoreAPI.ErrClosed
	}
	ownerKey := memoryStoreAPI.GraphViewOwnerKey(owner)
	views := make([]memoryStoreAPI.SavedGraphView, 0)
	for _, view := range s.graphViews {
		if memoryStoreAPI.GraphViewOwnerKey(view.Owner) == ownerKey {
			views = append(views, cloneGraphView(view))
		}
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
	view, ok := s.graphViews[graphViewMapKey(owner, id)]
	if !ok {
		return memoryStoreAPI.SavedGraphView{}, fmt.Errorf("%w: graph view", memoryStoreAPI.ErrNotFound)
	}
	return cloneGraphView(view), nil
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
	key := graphViewMapKey(view.Owner, view.ID)
	current, exists := s.graphViews[key]
	if (!exists && (expectedRevision != 0 || view.Revision != 1)) || (exists && (current.Revision != expectedRevision || view.Revision != expectedRevision+1)) {
		return fmt.Errorf("%w: graph view", memoryStoreAPI.ErrConflict)
	}
	ownerKey := memoryStoreAPI.GraphViewOwnerKey(view.Owner)
	for otherKey, other := range s.graphViews {
		if otherKey != key && memoryStoreAPI.GraphViewOwnerKey(other.Owner) == ownerKey && strings.EqualFold(strings.TrimSpace(other.Name), strings.TrimSpace(view.Name)) {
			return fmt.Errorf("%w: graph view name", memoryStoreAPI.ErrConflict)
		}
	}
	s.graphViews[key] = cloneGraphView(view)
	return nil
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
	key := graphViewMapKey(owner, id)
	current, exists := s.graphViews[key]
	if !exists {
		return fmt.Errorf("%w: graph view", memoryStoreAPI.ErrNotFound)
	}
	if expectedRevision == 0 || current.Revision != expectedRevision {
		return fmt.Errorf("%w: graph view", memoryStoreAPI.ErrConflict)
	}
	delete(s.graphViews, key)
	return nil
}
