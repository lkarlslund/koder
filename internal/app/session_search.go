package app

import (
	"context"
	"fmt"
	"strings"

	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/id"
)

type SessionSearchResult struct {
	SessionIDs []id.ID `json:"session_ids"`
}

// SearchSessions searches persisted session and chat data without loading chat runtimes.
func (c *Controller) SearchSessions(ctx context.Context, query string) (SessionSearchResult, error) {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return SessionSearchResult{}, nil
	}
	if c.agent == nil {
		return SessionSearchResult{}, fmt.Errorf("no chat agent")
	}
	state, err := c.ManageableSessions(ctx)
	if err != nil {
		return SessionSearchResult{}, err
	}
	result := SessionSearchResult{SessionIDs: make([]id.ID, 0)}
	unmatched := make([]id.ID, 0, len(state.Sessions)+len(state.QuickChats))
	searchTitle := func(session domain.Session) {
		if strings.Contains(strings.ToLower(session.Title), query) {
			result.SessionIDs = append(result.SessionIDs, session.ID)
			return
		}
		unmatched = append(unmatched, session.ID)
	}
	for _, session := range state.Sessions {
		searchTitle(session)
	}
	for _, session := range state.QuickChats {
		searchTitle(session)
	}
	matches, err := c.agent.SearchPersistedSessions(ctx, unmatched, query)
	if err != nil {
		return SessionSearchResult{}, err
	}
	for _, sessionID := range unmatched {
		if matches[sessionID] {
			result.SessionIDs = append(result.SessionIDs, sessionID)
		}
	}
	return result, nil
}
