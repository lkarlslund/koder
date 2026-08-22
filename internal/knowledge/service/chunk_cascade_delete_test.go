package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lkarlslund/koder/internal/knowledge"
	knowledgeStore "github.com/lkarlslund/koder/internal/knowledge/store"
	"github.com/lkarlslund/koder/internal/knowledge/store/memory"
)

type cascadeFixture struct {
	root       knowledge.Chunk
	dependent  knowledge.Chunk
	entry      knowledge.Entry
	link       knowledge.Link
	evidence   knowledge.Evidence
	dependency knowledge.ChunkID
}

func TestCascadeDeleteChunkAtomicallyRemovesOwnedGraphAndRepairsDependents(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := memory.New()
	t.Cleanup(func() { _ = store.Close() })
	fixture := seedCascadeFixture(t, ctx, store)
	service := cascadeTestService(t, store, "01a01688-fc5d-7f7d-8bb8-de244977f8af")
	result, err := service.CascadeDeleteChunk(ctx, DeleteChunkRequest{
		ChunkID: fixture.root.ID, ExpectedRevision: 1, Confirmed: true,
	})
	if err != nil {
		t.Fatalf("CascadeDeleteChunk() error = %v", err)
	}
	if len(result.DeletedEntryIDs) != 1 || result.DeletedEntryIDs[0] != fixture.entry.ID ||
		len(result.DeletedLinkIDs) != 1 || result.DeletedLinkIDs[0] != fixture.link.ID ||
		len(result.DeletedEvidenceIDs) != 1 || result.DeletedEvidenceIDs[0] != fixture.evidence.ID ||
		len(result.UpdatedDependentChunkIDs) != 1 || result.UpdatedDependentChunkIDs[0] != fixture.dependent.ID {
		t.Fatalf("CascadeDeleteChunk() = %#v", result)
	}
	if err := store.View(ctx, func(tx knowledgeStore.ReadTx) error {
		if _, err := tx.Chunk(ctx, fixture.root.ID); !errors.Is(err, knowledgeStore.ErrNotFound) {
			t.Errorf("root lookup error = %v, want ErrNotFound", err)
		}
		if _, err := tx.Entry(ctx, fixture.entry.ID); !errors.Is(err, knowledgeStore.ErrNotFound) {
			t.Errorf("entry lookup error = %v, want ErrNotFound", err)
		}
		if _, err := tx.Link(ctx, fixture.link.ID); !errors.Is(err, knowledgeStore.ErrNotFound) {
			t.Errorf("link lookup error = %v, want ErrNotFound", err)
		}
		if _, err := tx.Evidence(ctx, fixture.evidence.ID); !errors.Is(err, knowledgeStore.ErrNotFound) {
			t.Errorf("evidence lookup error = %v, want ErrNotFound", err)
		}
		dependent, err := tx.Chunk(ctx, fixture.dependent.ID)
		if err != nil {
			return err
		}
		if dependent.Revision.Number != 2 || len(dependent.DependencyIDs) != 0 || dependent.Revision.Reason == "" {
			t.Errorf("repaired dependent = %#v", dependent)
		}
		return nil
	}); err != nil {
		t.Fatalf("inspect cascade result: %v", err)
	}
}

func TestCascadeDeleteChunkRollsBackEveryMutationOnDependentFailure(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := memory.New()
	t.Cleanup(func() { _ = store.Close() })
	fixture := seedCascadeFixture(t, ctx, store)
	service := cascadeTestService(t, store, "not-a-uuid")
	if _, err := service.CascadeDeleteChunk(ctx, DeleteChunkRequest{
		ChunkID: fixture.root.ID, ExpectedRevision: 1, Confirmed: true,
	}); !errors.Is(err, knowledge.ErrInvalidRecord) {
		t.Fatalf("CascadeDeleteChunk(invalid revision ID) error = %v, want ErrInvalidRecord", err)
	}
	if err := store.View(ctx, func(tx knowledgeStore.ReadTx) error {
		for name, check := range map[string]func() error{
			"root":     func() error { _, err := tx.Chunk(ctx, fixture.root.ID); return err },
			"entry":    func() error { _, err := tx.Entry(ctx, fixture.entry.ID); return err },
			"link":     func() error { _, err := tx.Link(ctx, fixture.link.ID); return err },
			"evidence": func() error { _, err := tx.Evidence(ctx, fixture.evidence.ID); return err },
		} {
			if err := check(); err != nil {
				t.Errorf("%s missing after rollback: %v", name, err)
			}
		}
		dependent, err := tx.Chunk(ctx, fixture.dependent.ID)
		if err != nil {
			return err
		}
		if dependent.Revision.Number != 1 || len(dependent.DependencyIDs) != 1 || dependent.DependencyIDs[0] != fixture.root.ID {
			t.Errorf("dependent changed despite rollback: %#v", dependent)
		}
		return nil
	}); err != nil {
		t.Fatalf("inspect rollback: %v", err)
	}
}

