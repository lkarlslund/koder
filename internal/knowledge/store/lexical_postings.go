package store

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/lkarlslund/koder/internal/knowledge"
)

const (
	defaultLexicalPostingLimit = 100
	maxLexicalPostingLimit     = 1000
	maxLexicalPostingTerms     = 64
	maxLexicalPostingInput     = 4 << 10
)

// LexicalFieldFrequencies records how often one normalized term occurs in each searchable
// canonical entry field. It is derived data and never advances an entry revision.
type LexicalFieldFrequencies struct {
	Title   uint32 `json:"title,omitempty"`
	Summary uint32 `json:"summary,omitempty"`
	Body    uint32 `json:"body,omitempty"`
	Aliases uint32 `json:"aliases,omitempty"`
	Tags    uint32 `json:"tags,omitempty"`
}

func (f LexicalFieldFrequencies) Total() uint64 {
	return uint64(f.Title) + uint64(f.Summary) + uint64(f.Body) + uint64(f.Aliases) + uint64(f.Tags)
}

// LexicalPosting is one normalized term's occurrence data for one canonical entry.
type LexicalPosting struct {
	Term        string                  `json:"term"`
	EntryID     knowledge.EntryID       `json:"entry_id"`
	Frequencies LexicalFieldFrequencies `json:"frequencies"`
}

type LexicalPostingRequest struct {
	Terms  []string `json:"terms"`
	Limit  int      `json:"limit,omitempty"`
	Cursor string   `json:"cursor,omitempty"`
}

type LexicalPostingPage struct {
	Postings   []LexicalPosting `json:"postings"`
	NextCursor string           `json:"next_cursor,omitempty"`
}

// EntryLexicalPostings derives deterministic term postings from an entry's searchable
// fields. Repeated terms are counted, while each term produces only one posting.
func EntryLexicalPostings(entry knowledge.Entry) []LexicalPosting {
	frequencies := make(map[string]LexicalFieldFrequencies)
	add := func(value string, increment func(*LexicalFieldFrequencies)) {
		for _, term := range knowledge.LexicalTerms(value) {
			current := frequencies[term]
			increment(&current)
			frequencies[term] = current
		}
	}
	add(entry.Title, func(value *LexicalFieldFrequencies) { value.Title++ })
	add(entry.Summary, func(value *LexicalFieldFrequencies) { value.Summary++ })
	add(entry.Body, func(value *LexicalFieldFrequencies) { value.Body++ })
	for _, alias := range entry.Aliases {
		add(alias, func(value *LexicalFieldFrequencies) { value.Aliases++ })
	}
	for _, tag := range entry.Tags {
		add(tag, func(value *LexicalFieldFrequencies) { value.Tags++ })
	}
	postings := make([]LexicalPosting, 0, len(frequencies))
	for term, counts := range frequencies {
		postings = append(postings, LexicalPosting{Term: term, EntryID: entry.ID, Frequencies: counts})
	}
	slices.SortFunc(postings, func(left, right LexicalPosting) int {
		return strings.Compare(left.Term, right.Term)
	})
	return postings
}

// NormalizeLexicalPostingRequest tokenizes, deduplicates, and sorts requested terms.
// Callers may supply individual terms or natural-language fragments.
func NormalizeLexicalPostingRequest(request LexicalPostingRequest) (LexicalPostingRequest, error) {
	if request.Limit < 0 || request.Limit > maxLexicalPostingLimit {
		return LexicalPostingRequest{}, fmt.Errorf("lexical posting limit must be between 0 and %d", maxLexicalPostingLimit)
	}
	if request.Limit == 0 {
		request.Limit = defaultLexicalPostingLimit
	}
	totalBytes := 0
	terms := make([]string, 0, len(request.Terms))
	for _, value := range request.Terms {
		totalBytes += len(value)
		if totalBytes > maxLexicalPostingInput {
			return LexicalPostingRequest{}, fmt.Errorf("lexical posting terms exceed %d input bytes", maxLexicalPostingInput)
		}
		terms = append(terms, knowledge.LexicalTerms(value)...)
	}
	slices.Sort(terms)
	terms = slices.Compact(terms)
	if len(terms) == 0 {
		return LexicalPostingRequest{}, fmt.Errorf("at least one lexical posting term is required")
	}
	if len(terms) > maxLexicalPostingTerms {
		return LexicalPostingRequest{}, fmt.Errorf("lexical posting request exceeds %d normalized terms", maxLexicalPostingTerms)
	}
	request.Terms = terms
	return request, nil
}

