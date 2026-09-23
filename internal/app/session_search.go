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
	search := func(session domain.Session) error {
		if strings.Contains(strings.ToLower(session.Title), query) {
			result.SessionIDs = append(result.SessionIDs, session.ID)
			return nil
		}
		matched, err := c.agent.PersistedSessionMatches(ctx, session.ID, query)
		if err != nil {
			return err
		}
		if matched {
			result.SessionIDs = append(result.SessionIDs, session.ID)
		}
		return nil
	}
	for _, session := range state.Sessions {
		if err := search(session); err != nil {
			return SessionSearchResult{}, err
		}
	}
	for _, session := range state.QuickChats {
		if err := search(session); err != nil {
			return SessionSearchResult{}, err
		}
	}
	return result, nil
}