func seedCascadeFixture(t *testing.T, ctx context.Context, store *memory.Store) cascadeFixture {
	t.Helper()
	revision := func(id knowledge.RevisionID) knowledge.Revision {
		return knowledge.Revision{
			Number: 1, ID: id, Actor: knowledge.Actor{Kind: knowledge.ActorKindUser, ID: "user:test"}, CreatedAt: serviceTime,
		}
	}
	fixture := cascadeFixture{dependency: "01a01688-fc5d-7f7d-8bb8-de244977f8a5"}
	fixture.root = knowledge.Chunk{
		ID: "01a01688-fc5d-7f7d-8bb8-de244977f8a1", Title: "Root", Kind: knowledge.ChunkKindReference,
		Scope: knowledge.Scope{Kind: knowledge.ScopeKindGlobal}, Visibility: knowledge.VisibilityPrivate,
		State: knowledge.ChunkStateArchived, SchemaVersion: 1,
		Revision: revision("01a01688-fc5d-7f7d-8bb8-de244977f8a2"), CreatedAt: serviceTime, UpdatedAt: serviceTime,
		DependencyIDs: []knowledge.ChunkID{fixture.dependency}, Counts: knowledge.ChunkCounts{Entries: 1, Links: 1, Evidence: 1},
	}
	fixture.dependent = fixture.root
	fixture.dependent.ID = "01a01688-fc5d-7f7d-8bb8-de244977f8a3"
	fixture.dependent.Title = "Dependent"
	fixture.dependent.State = knowledge.ChunkStateActive
	fixture.dependent.Revision = revision("01a01688-fc5d-7f7d-8bb8-de244977f8a4")
	fixture.dependent.DependencyIDs = []knowledge.ChunkID{fixture.root.ID}
	fixture.dependent.Counts = knowledge.ChunkCounts{}
	fixture.evidence = knowledge.Evidence{
		ID: "01a01688-fc5d-7f7d-8bb8-de244977f8aa", Type: knowledge.EvidenceTypeObservation,
		Quality: knowledge.EvidenceQualityPrimary, Source: knowledge.Source{ID: "test:owned"},
		Actor: knowledge.Actor{Kind: knowledge.ActorKindUser, ID: "user:test"}, CreatedAt: serviceTime,
	}
	fixture.entry = knowledge.Entry{
		ID: "01a01688-fc5d-7f7d-8bb8-de244977f8a6", ChunkID: fixture.root.ID,
		Kind: knowledge.EntryKindFact, Title: "Fact", Scope: knowledge.Scope{Kind: knowledge.ScopeKindGlobal},
		Verification: knowledge.Verification{Status: knowledge.VerificationStatusUnverified}, State: knowledge.EntryStateActive,
		EvidenceIDs: []knowledge.EvidenceID{fixture.evidence.ID},
		Revision:    revision("01a01688-fc5d-7f7d-8bb8-de244977f8a7"), CreatedAt: serviceTime, UpdatedAt: serviceTime,
	}
	fixture.link = knowledge.Link{
		ID:     "01a01688-fc5d-7f7d-8bb8-de244977f8a8",
		Source: knowledge.ObjectRef{Kind: knowledge.ObjectKindEntry, ID: string(fixture.entry.ID)},
		Target: knowledge.ObjectRef{Kind: knowledge.ObjectKindChunk, ID: string(fixture.dependent.ID)},
		Kind:   knowledge.LinkKindRelatedTo, State: knowledge.LinkStateActive,
		Revision: revision("01a01688-fc5d-7f7d-8bb8-de244977f8a9"), CreatedAt: serviceTime, UpdatedAt: serviceTime,
	}
	if err := store.Update(ctx, func(tx knowledgeStore.WriteTx) error {
		if err := tx.PutChunk(ctx, fixture.root, 0); err != nil {
			return err
		}
		if err := tx.PutChunk(ctx, fixture.dependent, 0); err != nil {
			return err
		}
		if err := tx.PutEntry(ctx, fixture.entry, 0); err != nil {
			return err
		}
		if err := tx.PutLink(ctx, fixture.link, 0); err != nil {
			return err
		}
		return tx.PutEvidence(ctx, fixture.evidence)
	}); err != nil {
		t.Fatalf("seed cascade fixture: %v", err)
	}
	return fixture
}

func cascadeTestService(t *testing.T, store knowledgeStore.Store, revisionID string) *Service {
	t.Helper()
	service, err := New(Config{
		Store: store,
		Actor: func(context.Context) (knowledge.Actor, error) {
			return knowledge.Actor{Kind: knowledge.ActorKindUser, ID: "user:test"}, nil
		},
		Now:   func() time.Time { return serviceTime },
		NewID: func() string { return revisionID },
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return service
}
