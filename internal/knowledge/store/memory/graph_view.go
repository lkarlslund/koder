package memory

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/lkarlslund/koder/internal/knowledge"
	knowledgeStore "github.com/lkarlslund/koder/internal/knowledge/store"
)

var _ knowledgeStore.GraphViewStore = (*Store)(nil)

func graphViewMapKey(owner knowledge.Actor, id string) string {
	return knowledgeStore.GraphViewOwnerKey(owner) + "\x00" + id
}

func cloneGraphView(view knowledgeStore.SavedGraphView) knowledgeStore.SavedGraphView {
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

func (s *Store) ListGraphViews(ctx context.Context, owner knowledge.Actor) ([]knowledgeStore.SavedGraphView, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return nil, knowledgeStore.ErrClosed
	}
	ownerKey := knowledgeStore.GraphViewOwnerKey(owner)
	views := make([]knowledgeStore.SavedGraphView, 0)
	for _, view := range s.graphViews {
		if knowledgeStore.GraphViewOwnerKey(view.Owner) == ownerKey {
			views = append(views, cloneGraphView(view))
		}
	}
	slices.SortFunc(views, func(left, right knowledgeStore.SavedGraphView) int {
		return strings.Compare(strings.ToLower(left.Name), strings.ToLower(right.Name))
	})
	return views, nil
}

func (s *Store) GraphView(ctx context.Context, owner knowledge.Actor, id string) (knowledgeStore.SavedGraphView, error) {
	if err := ctx.Err(); err != nil {
		return knowledgeStore.SavedGraphView{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return knowledgeStore.SavedGraphView{}, knowledgeStore.ErrClosed
	}
	view, ok := s.graphViews[graphViewMapKey(owner, id)]
	if !ok {
		return knowledgeStore.SavedGraphView{}, fmt.Errorf("%w: graph view", knowledgeStore.ErrNotFound)
	}
	return cloneGraphView(view), nil
}

func (s *Store) PutGraphView(ctx context.Context, view knowledgeStore.SavedGraphView, expectedRevision uint64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := view.Validate(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return knowledgeStore.ErrClosed
	}
	key := graphViewMapKey(view.Owner, view.ID)
	current, exists := s.graphViews[key]
	if (!exists && (expectedRevision != 0 || view.Revision != 1)) || (exists && (current.Revision != expectedRevision || view.Revision != expectedRevision+1)) {
		return fmt.Errorf("%w: graph view", knowledgeStore.ErrConflict)
	}
	ownerKey := knowledgeStore.GraphViewOwnerKey(view.Owner)
	for otherKey, other := range s.graphViews {
		if otherKey != key && knowledgeStore.GraphViewOwnerKey(other.Owner) == ownerKey && strings.EqualFold(strings.TrimSpace(other.Name), strings.TrimSpace(view.Name)) {
			return fmt.Errorf("%w: graph view name", knowledgeStore.ErrConflict)
		}
	}
	s.graphViews[key] = cloneGraphView(view)
	return nil
}

func (s *Store) DeleteGraphView(ctx context.Context, owner knowledge.Actor, id string, expectedRevision uint64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return knowledgeStore.ErrClosed
	}
	key := graphViewMapKey(owner, id)
	current, exists := s.graphViews[key]
	if !exists {
		return fmt.Errorf("%w: graph view", knowledgeStore.ErrNotFound)
	}
	if expectedRevision == 0 || current.Revision != expectedRevision {
		return fmt.Errorf("%w: graph view", knowledgeStore.ErrConflict)
	}
	delete(s.graphViews, key)
	return nil
}
