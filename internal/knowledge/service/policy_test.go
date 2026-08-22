package service

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/lkarlslund/koder/internal/knowledge"
	"github.com/lkarlslund/koder/internal/knowledge/store/memory"
)

func TestCrossChunkLinkChecksEveryOwningChunkPolicy(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := memory.New()
	t.Cleanup(func() { _ = store.Close() })
	service := newTestService(t, store, nil)
	first, _ := service.CreateChunk(ctx, CreateChunkRequest{Chunk: testChunkCandidate()})
	secondCandidate := testChunkCandidate()
	secondCandidate.Title = "Second"
	second, _ := service.CreateChunk(ctx, CreateChunkRequest{Chunk: secondCandidate})
	entry, _ := service.CreateEntry(ctx, CreateEntryRequest{ChunkID: first.Chunk.ID, Entry: testEntryCandidate()})

	var checked []knowledge.ChunkID
	service.chunkPolicy = ChunkPolicyFunc(func(_ context.Context, _ knowledge.Actor, action ChunkPolicyAction, chunk knowledge.Chunk) error {
		if action != ChunkPolicyLinkCreate {
			t.Fatalf("policy action = %q", action)
		}
		checked = append(checked, chunk.ID)
		if chunk.ID == second.Chunk.ID {
			return fmt.Errorf("fixture denies target")
		}
		return nil
	})
	_, err := service.CreateLink(ctx, CreateLinkRequest{Link: knowledge.Link{
		Source: knowledge.ObjectRef{Kind: knowledge.ObjectKindEntry, ID: string(entry.Entry.ID)},
		Target: knowledge.ObjectRef{Kind: knowledge.ObjectKindChunk, ID: string(second.Chunk.ID)},
		Kind:   knowledge.LinkKindRelatedTo,
	}})
	if !errors.Is(err, ErrChunkPolicyDenied) {
		t.Fatalf("CreateLink() error = %v, want ErrChunkPolicyDenied", err)
	}
	if len(checked) != 2 || checked[0] != first.Chunk.ID || checked[1] != second.Chunk.ID {
		t.Fatalf("checked chunks = %v", checked)
	}
}

func TestLinkCreateAndRestoreRequireActiveEndpointChunks(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := memory.New()
	t.Cleanup(func() { _ = store.Close() })
	service := newTestService(t, store, nil)
	first, _ := service.CreateChunk(ctx, CreateChunkRequest{Chunk: testChunkCandidate()})
	secondCandidate := testChunkCandidate()
	secondCandidate.Title = "Second"
	second, _ := service.CreateChunk(ctx, CreateChunkRequest{Chunk: secondCandidate})
	archived, err := service.ArchiveChunk(ctx, ChunkLifecycleRequest{ChunkID: second.Chunk.ID, ExpectedRevision: 1})
	if err != nil {
		t.Fatalf("ArchiveChunk(): %v", err)
	}
	_, err = service.CreateLink(ctx, CreateLinkRequest{Link: knowledge.Link{
		Source: knowledge.ObjectRef{Kind: knowledge.ObjectKindChunk, ID: string(first.Chunk.ID)},
		Target: knowledge.ObjectRef{Kind: knowledge.ObjectKindChunk, ID: string(archived.Chunk.ID)},
		Kind:   knowledge.LinkKindRelatedTo,
	}})
	if !errors.Is(err, ErrLinkEndpointUnavailable) {
		t.Fatalf("CreateLink(archived endpoint) error = %v, want ErrLinkEndpointUnavailable", err)
	}
}

func TestUnlinkChecksBothPoliciesButAllowsArchivedEndpointCleanup(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := memory.New()
	t.Cleanup(func() { _ = store.Close() })
	service := newTestService(t, store, nil)
	created := createServiceLinkFixture(t, ctx, service)
	if _, err := service.ArchiveChunk(ctx, ChunkLifecycleRequest{
		ChunkID: knowledge.ChunkID(created.Target.ID), ExpectedRevision: 1,
	}); err != nil {
		t.Fatalf("ArchiveChunk(endpoint): %v", err)
	}
	var calls int
	service.chunkPolicy = ChunkPolicyFunc(func(_ context.Context, _ knowledge.Actor, action ChunkPolicyAction, _ knowledge.Chunk) error {
		calls++
		if action != ChunkPolicyLinkUnlink {
			t.Fatalf("policy action = %q", action)
		}
		return nil
	})
	if _, err := service.Unlink(ctx, LinkLifecycleRequest{LinkID: created.ID, ExpectedRevision: 1}); err != nil {
		t.Fatalf("Unlink(): %v", err)
	}
	if calls != 1 {
		t.Fatalf("same-chunk policy calls = %d, want 1", calls)
	}
}

