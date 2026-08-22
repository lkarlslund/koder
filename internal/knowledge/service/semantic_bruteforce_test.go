package service

import (
	"context"
	"crypto/sha256"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lkarlslund/koder/internal/knowledge"
)

func TestBruteForceSemanticIndexRebuildsAndFiltersExactRevisions(t *testing.T) {
	t.Parallel()
	backend := &semanticEmbeddingFake{}
	index, err := NewBruteForceSemanticIndex(BruteForceSemanticIndexConfig{
		Backend: backend, DocumentSchema: "knowledge-entry-v1", BatchSize: 2,
		Now: func() time.Time { return time.Date(2026, 8, 22, 18, 0, 0, 0, time.UTC) },
	})
	if err != nil {
		t.Fatalf("NewBruteForceSemanticIndex() error = %v", err)
	}
	status, err := index.Status(context.Background())
	if err != nil || !status.Available || status.Ready || status.Generation != 0 || backend.callCount() != 0 {
		t.Fatalf("initial Status() = %#v, %v; backend calls = %d", status, err, backend.callCount())
	}
	documents := []SemanticDocument{
		semanticTestDocument(semanticEntryA, semanticRevisionA, "a-1", "friendly dog"),
		semanticTestDocument(semanticEntryA, semanticRevisionA, "a-2", "canine companion"),
		semanticTestDocument(semanticEntryB, semanticRevisionB, "b-1", "linux disk partition utility"),
	}
	status, err = index.Rebuild(context.Background(), semanticDocumentSlice(documents))
	if err != nil {
		t.Fatalf("Rebuild() error = %v", err)
	}
	if !status.Ready || !status.Available || status.Generation != 1 || status.Documents != 3 || status.LastRebuiltAt.IsZero() {
		t.Fatalf("rebuilt Status() = %#v", status)
	}
	if backend.callCount() != 2 {
		t.Fatalf("embedding batches = %d, want 2", backend.callCount())
	}
	empty, err := index.Search(context.Background(), SemanticSearchRequest{Query: "private query", Limit: 2})
	if err != nil || len(empty.Matches) != 0 || backend.callCount() != 2 {
		t.Fatalf("Search(empty authorized corpus) = %#v, %v; backend calls = %d", empty, err, backend.callCount())
	}

	result, err := index.Search(context.Background(), SemanticSearchRequest{
		Query: "partition disk", Limit: 2,
		Corpus: []SemanticCorpusEntry{
			{EntryID: semanticEntryA, RevisionID: semanticRevisionA},
			{EntryID: semanticEntryB, RevisionID: semanticRevisionB},
		},
	})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(result.Matches) != 2 || result.Matches[0].EntryID != semanticEntryB || result.Generation != 1 {
		t.Fatalf("Search() = %#v", result)
	}

	staleOnly, err := index.Search(context.Background(), SemanticSearchRequest{
		Query: "dog", Limit: 1,
		Corpus: []SemanticCorpusEntry{{EntryID: semanticEntryA, RevisionID: semanticRevisionB}},
	})
	if err != nil || len(staleOnly.Matches) != 0 {
		t.Fatalf("Search(stale revision) = %#v, %v", staleOnly, err)
	}
}

