package service

import (
	"fmt"
	"math"
	"slices"
	"strings"

	"github.com/lkarlslund/koder/internal/knowledge"
	knowledgeStore "github.com/lkarlslund/koder/internal/knowledge/store"
)

// LexicalFieldWeights controls the relative importance of canonical entry fields. The
// zero value selects DefaultLexicalFieldWeights; in a non-zero value, individual zero
// fields are intentionally disabled.
type LexicalFieldWeights struct {
	Title   float64 `json:"title"`
	Summary float64 `json:"summary"`
	Body    float64 `json:"body"`
	Aliases float64 `json:"aliases"`
	Tags    float64 `json:"tags"`
}

func DefaultLexicalFieldWeights() LexicalFieldWeights {
	return LexicalFieldWeights{Title: 8, Summary: 4, Body: 1, Aliases: 6, Tags: 5}
}

type LexicalTermScore struct {
	Term              string                                 `json:"term"`
	Score             float64                                `json:"score"`
	InverseFrequency  float64                                `json:"inverse_frequency"`
	WeightedFrequency float64                                `json:"weighted_frequency"`
	Frequencies       knowledgeStore.LexicalFieldFrequencies `json:"frequencies"`
}

type LexicalScoredEntry struct {
	EntryID knowledge.EntryID  `json:"entry_id"`
	Score   float64            `json:"score"`
	Terms   []LexicalTermScore `json:"terms"`
}

// ScoreLexicalMatches groups postings by entry and applies deterministic field-weighted
// inverse-document-frequency scoring. Callers must pass frequencies calculated over the
// same corpus represented by documentCount.
func ScoreLexicalMatches(postings []knowledgeStore.LexicalPosting, documentCount uint64, frequencies []knowledgeStore.LexicalDocumentFrequency, weights LexicalFieldWeights) ([]LexicalScoredEntry, error) {
	if weights == (LexicalFieldWeights{}) {
		weights = DefaultLexicalFieldWeights()
	}
	if err := validateLexicalFieldWeights(weights); err != nil {
		return nil, err
	}
	documentFrequency := make(map[string]uint64, len(frequencies))
	for _, frequency := range frequencies {
		if frequency.Term == "" || frequency.Count > documentCount {
			return nil, fmt.Errorf("invalid lexical document frequency")
		}
		if _, duplicate := documentFrequency[frequency.Term]; duplicate {
			return nil, fmt.Errorf("duplicate lexical document frequency for %q", frequency.Term)
		}
		documentFrequency[frequency.Term] = frequency.Count
	}
	if len(postings) > 0 && documentCount == 0 {
		return nil, fmt.Errorf("lexical document count is required when postings are present")
	}

	grouped := make(map[knowledge.EntryID]map[string]knowledgeStore.LexicalPosting)
	for _, posting := range postings {
		df, ok := documentFrequency[posting.Term]
		if !ok || df == 0 || posting.EntryID == "" || posting.Frequencies.Total() == 0 {
			return nil, fmt.Errorf("lexical posting lacks valid corpus statistics")
		}
		terms := grouped[posting.EntryID]
		if terms == nil {
			terms = make(map[string]knowledgeStore.LexicalPosting)
			grouped[posting.EntryID] = terms
		}
		if _, duplicate := terms[posting.Term]; duplicate {
			return nil, fmt.Errorf("duplicate lexical posting for entry %s and term %q", posting.EntryID, posting.Term)
		}
		terms[posting.Term] = posting
	}

	results := make([]LexicalScoredEntry, 0, len(grouped))
	for entryID, terms := range grouped {
		result := LexicalScoredEntry{EntryID: entryID, Terms: make([]LexicalTermScore, 0, len(terms))}
		for term, posting := range terms {
			weightedFrequency := weightedLexicalFrequency(posting.Frequencies, weights)
			if math.IsNaN(weightedFrequency) || math.IsInf(weightedFrequency, 0) {
				return nil, fmt.Errorf("lexical weighted frequency is not finite")
			}
			if weightedFrequency == 0 {
				continue
			}
			df := documentFrequency[term]
			inverseFrequency := math.Log1p((float64(documentCount) - float64(df) + 0.5) / (float64(df) + 0.5))
			score := inverseFrequency * math.Log1p(weightedFrequency)
			if math.IsNaN(score) || math.IsInf(score, 0) {
				return nil, fmt.Errorf("lexical score is not finite")
			}
			result.Score += score
			result.Terms = append(result.Terms, LexicalTermScore{
				Term: term, Score: score, InverseFrequency: inverseFrequency,
				WeightedFrequency: weightedFrequency, Frequencies: posting.Frequencies,
			})
		}
		if len(result.Terms) == 0 {
			continue
		}
		slices.SortFunc(result.Terms, func(left, right LexicalTermScore) int {
			return strings.Compare(left.Term, right.Term)
		})
		results = append(results, result)
	}
	slices.SortFunc(results, func(left, right LexicalScoredEntry) int {
		if left.Score > right.Score {
			return -1
		}
		if left.Score < right.Score {
			return 1
		}
		return strings.Compare(string(left.EntryID), string(right.EntryID))
	})
	return results, nil
}

func validateLexicalFieldWeights(weights LexicalFieldWeights) error {
	values := []float64{weights.Title, weights.Summary, weights.Body, weights.Aliases, weights.Tags}
	positive := false
	for _, value := range values {
		if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
			return fmt.Errorf("lexical field weights must be finite and non-negative")
		}
		positive = positive || value > 0
	}
	if !positive {
		return fmt.Errorf("at least one lexical field weight must be positive")
	}
	return nil
}

func weightedLexicalFrequency(frequencies knowledgeStore.LexicalFieldFrequencies, weights LexicalFieldWeights) float64 {
	return float64(frequencies.Title)*weights.Title +
		float64(frequencies.Summary)*weights.Summary +
		float64(frequencies.Body)*weights.Body +
		float64(frequencies.Aliases)*weights.Aliases +
		float64(frequencies.Tags)*weights.Tags
}
