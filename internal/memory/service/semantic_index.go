package service

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/lkarlslund/koder/internal/memory"
)

const (
	maxSemanticQueryBytes    = 32 * 1024
	maxSemanticCorpusEntries = 100_000
	maxSemanticSearchLimit   = 1_000
	maxSemanticDimensions    = 65_536
)

var ErrSemanticIndexUnavailable = errors.New("memory semantic index unavailable")

// SemanticMetric records how an index compares vectors. Providers normalize every
// returned similarity to [0,1], where larger values are always more relevant.
type SemanticMetric string

const (
	SemanticMetricCosine    SemanticMetric = "cosine"
	SemanticMetricDot       SemanticMetric = "dot"
	SemanticMetricEuclidean SemanticMetric = "euclidean"
)

func (m SemanticMetric) valid() bool {
	switch m {
	case SemanticMetricCosine, SemanticMetricDot, SemanticMetricEuclidean:
		return true
	default:
		return false
	}
}

// SemanticIndexIdentity binds derived vectors to the implementation that created them.
// DocumentSchema changes whenever entry-to-text rendering or fragmentation changes.
type SemanticIndexIdentity struct {
	ProviderID     string         `json:"provider_id"`
	ModelID        string         `json:"model_id"`
	Dimensions     int            `json:"dimensions"`
	Metric         SemanticMetric `json:"metric"`
	DocumentSchema string         `json:"document_schema"`
}

func (i SemanticIndexIdentity) Validate() error {
	if err := validateEmbeddingIdentity(i.ProviderID, i.ModelID, i.Dimensions, i.Metric); err != nil {
		return err
	}
	if strings.TrimSpace(i.DocumentSchema) == "" {
		return fmt.Errorf("semantic index document_schema is required")
	}
	if len(i.DocumentSchema) > 256 {
		return fmt.Errorf("semantic index document_schema exceeds 256 bytes")
	}
	return nil
}

// SemanticDocument is one derived embedding unit. FragmentID is stable within an entry
// revision. ContentHash lets a provider skip unchanged text without making vectors or
// rendered text canonical memory.
type SemanticDocument struct {
	EntryID     memory.EntryID    `json:"entry_id"`
	RevisionID  memory.RevisionID `json:"revision_id"`
	FragmentID  string            `json:"fragment_id"`
	Content     string            `json:"-"`
	ContentHash string            `json:"content_hash"`
}

func (d SemanticDocument) Validate() error {
	if strings.TrimSpace(string(d.EntryID)) == "" || strings.TrimSpace(string(d.RevisionID)) == "" {
		return fmt.Errorf("semantic document entry and revision IDs are required")
	}
	if strings.TrimSpace(d.FragmentID) == "" || len(d.FragmentID) > 256 {
		return fmt.Errorf("semantic document fragment ID must contain between 1 and 256 bytes")
	}
	if strings.TrimSpace(d.Content) == "" {
		return fmt.Errorf("semantic document content is required")
	}
	wantHash := fmt.Sprintf("%x", sha256.Sum256([]byte(d.Content)))
	if d.ContentHash != wantHash {
		return fmt.Errorf("semantic document content hash does not match content")
	}
	return nil
}

// SemanticDocumentSource streams a canonical snapshot into a provider rebuild. It calls
// visit sequentially, and the provider must not retain Content after deriving its index
// representation.
type SemanticDocumentSource interface {
	ScanSemanticDocuments(context.Context, func(SemanticDocument) error) error
}

type SemanticDocumentSourceFunc func(context.Context, func(SemanticDocument) error) error

func (fn SemanticDocumentSourceFunc) ScanSemanticDocuments(ctx context.Context, visit func(SemanticDocument) error) error {
	return fn(ctx, visit)
}

// EmbeddingIdentity describes a backend without contacting it. Dimensions are validated
// again on every response so a changed or misconfigured remote model cannot corrupt an
// index generation.
type EmbeddingIdentity struct {
	ProviderID string
	ModelID    string
	Dimensions int
	Metric     SemanticMetric
}

func (i EmbeddingIdentity) Validate() error {
	return validateEmbeddingIdentity(i.ProviderID, i.ModelID, i.Dimensions, i.Metric)
}