func TestBruteForceSemanticIndexKeepsOldGenerationSearchableDuringRebuild(t *testing.T) {
	t.Parallel()
	backend := &semanticEmbeddingFake{}
	index, err := NewBruteForceSemanticIndex(BruteForceSemanticIndexConfig{
		Backend: backend, DocumentSchema: "knowledge-entry-v1", BatchSize: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := index.Rebuild(context.Background(), semanticDocumentSlice([]SemanticDocument{
		semanticTestDocument(semanticEntryA, semanticRevisionA, "old", "friendly dog"),
	})); err != nil {
		t.Fatalf("initial Rebuild() error = %v", err)
	}
	started := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	defer unblock()
	backend.setBlock("linux disk partition utility", started, release)
	rebuildDone := make(chan error, 1)
	go func() {
		_, err := index.Rebuild(context.Background(), semanticDocumentSlice([]SemanticDocument{
			semanticTestDocument(semanticEntryB, semanticRevisionB, "new", "linux disk partition utility"),
		}))
		rebuildDone <- err
	}()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("replacement rebuild did not reach embedding backend")
	}

	result, err := index.Search(context.Background(), SemanticSearchRequest{
		Query: "dog", Limit: 1,
		Corpus: []SemanticCorpusEntry{{EntryID: semanticEntryA, RevisionID: semanticRevisionA}},
	})
	if err != nil || len(result.Matches) != 1 || result.Matches[0].EntryID != semanticEntryA || result.Generation != 1 {
		t.Fatalf("Search() during rebuild = %#v, %v", result, err)
	}
	unblock()
	select {
	case err := <-rebuildDone:
		if err != nil {
			t.Fatalf("replacement Rebuild() error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("replacement rebuild did not finish")
	}
	status, _ := index.Status(context.Background())
	if status.Generation != 2 || status.Rebuilding {
		t.Fatalf("replacement Status() = %#v", status)
	}
}

func TestBruteForceSemanticIndexFailedRebuildPreservesActiveGeneration(t *testing.T) {
	t.Parallel()
	backend := &semanticEmbeddingFake{}
	index, err := NewBruteForceSemanticIndex(BruteForceSemanticIndexConfig{
		Backend: backend, DocumentSchema: "knowledge-entry-v1", BatchSize: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := index.Rebuild(context.Background(), semanticDocumentSlice([]SemanticDocument{
		semanticTestDocument(semanticEntryA, semanticRevisionA, "old", "friendly dog"),
	})); err != nil {
		t.Fatal(err)
	}
	backend.setFailure("force embedding failure")
	if _, err := index.Rebuild(context.Background(), semanticDocumentSlice([]SemanticDocument{
		semanticTestDocument(semanticEntryB, semanticRevisionB, "bad", "force embedding failure"),
	})); err == nil {
		t.Fatal("failed Rebuild() unexpectedly succeeded")
	}
	backend.setFailure("")
	status, _ := index.Status(context.Background())
	if status.Generation != 1 || !status.Ready || status.Documents != 1 || status.LastError == "" {
		t.Fatalf("Status() after failed rebuild = %#v", status)
	}
	result, err := index.Search(context.Background(), SemanticSearchRequest{
		Query: "dog", Limit: 1,
		Corpus: []SemanticCorpusEntry{{EntryID: semanticEntryA, RevisionID: semanticRevisionA}},
	})
	if err != nil || len(result.Matches) != 1 {
		t.Fatalf("active generation after failed rebuild = %#v, %v", result, err)
	}
}

func semanticTestDocument(entryID knowledge.EntryID, revisionID knowledge.RevisionID, fragmentID, content string) SemanticDocument {
	return SemanticDocument{
		EntryID: entryID, RevisionID: revisionID, FragmentID: fragmentID, Content: content,
		ContentHash: fmt.Sprintf("%x", sha256.Sum256([]byte(content))),
	}
}

func semanticDocumentSlice(documents []SemanticDocument) SemanticDocumentSource {
	documents = slices.Clone(documents)
	return SemanticDocumentSourceFunc(func(ctx context.Context, visit func(SemanticDocument) error) error {
		for _, document := range documents {
			if err := ctx.Err(); err != nil {
				return err
			}
			if err := visit(document); err != nil {
				return err
			}
		}
		return nil
	})
}

type semanticEmbeddingFake struct {
	mu        sync.Mutex
	calls     int
	failOn    string
	blockOn   string
	started   chan struct{}
	release   chan struct{}
	startOnce sync.Once
}

func (*semanticEmbeddingFake) Identity() EmbeddingIdentity {
	return EmbeddingIdentity{ProviderID: "fake", ModelID: "fake-v1", Dimensions: 2, Metric: SemanticMetricCosine}
}

func (f *semanticEmbeddingFake) Embed(ctx context.Context, inputs []string) ([][]float32, error) {
	f.mu.Lock()
	f.calls++
	failOn, blockOn, started, release := f.failOn, f.blockOn, f.started, f.release
	f.mu.Unlock()
	for _, input := range inputs {
		if failOn != "" && strings.Contains(input, failOn) {
			return nil, fmt.Errorf("mock embedding failure")
		}
		if blockOn != "" && strings.Contains(input, blockOn) {
			f.startOnce.Do(func() { close(started) })
			select {
			case <-release:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
	}
	result := make([][]float32, len(inputs))
	for index, input := range inputs {
		lower := strings.ToLower(input)
		switch {
		case strings.Contains(lower, "disk") || strings.Contains(lower, "partition"):
			result[index] = []float32{0, 1}
		default:
			result[index] = []float32{1, 0}
		}
	}
	return result, nil
}

func (f *semanticEmbeddingFake) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func (f *semanticEmbeddingFake) setFailure(value string) {
	f.mu.Lock()
	f.failOn = value
	f.mu.Unlock()
}

func (f *semanticEmbeddingFake) setBlock(value string, started, release chan struct{}) {
	f.mu.Lock()
	f.blockOn, f.started, f.release = value, started, release
	f.mu.Unlock()
}