func TestLinkPolicyRunsBeforeDuplicateAndNoOpDetails(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := memory.New()
	t.Cleanup(func() { _ = store.Close() })
	service := newTestService(t, store, nil)
	created := createServiceLinkFixture(t, ctx, service)

	service.chunkPolicy = denyChunkAction(ChunkPolicyLinkCreate)
	_, err := service.CreateLink(ctx, CreateLinkRequest{Link: knowledge.Link{
		Source: created.Source, Target: created.Target, Kind: created.Kind,
	}})
	var duplicate *DuplicateLinkError
	if !errors.Is(err, ErrChunkPolicyDenied) || errors.As(err, &duplicate) {
		t.Fatalf("denied duplicate create error = %v, duplicate=%#v", err, duplicate)
	}

	service.chunkPolicy = denyChunkAction(ChunkPolicyLinkRestore)
	if _, err := service.RestoreLink(ctx, LinkLifecycleRequest{
		LinkID: created.ID, ExpectedRevision: created.Revision.Number,
	}); !errors.Is(err, ErrChunkPolicyDenied) {
		t.Fatalf("denied no-op restore error = %v, want ErrChunkPolicyDenied", err)
	}
}

func TestChunkMutationsEnforceChunkPolicy(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		action ChunkPolicyAction
		run    func(context.Context, *Service, knowledge.Chunk) error
	}{
		{
			name: "create", action: ChunkPolicyCreate,
			run: func(ctx context.Context, service *Service, _ knowledge.Chunk) error {
				_, err := service.CreateChunk(ctx, CreateChunkRequest{Chunk: testChunkCandidate()})
				return err
			},
		},
		{
			name: "update", action: ChunkPolicyUpdate,
			run: func(ctx context.Context, service *Service, chunk knowledge.Chunk) error {
				content := ChunkContentFrom(chunk)
				content.Title = "Denied update"
				_, err := service.UpdateChunk(ctx, UpdateChunkRequest{ChunkID: chunk.ID, ExpectedRevision: chunk.Revision.Number, Content: content})
				return err
			},
		},
		{
			name: "archive", action: ChunkPolicyArchive,
			run: func(ctx context.Context, service *Service, chunk knowledge.Chunk) error {
				_, err := service.ArchiveChunk(ctx, ChunkLifecycleRequest{ChunkID: chunk.ID, ExpectedRevision: chunk.Revision.Number})
				return err
			},
		},
		{
			name: "restore", action: ChunkPolicyRestore,
			run: func(ctx context.Context, service *Service, chunk knowledge.Chunk) error {
				service.chunkPolicy = AllowAllChunkPolicy{}
				archived, err := service.ArchiveChunk(ctx, ChunkLifecycleRequest{ChunkID: chunk.ID, ExpectedRevision: chunk.Revision.Number})
				if err != nil {
					return err
				}
				service.chunkPolicy = denyChunkAction(ChunkPolicyRestore)
				_, err = service.RestoreChunk(ctx, ChunkLifecycleRequest{ChunkID: chunk.ID, ExpectedRevision: archived.Chunk.Revision.Number})
				return err
			},
		},
		{
			name: "delete", action: ChunkPolicyDelete,
			run: func(ctx context.Context, service *Service, chunk knowledge.Chunk) error {
				service.chunkPolicy = AllowAllChunkPolicy{}
				archived, err := service.ArchiveChunk(ctx, ChunkLifecycleRequest{ChunkID: chunk.ID, ExpectedRevision: chunk.Revision.Number})
				if err != nil {
					return err
				}
				service.chunkPolicy = denyChunkAction(ChunkPolicyDelete)
				return service.DeleteChunk(ctx, DeleteChunkRequest{ChunkID: chunk.ID, ExpectedRevision: archived.Chunk.Revision.Number, Confirmed: true})
			},
		},
		{
			name: "cascade delete", action: ChunkPolicyCascadeDelete,
			run: func(ctx context.Context, service *Service, chunk knowledge.Chunk) error {
				service.chunkPolicy = AllowAllChunkPolicy{}
				archived, err := service.ArchiveChunk(ctx, ChunkLifecycleRequest{ChunkID: chunk.ID, ExpectedRevision: chunk.Revision.Number})
				if err != nil {
					return err
				}
				service.chunkPolicy = denyChunkAction(ChunkPolicyCascadeDelete)
				_, err = service.CascadeDeleteChunk(ctx, DeleteChunkRequest{ChunkID: chunk.ID, ExpectedRevision: archived.Chunk.Revision.Number, Confirmed: true})
				return err
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			store := memory.New()
			t.Cleanup(func() { _ = store.Close() })
			service := newTestService(t, store, nil)
			var chunk knowledge.Chunk
			if test.action != ChunkPolicyCreate {
				created, err := service.CreateChunk(ctx, CreateChunkRequest{Chunk: testChunkCandidate()})
				if err != nil {
					t.Fatalf("seed chunk: %v", err)
				}
				chunk = created.Chunk
			}
			service.chunkPolicy = denyChunkAction(test.action)
			if err := test.run(ctx, service, chunk); !errors.Is(err, ErrChunkPolicyDenied) {
				t.Fatalf("mutation error = %v, want ErrChunkPolicyDenied", err)
			}
		})
	}
}

