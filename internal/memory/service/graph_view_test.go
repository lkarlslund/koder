package service

import (
	"context"
	"errors"
	"testing"

	"github.com/lkarlslund/koder/internal/memory"
	memoryStoreAPI "github.com/lkarlslund/koder/internal/memory/store"
	memoryBackend "github.com/lkarlslund/koder/internal/memory/store/memory"
)

func TestGraphViewLifecycleIsOwnerScopedAndRevisioned(t *testing.T) {
	t.Parallel()
	backend := memoryBackend.New()
	t.Cleanup(func() { _ = backend.Close() })
	service := newTestService(t, backend, nil)
	state := memoryStoreAPI.GraphViewState{
		Browser:     memoryStoreAPI.GraphViewBrowserState{ScopeKind: "project", ObjectKind: "entry", ID: "01a01688-fc5d-7f7d-8bb8-de244977f8a2"},
		Root:        &memory.ObjectRef{Kind: memory.ObjectKindChunk, ID: "01a01688-fc5d-7f7d-8bb8-de244977f8a1"},
		HiddenNodes: []string{"entry:01a01688-fc5d-7f7d-8bb8-de244977f8a2"},
		PinnedNodes: []memoryStoreAPI.GraphViewPin{{Key: "entry:01a01688-fc5d-7f7d-8bb8-de244977f8a2", X: 4, Y: -2}},
		Frontier:    []memoryStoreAPI.GraphViewExpansion{{Kind: "entry", ID: "01a01688-fc5d-7f7d-8bb8-de244977f8a2", Direction: "outgoing"}},
	}
	created, err := service.CreateGraphView(context.Background(), SaveGraphViewRequest{Name: " Disk tools ", State: state})
	if err != nil {
		t.Fatalf("CreateGraphView() error = %v", err)
	}
	if created.Name != "Disk tools" || created.Revision != 1 || created.State.Layout != "force_atlas2" || created.State.MobilePane != "graph" || created.State.Presentation != "canvas" {
		t.Fatalf("CreateGraphView() = %#v", created)
	}
	listed, err := service.ListGraphViews(context.Background())
	if err != nil || len(listed) != 1 || listed[0].ID != created.ID {
		t.Fatalf("ListGraphViews() = %#v, %v", listed, err)
	}
	updated, err := service.UpdateGraphView(context.Background(), created.ID, SaveGraphViewRequest{Name: "Linux disk tools", State: state, ExpectedRevision: 1})
	if err != nil || updated.Revision != 2 || updated.Name != "Linux disk tools" {
		t.Fatalf("UpdateGraphView() = %#v, %v", updated, err)
	}
	if _, err := service.UpdateGraphView(context.Background(), created.ID, SaveGraphViewRequest{Name: "stale", State: state, ExpectedRevision: 1}); !errors.Is(err, memoryStoreAPI.ErrConflict) {
		t.Fatalf("stale UpdateGraphView() error = %v, want conflict", err)
	}
	if _, err := service.CreateGraphView(context.Background(), SaveGraphViewRequest{Name: "linux DISK tools", State: state}); !errors.Is(err, memoryStoreAPI.ErrConflict) {
		t.Fatalf("duplicate CreateGraphView() error = %v, want conflict", err)
	}
	if err := service.DeleteGraphView(context.Background(), created.ID, 2); err != nil {
		t.Fatalf("DeleteGraphView() error = %v", err)
	}
	if _, err := service.GetGraphView(context.Background(), created.ID); !errors.Is(err, memoryStoreAPI.ErrNotFound) {
		t.Fatalf("GetGraphView(deleted) error = %v, want not found", err)
	}
}

func TestGraphViewRejectsUnsafeOrOversizedState(t *testing.T) {
	t.Parallel()
	backend := memoryBackend.New()
	t.Cleanup(func() { _ = backend.Close() })
	service := newTestService(t, backend, nil)
	tests := []memoryStoreAPI.GraphViewState{
		{Browser: memoryStoreAPI.GraphViewBrowserState{Kind: "unknown"}},
		{HiddenNodes: []string{"entry:../secret"}},
		{PinnedNodes: []memoryStoreAPI.GraphViewPin{{Key: "entry:01a01688-fc5d-7f7d-8bb8-de244977f8a2", X: 2e6}}},
		{Frontier: []memoryStoreAPI.GraphViewExpansion{{Kind: "entry", ID: "01a01688-fc5d-7f7d-8bb8-de244977f8a2", Direction: "both"}}},
	}
	for _, state := range tests {
		if _, err := service.CreateGraphView(context.Background(), SaveGraphViewRequest{Name: "Unsafe", State: state}); !errors.Is(err, memory.ErrInvalidRecord) {
			t.Fatalf("CreateGraphView(%#v) error = %v, want invalid record", state, err)
		}
	}
}