func validateEmbeddingIdentity(providerID, modelID string, dimensions int, metric SemanticMetric) error {
	for _, item := range []struct{ field, value string }{
		{field: "provider_id", value: providerID}, {field: "model_id", value: modelID},
	} {
		if strings.TrimSpace(item.value) == "" {
			return fmt.Errorf("semantic index %s is required", item.field)
		}
		if len(item.value) > 256 {
			return fmt.Errorf("semantic index %s exceeds 256 bytes", item.field)
		}
	}
	if dimensions <= 0 || dimensions > maxSemanticDimensions {
		return fmt.Errorf("semantic index dimensions must be between 1 and %d", maxSemanticDimensions)
	}
	if !metric.valid() {
		return fmt.Errorf("semantic index metric %q is not supported", metric)
	}
	return nil
}

// EmbeddingBackend returns one vector per input in the same order. It must honor context
// cancellation and must not make network or model calls from Identity.
type EmbeddingBackend interface {
	Identity() EmbeddingIdentity
	Embed(context.Context, []string) ([][]float32, error)
}

type SemanticCorpusEntry struct {
	EntryID    memory.EntryID    `json:"entry_id"`
	RevisionID memory.RevisionID `json:"revision_id"`
}

// SemanticSearchRequest contains an already-authorized corpus. A provider must constrain
// retrieval to these exact entry revisions; it may not search a broader corpus and rely
// on the caller to filter the result afterward.
type SemanticSearchRequest struct {
	Query  string                `json:"query"`
	Corpus []SemanticCorpusEntry `json:"corpus"`
	Limit  int                   `json:"limit"`
}

func NormalizeSemanticSearchRequest(request SemanticSearchRequest) (SemanticSearchRequest, error) {
	request.Query = strings.TrimSpace(request.Query)
	if request.Query == "" {
		return SemanticSearchRequest{}, fmt.Errorf("semantic search query is required")
	}
	if len(request.Query) > maxSemanticQueryBytes {
		return SemanticSearchRequest{}, fmt.Errorf("semantic search query exceeds %d bytes", maxSemanticQueryBytes)
	}
	if request.Limit <= 0 || request.Limit > maxSemanticSearchLimit {
		return SemanticSearchRequest{}, fmt.Errorf("semantic search limit must be between 1 and %d", maxSemanticSearchLimit)
	}
	if len(request.Corpus) > maxSemanticCorpusEntries {
		return SemanticSearchRequest{}, fmt.Errorf("semantic search corpus exceeds %d entries", maxSemanticCorpusEntries)
	}
	request.Corpus = slices.Clone(request.Corpus)
	slices.SortFunc(request.Corpus, compareSemanticCorpusEntries)
	compacted := request.Corpus[:0]
	for _, candidate := range request.Corpus {
		if strings.TrimSpace(string(candidate.EntryID)) == "" || strings.TrimSpace(string(candidate.RevisionID)) == "" {
			return SemanticSearchRequest{}, fmt.Errorf("semantic search corpus entry and revision IDs are required")
		}
		if len(compacted) > 0 && compacted[len(compacted)-1].EntryID == candidate.EntryID {
			if compacted[len(compacted)-1].RevisionID != candidate.RevisionID {
				return SemanticSearchRequest{}, fmt.Errorf("semantic search corpus contains conflicting revisions for entry %s", candidate.EntryID)
			}
			continue
		}
		compacted = append(compacted, candidate)
	}
	request.Corpus = compacted
	return request, nil
}

func compareSemanticCorpusEntries(left, right SemanticCorpusEntry) int {
	if compared := strings.Compare(string(left.EntryID), string(right.EntryID)); compared != 0 {
		return compared
	}
	return strings.Compare(string(left.RevisionID), string(right.RevisionID))
}

type SemanticSearchMatch struct {
	EntryID    memory.EntryID    `json:"entry_id"`
	RevisionID memory.RevisionID `json:"revision_id"`
	FragmentID string            `json:"fragment_id,omitempty"`
	Score      float64           `json:"score"`
}

type SemanticSearchResult struct {
	Identity   SemanticIndexIdentity `json:"identity"`
	Generation uint64                `json:"generation"`
	Matches    []SemanticSearchMatch `json:"matches"`
	Truncated  bool                  `json:"truncated,omitempty"`
}