func TestEntryMutationsEnforceOwningChunkPolicy(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		action ChunkPolicyAction
		run    func(context.Context, *Service, knowledge.Chunk, knowledge.Entry) error
	}{
		{
			name: "create", action: ChunkPolicyEntryCreate,
			run: func(ctx context.Context, service *Service, chunk knowledge.Chunk, _ knowledge.Entry) error {
				_, err := service.CreateEntry(ctx, CreateEntryRequest{ChunkID: chunk.ID, Entry: testEntryCandidate()})
				return err
			},
		},
		{
			name: "update", action: ChunkPolicyEntryUpdate,
			run: func(ctx context.Context, service *Service, _ knowledge.Chunk, entry knowledge.Entry) error {
				content := EntryContentFrom(entry)
				content.Title = "Denied update"
				_, err := service.UpdateEntry(ctx, UpdateEntryRequest{EntryID: entry.ID, ExpectedRevision: entry.Revision.Number, Content: content})
				return err
			},
		},
		{
			name: "archive", action: ChunkPolicyEntryArchive,
			run: func(ctx context.Context, service *Service, _ knowledge.Chunk, entry knowledge.Entry) error {
				_, err := service.ArchiveEntry(ctx, EntryLifecycleRequest{EntryID: entry.ID, ExpectedRevision: entry.Revision.Number})
				return err
			},
		},
		{
			name: "restore", action: ChunkPolicyEntryRestore,
			run: func(ctx context.Context, service *Service, _ knowledge.Chunk, entry knowledge.Entry) error {
				service.chunkPolicy = AllowAllChunkPolicy{}
				archived, err := service.ArchiveEntry(ctx, EntryLifecycleRequest{EntryID: entry.ID, ExpectedRevision: entry.Revision.Number})
				if err != nil {
					return err
				}
				service.chunkPolicy = denyChunkAction(ChunkPolicyEntryRestore)
				_, err = service.RestoreEntry(ctx, EntryLifecycleRequest{EntryID: entry.ID, ExpectedRevision: archived.Entry.Revision.Number})
				return err
			},
		},
		{
			name: "verify", action: ChunkPolicyEntryVerify,
			run: func(ctx context.Context, service *Service, _ knowledge.Chunk, entry knowledge.Entry) error {
				_, err := service.VerifyEntry(ctx, VerifyEntryRequest{
					EntryID: entry.ID, ExpectedRevision: entry.Revision.Number, Status: knowledge.VerificationStatusUnverified,
				})
				return err
			},
		},
		{
			name: "supersede", action: ChunkPolicyEntrySupersede,
			run: func(ctx context.Context, service *Service, chunk knowledge.Chunk, entry knowledge.Entry) error {
				service.chunkPolicy = AllowAllChunkPolicy{}
				replacementCandidate := testEntryCandidate()
				replacementCandidate.Title = "Replacement"
				replacement, err := service.CreateEntry(ctx, CreateEntryRequest{ChunkID: chunk.ID, Entry: replacementCandidate})
				if err != nil {
					return err
				}
				service.chunkPolicy = denyChunkAction(ChunkPolicyEntrySupersede)
				_, err = service.SupersedeEntry(ctx, SupersedeEntryRequest{
					EntryID: entry.ID, ExpectedRevision: entry.Revision.Number, ReplacementEntryID: replacement.Entry.ID,
				})
				return err
			},
		},
		{
			name: "delete", action: ChunkPolicyEntryDelete,
			run: func(ctx context.Context, service *Service, _ knowledge.Chunk, entry knowledge.Entry) error {
				service.chunkPolicy = AllowAllChunkPolicy{}
				archived, err := service.ArchiveEntry(ctx, EntryLifecycleRequest{EntryID: entry.ID, ExpectedRevision: entry.Revision.Number})
				if err != nil {
					return err
				}
				service.chunkPolicy = denyChunkAction(ChunkPolicyEntryDelete)
				return service.DeleteEntry(ctx, DeleteEntryRequest{
					EntryID: entry.ID, ExpectedRevision: archived.Entry.Revision.Number, Confirmed: true,
				})
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			store := memory.New()
			t.Cleanup(func() { _ = store.Close() })
			service := newTestService(t, store, nil)
			createdChunk, err := service.CreateChunk(ctx, CreateChunkRequest{Chunk: testChunkCandidate()})
			if err != nil {
				t.Fatalf("seed chunk: %v", err)
			}
			var entry knowledge.Entry
			if test.action != ChunkPolicyEntryCreate {
				createdEntry, err := service.CreateEntry(ctx, CreateEntryRequest{ChunkID: createdChunk.Chunk.ID, Entry: testEntryCandidate()})
				if err != nil {
					t.Fatalf("seed entry: %v", err)
				}
				entry = createdEntry.Entry
			}
			service.chunkPolicy = denyChunkAction(test.action)
			if err := test.run(ctx, service, createdChunk.Chunk, entry); !errors.Is(err, ErrChunkPolicyDenied) {
				t.Fatalf("mutation error = %v, want ErrChunkPolicyDenied", err)
			}
		})
	}
}

