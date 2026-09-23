package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/lkarlslund/koder/internal/id"
)

const persistedSearchPageSize = 64

// SessionMatches reports whether a persisted chat title or timeline item in a
// session contains query. It reads bounded pages and does not hydrate runtimes.
func (s *Source) SessionMatches(ctx context.Context, sessionID id.ID, query string) (bool, error) {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return true, nil
	}
	chats, err := s.ListRecordsForSession(ctx, sessionID)
	if err != nil {
		return false, fmt.Errorf("list persisted chats: %w", err)
	}
	for _, chatRecord := range chats {
		if strings.Contains(strings.ToLower(chatRecord.Title), query) {
			return true, nil
		}
		before := id.ID("")
		for {
			page, err := s.TimelinePage(ctx, chatRecord.ID, before, persistedSearchPageSize, false)
			if err != nil {
				return false, fmt.Errorf("search persisted chat %s: %w", chatRecord.ID, err)
			}
			for _, item := range page.Items {
				content, err := json.Marshal(item.Content)
				if err != nil {
					return false, fmt.Errorf("encode timeline item %s: %w", item.ID, err)
				}
				if strings.Contains(strings.ToLower(string(content)), query) {
					return true, nil
				}
			}
			if !page.HasMore || len(page.Items) == 0 {
				break
			}
			before = page.Before
		}
	}
	return false, nil
}