// NormalizeSemanticSearchResult distrusts provider output: it enforces the authorized
// revision set, normalized finite scores, unique entries, and deterministic ordering.
func NormalizeSemanticSearchResult(request SemanticSearchRequest, result SemanticSearchResult) (SemanticSearchResult, error) {
	request, err := NormalizeSemanticSearchRequest(request)
	if err != nil {
		return SemanticSearchResult{}, err
	}
	if err := result.Identity.Validate(); err != nil {
		return SemanticSearchResult{}, err
	}
	if result.Generation == 0 {
		return SemanticSearchResult{}, fmt.Errorf("semantic search result generation is required")
	}
	if len(result.Matches) > request.Limit {
		return SemanticSearchResult{}, fmt.Errorf("semantic search returned %d matches for limit %d", len(result.Matches), request.Limit)
	}
	allowed := make(map[memory.EntryID]memory.RevisionID, len(request.Corpus))
	for _, candidate := range request.Corpus {
		allowed[candidate.EntryID] = candidate.RevisionID
	}
	result.Matches = slices.Clone(result.Matches)
	seen := make(map[memory.EntryID]struct{}, len(result.Matches))
	for index := range result.Matches {
		match := &result.Matches[index]
		if math.IsNaN(match.Score) || math.IsInf(match.Score, 0) || match.Score < 0 || match.Score > 1 {
			return SemanticSearchResult{}, fmt.Errorf("semantic search score for entry %s must be finite and between 0 and 1", match.EntryID)
		}
		revisionID, ok := allowed[match.EntryID]
		if !ok || revisionID != match.RevisionID {
			return SemanticSearchResult{}, fmt.Errorf("semantic search returned unauthorized or stale entry revision %s/%s", match.EntryID, match.RevisionID)
		}
		if _, duplicate := seen[match.EntryID]; duplicate {
			return SemanticSearchResult{}, fmt.Errorf("semantic search returned entry %s more than once", match.EntryID)
		}
		seen[match.EntryID] = struct{}{}
		match.FragmentID = strings.TrimSpace(match.FragmentID)
		if len(match.FragmentID) > 256 {
			return SemanticSearchResult{}, fmt.Errorf("semantic search fragment ID exceeds 256 bytes")
		}
	}
	slices.SortFunc(result.Matches, func(left, right SemanticSearchMatch) int {
		if left.Score > right.Score {
			return -1
		}
		if left.Score < right.Score {
			return 1
		}
		return strings.Compare(string(left.EntryID), string(right.EntryID))
	})
	return result, nil
}

// SemanticIndexStatus contains operational metadata only. LastError must be sanitized and
// must never contain queries, entry content, or other canonical memory.
type SemanticIndexStatus struct {
	Identity       SemanticIndexIdentity `json:"identity"`
	Generation     uint64                `json:"generation"`
	Available      bool                  `json:"available"`
	Ready          bool                  `json:"ready"`
	Rebuilding     bool                  `json:"rebuilding"`
	Documents      uint64                `json:"documents"`
	StaleDocuments uint64                `json:"stale_documents"`
	LastRebuiltAt  time.Time             `json:"last_rebuilt_at,omitzero"`
	LastError      string                `json:"last_error,omitempty"`
}

// SemanticIndexProvider owns optional derived semantic retrieval data. Canonical writes,
// lexical search, and graph search must remain usable when this interface is nil or any
// method returns ErrSemanticIndexUnavailable.
type SemanticIndexProvider interface {
	Status(context.Context) (SemanticIndexStatus, error)
	Search(context.Context, SemanticSearchRequest) (SemanticSearchResult, error)
	Rebuild(context.Context, SemanticDocumentSource) (SemanticIndexStatus, error)
}

type SearchScoreSignal struct {
	EntryID memory.EntryID `json:"entry_id"`
	Score   float64        `json:"score"`
}

type SearchScoreBlendRequest struct {
	Lexical  []SearchScoreSignal `json:"lexical,omitempty"`
	Semantic []SearchScoreSignal `json:"semantic,omitempty"`
	Limit    int                 `json:"limit"`
}

func NormalizeSearchScoreBlendRequest(request SearchScoreBlendRequest) (SearchScoreBlendRequest, error) {
	if request.Limit <= 0 || request.Limit > maxSemanticSearchLimit {
		return SearchScoreBlendRequest{}, fmt.Errorf("search score blend limit must be between 1 and %d", maxSemanticSearchLimit)
	}
	var err error
	request.Lexical, err = normalizeSearchScoreSignals("lexical", request.Lexical, false)
	if err != nil {
		return SearchScoreBlendRequest{}, err
	}
	request.Semantic, err = normalizeSearchScoreSignals("semantic", request.Semantic, true)
	if err != nil {
		return SearchScoreBlendRequest{}, err
	}
	return request, nil
}