func TestChunkUpdateAuthorizesDestinationScope(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := memory.New()
	t.Cleanup(func() { _ = store.Close() })
	service := newTestService(t, store, nil)
	created, err := service.CreateChunk(ctx, CreateChunkRequest{Chunk: testChunkCandidate()})
	if err != nil {
		t.Fatalf("seed chunk: %v", err)
	}
	service.chunkPolicy = ChunkPolicyFunc(func(_ context.Context, _ knowledge.Actor, action ChunkPolicyAction, chunk knowledge.Chunk) error {
		if action == ChunkPolicyUpdate && chunk.Scope.Kind == knowledge.ScopeKindProject {
			return errors.New("project scope denied")
		}
		return nil
	})
	content := ChunkContentFrom(created.Chunk)
	content.Scope = knowledge.Scope{Kind: knowledge.ScopeKindProject, Selector: "secret-project"}
	_, err = service.UpdateChunk(ctx, UpdateChunkRequest{ChunkID: created.Chunk.ID, ExpectedRevision: 1, Content: content})
	if !errors.Is(err, ErrChunkPolicyDenied) {
		t.Fatalf("UpdateChunk() error = %v, want destination policy denial", err)
	}
	got, err := service.Chunk(ctx, created.Chunk.ID)
	if err != nil || got.Scope != created.Chunk.Scope || got.Revision.Number != 1 {
		t.Fatalf("chunk changed after denied scope move: chunk=%#v err=%v", got, err)
	}
}

func TestCascadeDeleteAuthorizesEveryTouchedChunkBeforeMutation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := memory.New()
	t.Cleanup(func() { _ = store.Close() })
	fixture := seedCascadeFixture(t, ctx, store)
	service := cascadeTestService(t, store, "01a01688-fc5d-7f7d-8bb8-de244977f8af")
	var checked []knowledge.ChunkID
	service.chunkPolicy = ChunkPolicyFunc(func(_ context.Context, _ knowledge.Actor, action ChunkPolicyAction, chunk knowledge.Chunk) error {
		if action != ChunkPolicyCascadeDelete {
			t.Fatalf("policy action = %q", action)
		}
		checked = append(checked, chunk.ID)
		if chunk.ID == fixture.dependent.ID {
			return errors.New("dependent chunk denied")
		}
		return nil
	})
	_, err := service.CascadeDeleteChunk(ctx, DeleteChunkRequest{
		ChunkID: fixture.root.ID, ExpectedRevision: 1, Confirmed: true,
	})
	if !errors.Is(err, ErrChunkPolicyDenied) {
		t.Fatalf("CascadeDeleteChunk() error = %v, want ErrChunkPolicyDenied", err)
	}
	if len(checked) != 2 || checked[0] != fixture.root.ID || checked[1] != fixture.dependent.ID {
		t.Fatalf("authorized chunks = %v", checked)
	}
	service.chunkPolicy = AllowAllChunkPolicy{}
	if _, err := service.Chunk(ctx, fixture.root.ID); err != nil {
		t.Fatalf("root changed after denied cascade: %v", err)
	}
	if _, err := service.Chunk(ctx, fixture.dependent.ID); err != nil {
		t.Fatalf("dependent changed after denied cascade: %v", err)
	}
}

func denyChunkAction(denied ChunkPolicyAction) ChunkPolicy {
	return ChunkPolicyFunc(func(_ context.Context, _ knowledge.Actor, action ChunkPolicyAction, _ knowledge.Chunk) error {
		if action == denied {
			return errors.New("denied by test policy")
		}
		return nil
	})
}
