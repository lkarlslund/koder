package service

import (
	"slices"
	"strings"

	memoryStoreAPI "github.com/lkarlslund/koder/internal/memory/store"
)

// deduplicateLexicalPostings collapses repeated projections of the same current entry
// term. Maxima preserve the most complete projection without double-counting a revision
// that appeared on both sides of a page boundary.
func deduplicateLexicalPostings(postings []memoryStoreAPI.LexicalPosting) []memoryStoreAPI.LexicalPosting {
	byKey := make(map[string]memoryStoreAPI.LexicalPosting, len(postings))
	for _, posting := range postings {
		key := posting.Term + "\x00" + string(posting.EntryID)
		if current, exists := byKey[key]; exists {
			current.Frequencies.Title = max(current.Frequencies.Title, posting.Frequencies.Title)
			current.Frequencies.Summary = max(current.Frequencies.Summary, posting.Frequencies.Summary)
			current.Frequencies.Body = max(current.Frequencies.Body, posting.Frequencies.Body)
			current.Frequencies.Aliases = max(current.Frequencies.Aliases, posting.Frequencies.Aliases)
			current.Frequencies.Tags = max(current.Frequencies.Tags, posting.Frequencies.Tags)
			byKey[key] = current
			continue
		}
		byKey[key] = posting
	}
	result := make([]memoryStoreAPI.LexicalPosting, 0, len(byKey))
	for _, posting := range byKey {
		result = append(result, posting)
	}
	slices.SortFunc(result, func(left, right memoryStoreAPI.LexicalPosting) int {
		if comparison := strings.Compare(left.Term, right.Term); comparison != 0 {
			return comparison
		}
		return strings.Compare(string(left.EntryID), string(right.EntryID))
	})
	return result
}