func normalizeSearchScoreSignals(name string, signals []SearchScoreSignal, normalized bool) ([]SearchScoreSignal, error) {
	signals = slices.Clone(signals)
	slices.SortFunc(signals, func(left, right SearchScoreSignal) int {
		return strings.Compare(string(left.EntryID), string(right.EntryID))
	})
	for index, signal := range signals {
		if strings.TrimSpace(string(signal.EntryID)) == "" {
			return nil, fmt.Errorf("%s search score entry ID is required", name)
		}
		if math.IsNaN(signal.Score) || math.IsInf(signal.Score, 0) || signal.Score < 0 || normalized && signal.Score > 1 {
			return nil, fmt.Errorf("%s search score for entry %s is invalid", name, signal.EntryID)
		}
		if index > 0 && signals[index-1].EntryID == signal.EntryID {
			return nil, fmt.Errorf("%s search scores contain duplicate entry %s", name, signal.EntryID)
		}
	}
	return signals, nil
}

type BlendedSearchScore struct {
	EntryID              memory.EntryID `json:"entry_id"`
	Score                float64        `json:"score"`
	LexicalContribution  float64        `json:"lexical_contribution,omitempty"`
	SemanticContribution float64        `json:"semantic_contribution,omitempty"`
}

// NormalizeBlendedSearchScores ensures a pluggable blender cannot introduce candidates
// absent from its inputs or produce unstable, non-finite ranking data.
func NormalizeBlendedSearchScores(request SearchScoreBlendRequest, result []BlendedSearchScore) ([]BlendedSearchScore, error) {
	request, err := NormalizeSearchScoreBlendRequest(request)
	if err != nil {
		return nil, err
	}
	if len(result) > request.Limit {
		return nil, fmt.Errorf("search score blender returned %d matches for limit %d", len(result), request.Limit)
	}
	allowed := make(map[memory.EntryID]struct{}, len(request.Lexical)+len(request.Semantic))
	for _, signal := range request.Lexical {
		allowed[signal.EntryID] = struct{}{}
	}
	for _, signal := range request.Semantic {
		allowed[signal.EntryID] = struct{}{}
	}
	result = slices.Clone(result)
	seen := make(map[memory.EntryID]struct{}, len(result))
	for _, score := range result {
		if _, ok := allowed[score.EntryID]; !ok {
			return nil, fmt.Errorf("search score blender returned unknown entry %s", score.EntryID)
		}
		if _, duplicate := seen[score.EntryID]; duplicate {
			return nil, fmt.Errorf("search score blender returned duplicate entry %s", score.EntryID)
		}
		seen[score.EntryID] = struct{}{}
		values := []float64{score.Score, score.LexicalContribution, score.SemanticContribution}
		for _, value := range values {
			if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
				return nil, fmt.Errorf("search score blender returned invalid score for entry %s", score.EntryID)
			}
		}
		if math.Abs(score.Score-score.LexicalContribution-score.SemanticContribution) > 1/rankingPrecision {
			return nil, fmt.Errorf("search score blender contributions do not sum to score for entry %s", score.EntryID)
		}
	}
	slices.SortFunc(result, func(left, right BlendedSearchScore) int {
		if left.Score > right.Score {
			return -1
		}
		if left.Score < right.Score {
			return 1
		}
		return strings.Compare(string(left.EntryID), string(right.EntryID))
	})
	return result, nil
}

// SearchScoreBlender combines provider-independent lexical and normalized semantic
// signals. Implementations must be deterministic and may return candidates present in
// either input; verification, scope, freshness, and evidence ranking remains a later
// service-owned step.
type SearchScoreBlender interface {
	BlendSearchScores(context.Context, SearchScoreBlendRequest) ([]BlendedSearchScore, error)
}

type SearchScoreBlenderFunc func(context.Context, SearchScoreBlendRequest) ([]BlendedSearchScore, error)

func (fn SearchScoreBlenderFunc) BlendSearchScores(ctx context.Context, request SearchScoreBlendRequest) ([]BlendedSearchScore, error) {
	return fn(ctx, request)
}
