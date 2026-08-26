package service

import (
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/lkarlslund/koder/internal/memory"
)

const (
	defaultSemanticEmbeddingBatch = 32
	defaultSemanticMaxDocuments   = 10_000
	maxSemanticVectorComponents   = 50_000_000
)

type BruteForceSemanticIndexConfig struct {
	Backend        EmbeddingBackend
	DocumentSchema string
	BatchSize      int
	MaxDocuments   int
	Now            func() time.Time
}

// BruteForceSemanticIndex is an optional, in-process semantic provider. Rebuild creates
// a complete immutable generation and swaps it atomically; searches continue against the
// previous generation while embedding the replacement corpus.
type BruteForceSemanticIndex struct {
	backend      EmbeddingBackend
	identity     SemanticIndexIdentity
	batchSize    int
	maxDocuments int
	now          func() time.Time

	mu            sync.RWMutex
	rebuildMu     sync.Mutex
	generation    uint64
	documents     []semanticVectorDocument
	available     bool
	ready         bool
	rebuilding    bool
	lastRebuiltAt time.Time
	lastError     string
}

type semanticVectorDocument struct {
	entryID     memory.EntryID
	revisionID  memory.RevisionID
	fragmentID  string
	contentHash string
	vector      []float32
}

var _ SemanticIndexProvider = (*BruteForceSemanticIndex)(nil)

