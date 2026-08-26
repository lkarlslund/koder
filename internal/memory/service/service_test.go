package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lkarlslund/koder/internal/memory"
	memoryStoreAPI "github.com/lkarlslund/koder/internal/memory/store"
	memoryBackend "github.com/lkarlslund/koder/internal/memory/store/memory"
)

var serviceTime = time.Date(2026, 8, 22, 17, 0, 0, 123456789, time.UTC)

type recordingClassifier struct {
	input  memory.ClassificationInput
	result memory.ClassificationResult
	err    error
	calls  int
}

func (c *recordingClassifier) Classify(_ context.Context, input memory.ClassificationInput) (memory.ClassificationResult, error) {
	c.calls++
	c.input = input
	return c.result, c.err
}

func TestCreateAndGetChunkCanonicalizesAndAssignsServerFields(t *testing.T) {
	t.Parallel()
	store := memoryBackend.New()
	t.Cleanup(func() { _ = store.Close() })
	classifier := &recordingClassifier{result: memory.ClassificationResult{Decision: memory.ClassificationDecisionAllow}}
	service := newTestService(t, store, classifier)
	candidate := testChunkCandidate()
	candidate.Title = "  Go\u0308   tools  "
	candidate.Aliases = []string{" GÖ tools ", "CLI", "cli"}
	candidate.Tags = []string{" Linux Tools ", "linux-tools"}
	candidate.Locale = "da_dk"

	result, err := service.CreateChunk(context.Background(), CreateChunkRequest{Chunk: candidate})
	if err != nil {
		t.Fatalf("CreateChunk() error = %v", err)
	}
	chunk := result.Chunk
	if chunk.ID != "01a01688-fc5d-7f7d-8bb8-de244977f8a1" || chunk.Revision.ID != "01a01688-fc5d-7f7d-8bb8-de244977f8a2" || chunk.Revision.Number != 1 {
		t.Fatalf("assigned identity/revision = %#v", chunk)
	}
	if chunk.Title != "Gö tools" || len(chunk.Aliases) != 1 || chunk.Aliases[0] != "CLI" || len(chunk.Tags) != 1 || chunk.Tags[0] != "linux-tools" || chunk.Locale != "da-DK" {
		t.Fatalf("canonical chunk text = %#v", chunk)
	}
	if chunk.State != memory.ChunkStateActive || chunk.Visibility != memory.VisibilityPrivate || chunk.SchemaVersion != 1 || !chunk.CreatedAt.Equal(serviceTime) || !chunk.UpdatedAt.Equal(serviceTime) {
		t.Fatalf("canonical defaults = %#v", chunk)
	}
	if classifier.calls != 1 || classificationField(classifier.input, "title") != "  Go\u0308   tools  " {
		t.Fatalf("classifier did not receive raw candidate: %#v", classifier.input)
	}
	got, err := service.Chunk(context.Background(), chunk.ID)
	if err != nil {
		t.Fatalf("Chunk() error = %v", err)
	}
	if got.Title != chunk.Title || got.Revision != chunk.Revision {
		t.Fatalf("Chunk() = %#v, want %#v", got, chunk)
	}
}

func TestCreateChunkRejectsSecretsBeforePersistence(t *testing.T) {
	t.Parallel()
	store := memoryBackend.New()
	t.Cleanup(func() { _ = store.Close() })
	service := newTestService(t, store, nil)
	candidate := testChunkCandidate()
	candidate.Description = "api_key=super-secret-credential"
	result, err := service.CreateChunk(context.Background(), CreateChunkRequest{Chunk: candidate})
	if !errors.Is(err, ErrClassificationRejected) || result.Classification.Decision != memory.ClassificationDecisionReject {
		t.Fatalf("CreateChunk() = %#v, %v, want classification rejection", result, err)
	}
	assertNoChunks(t, store)
}

func TestCreateChunkRequiresExplicitSensitiveDataReview(t *testing.T) {
	t.Parallel()
	store := memoryBackend.New()
	t.Cleanup(func() { _ = store.Close() })
	service := newTestService(t, store, nil)
	candidate := testChunkCandidate()
	candidate.Description = "Contact person@example.dk for details"
	result, err := service.CreateChunk(context.Background(), CreateChunkRequest{Chunk: candidate})
	if !errors.Is(err, ErrReviewRequired) || result.Classification.Decision != memory.ClassificationDecisionReview {
		t.Fatalf("CreateChunk() = %#v, %v, want review", result, err)
	}
	assertNoChunks(t, store)
	if _, err := service.CreateChunk(context.Background(), CreateChunkRequest{Chunk: candidate, ReviewApproved: true}); err != nil {
		t.Fatalf("CreateChunk(review approved) error = %v", err)
	}
}

