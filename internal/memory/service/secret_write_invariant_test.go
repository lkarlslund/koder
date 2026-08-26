package service

import (
	"context"
	"errors"
	"testing"

	"github.com/lkarlslund/koder/internal/memory"
	memoryStoreAPI "github.com/lkarlslund/koder/internal/memory/store"
	memoryPebble "github.com/lkarlslund/koder/internal/memory/store/pebble"
)

type updateTrackingStore struct {
	memoryStoreAPI.Store
	updates int
}

func (s *updateTrackingStore) Update(ctx context.Context, fn func(memoryStoreAPI.WriteTx) error) error {
	s.updates++
	return s.Store.Update(ctx, fn)
}

func TestSecretRejectionNeverEntersPebbleWritePath(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	durable, err := memoryPebble.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open(): %v", err)
	}
	t.Cleanup(func() { _ = durable.Close() })
	tracked := &updateTrackingStore{Store: durable}
	service := newTestService(t, tracked, nil)
	parent, err := service.CreateChunk(ctx, CreateChunkRequest{Chunk: testChunkCandidate()})
	if err != nil {
		t.Fatalf("create safe chunk: %v", err)
	}
	entry, err := service.CreateEntry(ctx, CreateEntryRequest{ChunkID: parent.Chunk.ID, Entry: testEntryCandidate()})
	if err != nil {
		t.Fatalf("create safe entry: %v", err)
	}
	tracked.updates = 0

	secretChunk := testChunkCandidate()
	secretChunk.Description = "api_key=extremely-secret-value"
	chunkUpdate := ChunkContentFrom(parent.Chunk)
	chunkUpdate.Description = secretChunk.Description
	secretEntry := testEntryCandidate()
	secretEntry.Body = "password=extremely-secret-value"
	entryUpdate := EntryContentFrom(entry.Entry)
	entryUpdate.Applicability.Software = []memory.SoftwareConstraint{{
		Name: "tool", VersionRange: "password=extremely-secret-value",
	}}
	secretEvidence := testEvidenceCandidate()
	secretEvidence.Source.Excerpt = "api_key=extremely-secret-value"

	attempts := map[string]func() error{
		"create chunk": func() error {
			_, err := service.CreateChunk(ctx, CreateChunkRequest{Chunk: secretChunk, ReviewApproved: true})
			return err
		},
		"update chunk": func() error {
			_, err := service.UpdateChunk(ctx, UpdateChunkRequest{
				ChunkID: parent.Chunk.ID, ExpectedRevision: 1, Content: chunkUpdate, ReviewApproved: true,
			})
			return err
		},
		"create entry": func() error {
			_, err := service.CreateEntry(ctx, CreateEntryRequest{
				ChunkID: parent.Chunk.ID, Entry: secretEntry, ReviewApproved: true,
			})
			return err
		},
		"update entry": func() error {
			_, err := service.UpdateEntry(ctx, UpdateEntryRequest{
				EntryID: entry.Entry.ID, ExpectedRevision: 1, Content: entryUpdate, ReviewApproved: true,
			})
			return err
		},
		"create evidence": func() error {
			_, err := service.CreateEvidence(ctx, CreateEvidenceRequest{Evidence: secretEvidence, ReviewApproved: true})
			return err
		},
	}
	for name, attempt := range attempts {
		t.Run(name, func(t *testing.T) {
			if err := attempt(); !errors.Is(err, ErrClassificationRejected) {
				t.Fatalf("secret write error = %v, want ErrClassificationRejected", err)
			}
		})
	}
	if tracked.updates != 0 {
		t.Fatalf("rejected writes entered Store.Update %d times", tracked.updates)
	}

	stats, err := durable.ScanCanonical(ctx, func(memoryStoreAPI.CanonicalRecord) error { return nil })
	if err != nil {
		t.Fatalf("ScanCanonical(): %v", err)
	}
	if stats.Chunks != 1 || stats.Entries != 1 || stats.Links != 0 || stats.Evidence != 0 {
		t.Fatalf("canonical records changed after rejected writes: %#v", stats)
	}
	for _, object := range []memory.ObjectRef{
		{Kind: memory.ObjectKindChunk, ID: string(parent.Chunk.ID)},
		{Kind: memory.ObjectKindEntry, ID: string(entry.Entry.ID)},
	} {
		page, err := durable.ListRevisions(ctx, memoryStoreAPI.RevisionListRequest{Object: object})
		if err != nil || len(page.Revisions) != 1 {
			t.Fatalf("revision history for %v changed: %#v, %v", object, page, err)
		}
	}
}