func NewBruteForceSemanticIndex(cfg BruteForceSemanticIndexConfig) (*BruteForceSemanticIndex, error) {
	if cfg.Backend == nil {
		return nil, fmt.Errorf("semantic embedding backend is required")
	}
	embeddingIdentity := cfg.Backend.Identity()
	if err := embeddingIdentity.Validate(); err != nil {
		return nil, err
	}
	if embeddingIdentity.Metric != SemanticMetricCosine {
		return nil, fmt.Errorf("brute-force semantic index requires cosine embeddings")
	}
	identity := SemanticIndexIdentity{
		ProviderID: embeddingIdentity.ProviderID, ModelID: embeddingIdentity.ModelID,
		Dimensions: embeddingIdentity.Dimensions, Metric: embeddingIdentity.Metric,
		DocumentSchema: strings.TrimSpace(cfg.DocumentSchema),
	}
	if err := identity.Validate(); err != nil {
		return nil, err
	}
	if cfg.BatchSize == 0 {
		cfg.BatchSize = defaultSemanticEmbeddingBatch
	}
	if cfg.BatchSize < 1 || cfg.BatchSize > 1_000 {
		return nil, fmt.Errorf("semantic embedding batch size must be between 1 and 1000")
	}
	if cfg.MaxDocuments == 0 {
		cfg.MaxDocuments = defaultSemanticMaxDocuments
	}
	if cfg.MaxDocuments < 1 || cfg.MaxDocuments > maxSemanticCorpusEntries {
		return nil, fmt.Errorf("semantic maximum documents must be between 1 and %d", maxSemanticCorpusEntries)
	}
	if cfg.MaxDocuments > maxSemanticVectorComponents/identity.Dimensions {
		return nil, fmt.Errorf("semantic index configuration exceeds %d vector components", maxSemanticVectorComponents)
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &BruteForceSemanticIndex{
		backend: cfg.Backend, identity: identity, batchSize: cfg.BatchSize,
		maxDocuments: cfg.MaxDocuments, now: cfg.Now, available: true,
	}, nil
}

func (p *BruteForceSemanticIndex) Status(ctx context.Context) (SemanticIndexStatus, error) {
	if err := ctx.Err(); err != nil {
		return SemanticIndexStatus{}, err
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.statusLocked(), nil
}

func (p *BruteForceSemanticIndex) statusLocked() SemanticIndexStatus {
	return SemanticIndexStatus{
		Identity: p.identity, Generation: p.generation, Available: p.available,
		Ready: p.ready, Rebuilding: p.rebuilding, Documents: uint64(len(p.documents)),
		LastRebuiltAt: p.lastRebuiltAt, LastError: p.lastError,
	}
}

func (p *BruteForceSemanticIndex) Rebuild(ctx context.Context, source SemanticDocumentSource) (status SemanticIndexStatus, rebuildErr error) {
	if err := ctx.Err(); err != nil {
		return SemanticIndexStatus{}, err
	}
	if source == nil {
		return SemanticIndexStatus{}, fmt.Errorf("semantic document source is required")
	}
	if !p.rebuildMu.TryLock() {
		return SemanticIndexStatus{}, fmt.Errorf("semantic index rebuild already running")
	}
	defer p.rebuildMu.Unlock()
	p.mu.Lock()
	p.rebuilding = true
	p.lastError = ""
	p.mu.Unlock()
	defer func() {
		p.mu.Lock()
		p.rebuilding = false
		if rebuildErr != nil {
			if errors.Is(rebuildErr, ErrSemanticIndexUnavailable) {
				p.available = false
			}
			p.lastError = "semantic index rebuild failed"
		}
		status = p.statusLocked()
		p.mu.Unlock()
	}()

	documents := make([]semanticVectorDocument, 0)
	batch := make([]SemanticDocument, 0, p.batchSize)
	seen := make(map[string]struct{})
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		inputs := make([]string, len(batch))
		for index := range batch {
			inputs[index] = batch[index].Content
		}
		vectors, err := p.backend.Embed(ctx, inputs)
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return ctxErr
			}
			return fmt.Errorf("%w: embed semantic rebuild batch: %v", ErrSemanticIndexUnavailable, err)
		}
		if len(vectors) != len(batch) {
			return fmt.Errorf("%w: semantic embedding backend returned %d vectors for %d inputs", ErrSemanticIndexUnavailable, len(vectors), len(batch))
		}
		for index, vector := range vectors {
			normalized, err := normalizeEmbeddingVector(vector, p.identity.Dimensions)
			if err != nil {
				return fmt.Errorf("%w: semantic embedding response %d: %v", ErrSemanticIndexUnavailable, index, err)
			}
			documents = append(documents, semanticVectorDocument{
				entryID: batch[index].EntryID, revisionID: batch[index].RevisionID,
				fragmentID: batch[index].FragmentID, contentHash: batch[index].ContentHash,
				vector: normalized,
			})
		}
		batch = batch[:0]
		return nil
	}
	err := source.ScanSemanticDocuments(ctx, func(document SemanticDocument) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := document.Validate(); err != nil {
			return err
		}
		key := strings.Join([]string{string(document.EntryID), string(document.RevisionID), document.FragmentID}, "\x00")
		if _, duplicate := seen[key]; duplicate {
			return fmt.Errorf("semantic document source returned duplicate fragment %s", document.FragmentID)
		}
		seen[key] = struct{}{}
		if len(seen) > p.maxDocuments {
			return fmt.Errorf("semantic document source exceeds %d fragments", p.maxDocuments)
		}
		batch = append(batch, document)
		if len(batch) == p.batchSize {
			return flush()
		}
		return nil
	})
	if err != nil {
		return SemanticIndexStatus{}, err
	}
	if err := flush(); err != nil {
		return SemanticIndexStatus{}, err
	}
	if err := ctx.Err(); err != nil {
		return SemanticIndexStatus{}, err
	}
	slices.SortFunc(documents, func(left, right semanticVectorDocument) int {
		if compared := strings.Compare(string(left.entryID), string(right.entryID)); compared != 0 {
			return compared
		}
		if compared := strings.Compare(string(left.revisionID), string(right.revisionID)); compared != 0 {
			return compared
		}
		return strings.Compare(left.fragmentID, right.fragmentID)
	})
	if err := ctx.Err(); err != nil {
		return SemanticIndexStatus{}, err
	}

	p.mu.Lock()
	if p.generation == math.MaxUint64 {
		p.mu.Unlock()
		return SemanticIndexStatus{}, fmt.Errorf("semantic index generation exhausted")
	}
	p.documents = documents
	p.generation++
	p.available = true
	p.ready = true
	p.lastRebuiltAt = p.now().UTC().Round(0)
	p.lastError = ""
	p.mu.Unlock()
	return SemanticIndexStatus{}, nil
}