func TestCreateChunkRejectsInvalidOrServerOwnedCandidate(t *testing.T) {
	t.Parallel()
	store := memoryBackend.New()
	t.Cleanup(func() { _ = store.Close() })
	classifier := &recordingClassifier{result: memory.ClassificationResult{Decision: memory.ClassificationDecisionAllow}}
	service := newTestService(t, store, classifier)

	serverOwned := testChunkCandidate()
	serverOwned.ID = "01a01688-fc5d-7f7d-8bb8-de244977f8a1"
	if _, err := service.CreateChunk(context.Background(), CreateChunkRequest{Chunk: serverOwned}); !errors.Is(err, memory.ErrInvalidRecord) {
		t.Fatalf("CreateChunk(server fields) error = %v, want ErrInvalidRecord", err)
	}
	if classifier.calls != 0 {
		t.Fatalf("server-owned candidate reached classifier %d times", classifier.calls)
	}

	invalid := testChunkCandidate()
	invalid.Kind = memory.ChunkKindUnspecified
	if _, err := service.CreateChunk(context.Background(), CreateChunkRequest{Chunk: invalid}); !errors.Is(err, memory.ErrInvalidRecord) {
		t.Fatalf("CreateChunk(invalid) error = %v, want ErrInvalidRecord", err)
	}
	assertNoChunks(t, store)
}

func TestChunkGetPreservesStoreErrorsAndCancellation(t *testing.T) {
	t.Parallel()
	store := memoryBackend.New()
	t.Cleanup(func() { _ = store.Close() })
	service := newTestService(t, store, nil)
	if _, err := service.Chunk(context.Background(), "01a01688-fc5d-7f7d-8bb8-de244977f8a1"); !errors.Is(err, memoryStoreAPI.ErrNotFound) {
		t.Fatalf("Chunk(missing) error = %v, want ErrNotFound", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := service.Chunk(ctx, "unused"); !errors.Is(err, context.Canceled) {
		t.Fatalf("Chunk(canceled) error = %v, want context.Canceled", err)
	}
}

func newTestService(t *testing.T, store memoryStoreAPI.Store, classifier memory.Classifier) *Service {
	t.Helper()
	ids := []string{
		"01a01688-fc5d-7f7d-8bb8-de244977f8a1",
		"01a01688-fc5d-7f7d-8bb8-de244977f8a2",
		"01a01688-fc5d-7f7d-8bb8-de244977f8a3",
		"01a01688-fc5d-7f7d-8bb8-de244977f8a4",
		"01a01688-fc5d-7f7d-8bb8-de244977f8a5",
		"01a01688-fc5d-7f7d-8bb8-de244977f8a6",
		"01a01688-fc5d-7f7d-8bb8-de244977f8a7",
		"01a01688-fc5d-7f7d-8bb8-de244977f8a8",
		"01a01688-fc5d-7f7d-8bb8-de244977f8a9",
		"01a01688-fc5d-7f7d-8bb8-de244977f8aa",
		"01a01688-fc5d-7f7d-8bb8-de244977f8ab",
		"01a01688-fc5d-7f7d-8bb8-de244977f8ac",
	}
	service, err := New(Config{
		Store: store, Classifier: classifier,
		Actor: func(context.Context) (memory.Actor, error) {
			return memory.Actor{Kind: memory.ActorKindUser, ID: "user:test"}, nil
		},
		Now: func() time.Time { return serviceTime },
		NewID: func() string {
			value := ids[0]
			ids = ids[1:]
			return value
		},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return service
}

func testChunkCandidate() memory.Chunk {
	return memory.Chunk{
		Title: "Test chunk", Kind: memory.ChunkKindReference,
		Scope: memory.Scope{Kind: memory.ScopeKindGlobal},
	}
}

func classificationField(input memory.ClassificationInput, name string) string {
	for _, field := range input.Fields {
		if field.Name == name {
			return field.Value
		}
	}
	return ""
}

func assertNoChunks(t *testing.T, store *memoryBackend.Store) {
	t.Helper()
	stats, err := store.ScanCanonical(context.Background(), func(memoryStoreAPI.CanonicalRecord) error { return nil })
	if err != nil {
		t.Fatalf("ScanCanonical() error = %v", err)
	}
	if stats.Chunks != 0 {
		t.Fatalf("store contains %d chunks after rejected create", stats.Chunks)
	}
}