// PaginateLexicalPostings applies the backend-neutral posting lookup contract.
func PaginateLexicalPostings(postings []LexicalPosting, request LexicalPostingRequest, generation uint64) (LexicalPostingPage, error) {
	request, err := NormalizeLexicalPostingRequest(request)
	if err != nil {
		return LexicalPostingPage{}, err
	}
	binding, err := lexicalPostingCursorBinding(request, generation)
	if err != nil {
		return LexicalPostingPage{}, err
	}

	byKey := make(map[string]LexicalPosting, len(postings))
	for _, posting := range postings {
		if err := validateLexicalPosting(posting); err != nil {
			return LexicalPostingPage{}, err
		}
		if !slices.Contains(request.Terms, posting.Term) {
			continue
		}
		key := lexicalPostingKey(posting)
		if existing, ok := byKey[key]; ok {
			existing.Frequencies = maxLexicalFrequencies(existing.Frequencies, posting.Frequencies)
			byKey[key] = existing
			continue
		}
		byKey[key] = posting
	}
	filtered := make([]LexicalPosting, 0, len(byKey))
	for _, posting := range byKey {
		filtered = append(filtered, posting)
	}
	slices.SortFunc(filtered, compareLexicalPostings)

	if request.Cursor != "" {
		position, err := DecodeCursor(request.Cursor, binding)
		if err != nil {
			return LexicalPostingPage{}, err
		}
		first := len(filtered)
		for index, posting := range filtered {
			if posting.Term > position.SortValue ||
				(posting.Term == position.SortValue && string(posting.EntryID) > position.ObjectID) {
				first = index
				break
			}
		}
		filtered = filtered[first:]
	}

	page := LexicalPostingPage{}
	if len(filtered) <= request.Limit {
		page.Postings = filtered
		return page, nil
	}
	page.Postings = filtered[:request.Limit]
	last := page.Postings[len(page.Postings)-1]
	page.NextCursor, err = EncodeCursor(binding, CursorPosition{SortValue: last.Term, ObjectID: string(last.EntryID)})
	return page, err
}

func lexicalPostingCursorBinding(request LexicalPostingRequest, generation uint64) (CursorBinding, error) {
	encoded, err := json.Marshal(request.Terms)
	if err != nil {
		return CursorBinding{}, err
	}
	digest := sha256.Sum256(encoded)
	return CursorBinding{
		Index: "entry-lexical", IndexGeneration: generation,
		QueryFingerprint: hex.EncodeToString(digest[:]), SortField: "term_entry_id",
	}, nil
}

func validateLexicalPosting(posting LexicalPosting) error {
	if posting.EntryID == "" || posting.Term == "" || posting.Frequencies.Total() == 0 {
		return fmt.Errorf("invalid lexical posting")
	}
	terms := knowledge.LexicalTerms(posting.Term)
	if len(terms) != 1 || terms[0] != posting.Term {
		return fmt.Errorf("invalid normalized lexical posting term")
	}
	return nil
}

func compareLexicalPostings(left, right LexicalPosting) int {
	if comparison := strings.Compare(left.Term, right.Term); comparison != 0 {
		return comparison
	}
	return strings.Compare(string(left.EntryID), string(right.EntryID))
}

func lexicalPostingKey(posting LexicalPosting) string {
	return posting.Term + "\x00" + string(posting.EntryID)
}

func maxLexicalFrequencies(left, right LexicalFieldFrequencies) LexicalFieldFrequencies {
	left.Title = max(left.Title, right.Title)
	left.Summary = max(left.Summary, right.Summary)
	left.Body = max(left.Body, right.Body)
	left.Aliases = max(left.Aliases, right.Aliases)
	left.Tags = max(left.Tags, right.Tags)
	return left
}
