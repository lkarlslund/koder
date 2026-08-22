package pebble

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lkarlslund/koder/internal/knowledge"
	knowledgeStore "github.com/lkarlslund/koder/internal/knowledge/store"
)

func TestGraphViewsPersistOutsideCanonicalRecords(t *testing.T) {
	t.Parallel()
	stateDir := t.TempDir()
	owner := knowledge.Actor{Kind: knowledge.ActorKindUser, ID: "browser:test"}
	now := time.Date(2026, 8, 22, 10, 0, 0, 0, time.UTC)
	view := knowledgeStore.SavedGraphView{
		ID: "01a01688-fc5d-7f7d-8bb8-de244977f8a1", Owner: owner, Name: "My graph",
		State:    knowledgeStore.GraphViewState{Layout: "force_atlas2", MobilePane: "graph"},
		Revision: 1, CreatedAt: now, UpdatedAt: now,
	}
	first, err := Open(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.PutGraphView(context.Background(), view, 0); err != nil {
		t.Fatalf("PutGraphView() error = %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	second, err := Open(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = second.Close() })
	got, err := second.GraphView(context.Background(), owner, view.ID)
	if err != nil || got.Name != view.Name || got.Revision != 1 {
		t.Fatalf("GraphView() = %#v, %v", got, err)
	}
	views, err := second.ListGraphViews(context.Background(), owner)
	if err != nil || len(views) != 1 {
		t.Fatalf("ListGraphViews() = %#v, %v", views, err)
	}
	var canonical int
	if err := second.View(context.Background(), func(tx knowledgeStore.ReadTx) error {
		for _, id := range []knowledge.ChunkID{knowledge.ChunkID(view.ID)} {
			if _, err := tx.Chunk(context.Background(), id); !errors.Is(err, knowledgeStore.ErrNotFound) {
				canonical++
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if canonical != 0 {
		t.Fatal("saved graph view appeared as canonical Knowledge")
	}
	if err := second.DeleteGraphView(context.Background(), owner, view.ID, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := second.GraphView(context.Background(), owner, view.ID); !errors.Is(err, knowledgeStore.ErrNotFound) {
		t.Fatalf("GraphView(deleted) error = %v", err)
	}
}
