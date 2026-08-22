package store

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/lkarlslund/koder/internal/knowledge"
)

func TestEntryLexicalPostingsCountsNormalizedTermsByField(t *testing.T) {
	t.Parallel()
	entry := knowledge.Entry{
		ID:      "01a01f76-1ff6-7c1d-967a-66ad5703dd33",
		Title:   "Linux partition Linux",
		Summary: "Partition tooling",
		Body:    "Use `sfdisk --dump`.",
		Aliases: []string{"Linux disk", "sfdisk command"},
		Tags:    []string{"linux-tools", "sfdisk"},
	}
	postings := EntryLexicalPostings(entry)
	byTerm := make(map[string]LexicalFieldFrequencies, len(postings))
	for _, posting := range postings {
		byTerm[posting.Term] = posting.Frequencies
	}
	want := map[string]LexicalFieldFrequencies{
		"linux":       {Title: 2, Aliases: 1},
		"partition":   {Title: 1, Summary: 1},
		"sfdisk":      {Body: 1, Aliases: 1, Tags: 1},
		"linux-tools": {Tags: 1},
	}
	for term, frequencies := range want {
		if got := byTerm[term]; got != frequencies {
			t.Errorf("posting %q = %#v, want %#v", term, got, frequencies)
		}
	}
	for index := 1; index < len(postings); index++ {
		if postings[index-1].Term >= postings[index].Term {
			t.Fatalf("postings not strictly sorted: %#v", postings)
		}
	}
}

func TestPaginateLexicalPostingsNormalizesTermsAndUsesStableCursor(t *testing.T) {
	t.Parallel()
	postings := []LexicalPosting{
		{Term: "tools", EntryID: "01a01f76-1ff6-7c1d-967a-66ad5703dd35", Frequencies: LexicalFieldFrequencies{Tags: 1}},
		{Term: "linux", EntryID: "01a01f76-1ff6-7c1d-967a-66ad5703dd34", Frequencies: LexicalFieldFrequencies{Body: 1}},
		{Term: "linux", EntryID: "01a01f76-1ff6-7c1d-967a-66ad5703dd33", Frequencies: LexicalFieldFrequencies{Title: 1}},
		{Term: "ignored", EntryID: "01a01f76-1ff6-7c1d-967a-66ad5703dd36", Frequencies: LexicalFieldFrequencies{Body: 1}},
	}
	request := LexicalPostingRequest{Terms: []string{" Tools ", "LINUX tools"}, Limit: 1}
	var got []string
	for {
		page, err := PaginateLexicalPostings(postings, request, 7, 4)
		if err != nil {
			t.Fatalf("PaginateLexicalPostings() error = %v", err)
		}
		if len(page.Postings) != 1 {
			t.Fatalf("page postings = %#v", page.Postings)
		}
		wantFrequencies := []LexicalDocumentFrequency{{Term: "linux", Count: 2}, {Term: "tools", Count: 1}}
		if page.DocumentCount != 4 || !reflect.DeepEqual(page.DocumentFrequencies, wantFrequencies) {
			t.Fatalf("corpus statistics = %d, %v", page.DocumentCount, page.DocumentFrequencies)
		}
		got = append(got, page.Postings[0].Term+"/"+string(page.Postings[0].EntryID))
		if page.NextCursor == "" {
			break
		}
		request.Cursor = page.NextCursor
	}
	want := []string{
		"linux/01a01f76-1ff6-7c1d-967a-66ad5703dd33",
		"linux/01a01f76-1ff6-7c1d-967a-66ad5703dd34",
		"tools/01a01f76-1ff6-7c1d-967a-66ad5703dd35",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("paged postings = %v, want %v", got, want)
	}
}

func TestPaginateLexicalPostingsRejectsInvalidAndStaleRequests(t *testing.T) {
	t.Parallel()
	postings := []LexicalPosting{
		{Term: "linux", EntryID: "01a01f76-1ff6-7c1d-967a-66ad5703dd33", Frequencies: LexicalFieldFrequencies{Title: 1}},
		{Term: "linux", EntryID: "01a01f76-1ff6-7c1d-967a-66ad5703dd34", Frequencies: LexicalFieldFrequencies{Body: 1}},
	}
	request := LexicalPostingRequest{Terms: []string{"linux"}, Limit: 1}
	page, err := PaginateLexicalPostings(postings, request, 2, 2)
	if err != nil || page.NextCursor == "" {
		t.Fatalf("first page = %#v, %v", page, err)
	}
	changed := request
	changed.Terms = []string{"different"}
	changed.Cursor = page.NextCursor
	if _, err := PaginateLexicalPostings(postings, changed, 2, 2); !errors.Is(err, ErrInvalidCursor) {
		t.Fatalf("changed terms error = %v, want ErrInvalidCursor", err)
	}
	request.Cursor = page.NextCursor
	if _, err := PaginateLexicalPostings(postings, request, 3, 2); !errors.Is(err, ErrStaleCursor) {
		t.Fatalf("retired generation error = %v, want ErrStaleCursor", err)
	}

	tooManyTerms := make([]string, maxLexicalPostingTerms+1)
	for index := range tooManyTerms {
		tooManyTerms[index] = "term" + string(rune('一'+index))
	}
	for _, invalid := range []LexicalPostingRequest{
		{},
		{Terms: []string{strings.Repeat("a", maxLexicalPostingInput+1)}},
		{Terms: []string{"valid"}, Limit: maxLexicalPostingLimit + 1},
		{Terms: tooManyTerms},
	} {
		if _, err := NormalizeLexicalPostingRequest(invalid); err == nil {
			t.Errorf("NormalizeLexicalPostingRequest(%#v) unexpectedly succeeded", invalid)
		}
	}
}