func (p *BruteForceSemanticIndex) Search(ctx context.Context, request SemanticSearchRequest) (SemanticSearchResult, error) {
	request, err := NormalizeSemanticSearchRequest(request)
	if err != nil {
		return SemanticSearchResult{}, err
	}
	p.mu.RLock()
	if !p.ready {
		p.mu.RUnlock()
		return SemanticSearchResult{}, ErrSemanticIndexUnavailable
	}
	generation := p.generation
	documents := p.documents
	p.mu.RUnlock()
	allowed := make(map[string]struct{}, len(request.Corpus))
	for _, candidate := range request.Corpus {
		allowed[strings.Join([]string{string(candidate.EntryID), string(candidate.RevisionID)}, "\x00")] = struct{}{}
	}
	hasIndexedCandidate := false
	for _, document := range documents {
		if _, ok := allowed[strings.Join([]string{string(document.entryID), string(document.revisionID)}, "\x00")]; ok {
			hasIndexedCandidate = true
			break
		}
	}
	if !hasIndexedCandidate {
		return NormalizeSemanticSearchResult(request, SemanticSearchResult{
			Identity: p.identity, Generation: generation, Matches: []SemanticSearchMatch{},
		})
	}

	vectors, err := p.backend.Embed(ctx, []string{request.Query})
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return SemanticSearchResult{}, ctxErr
		}
		p.markUnavailable("semantic index query failed")
		return SemanticSearchResult{}, fmt.Errorf("%w: embed semantic query: %v", ErrSemanticIndexUnavailable, err)
	}
	if len(vectors) != 1 {
		p.markUnavailable("semantic index query failed")
		return SemanticSearchResult{}, fmt.Errorf("semantic embedding backend returned %d query vectors", len(vectors))
	}
	query, err := normalizeEmbeddingVector(vectors[0], p.identity.Dimensions)
	if err != nil {
		p.markUnavailable("semantic index query failed")
		return SemanticSearchResult{}, fmt.Errorf("semantic query embedding: %w", err)
	}
	best := make(map[string]SemanticSearchMatch)
	for _, document := range documents {
		if err := ctx.Err(); err != nil {
			return SemanticSearchResult{}, err
		}
		if _, ok := allowed[strings.Join([]string{string(document.entryID), string(document.revisionID)}, "\x00")]; !ok {
			continue
		}
		score := min(1, max(0, (dotProduct(query, document.vector)+1)/2))
		candidate := SemanticSearchMatch{
			EntryID: document.entryID, RevisionID: document.revisionID,
			FragmentID: document.fragmentID, Score: score,
		}
		current, exists := best[string(document.entryID)]
		if !exists || candidate.Score > current.Score || candidate.Score == current.Score && candidate.FragmentID < current.FragmentID {
			best[string(document.entryID)] = candidate
		}
	}
	matches := make([]SemanticSearchMatch, 0, len(best))
	for _, match := range best {
		matches = append(matches, match)
	}
	slices.SortFunc(matches, func(left, right SemanticSearchMatch) int {
		if left.Score > right.Score {
			return -1
		}
		if left.Score < right.Score {
			return 1
		}
		return strings.Compare(string(left.EntryID), string(right.EntryID))
	})
	truncated := len(matches) > request.Limit
	if truncated {
		matches = matches[:request.Limit]
	}
	result, err := NormalizeSemanticSearchResult(request, SemanticSearchResult{
		Identity: p.identity, Generation: generation, Matches: matches, Truncated: truncated,
	})
	if err != nil {
		return SemanticSearchResult{}, err
	}
	p.mu.Lock()
	p.available = true
	if p.lastError == "semantic index query failed" {
		p.lastError = ""
	}
	p.mu.Unlock()
	return result, nil
}

func (p *BruteForceSemanticIndex) markUnavailable(message string) {
	p.mu.Lock()
	p.available = false
	p.lastError = message
	p.mu.Unlock()
}

func normalizeEmbeddingVector(vector []float32, dimensions int) ([]float32, error) {
	if len(vector) != dimensions {
		return nil, fmt.Errorf("embedding has %d dimensions, want %d", len(vector), dimensions)
	}
	normSquared := 0.0
	result := make([]float32, len(vector))
	for index, value := range vector {
		if math.IsNaN(float64(value)) || math.IsInf(float64(value), 0) {
			return nil, fmt.Errorf("embedding contains a non-finite component")
		}
		result[index] = value
		normSquared += float64(value) * float64(value)
	}
	if normSquared == 0 || math.IsInf(normSquared, 0) {
		return nil, fmt.Errorf("embedding vector has no finite magnitude")
	}
	norm := math.Sqrt(normSquared)
	for index := range result {
		result[index] = float32(float64(result[index]) / norm)
	}
	return result, nil
}

func dotProduct(left, right []float32) float64 {
	total := 0.0
	for index := range left {
		total += float64(left[index]) * float64(right[index])
	}
	return total
}
