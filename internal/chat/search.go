package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/id"
)

var errSearchComplete = errors.New("session search complete")

// SearchSessions reports which sessions have a persisted chat title or
// timeline item containing query. Collections are streamed once so memory use
// is bounded and chat runtimes are never hydrated.
func (s *Source) SearchSessions(ctx context.Context, sessionIDs []id.ID, query string) (map[id.ID]bool, error) {
	query = strings.ToLower(strings.TrimSpace(query))
	wanted := make(map[id.ID]struct{}, len(sessionIDs))
	for _, sessionID := range sessionIDs {
		wanted[sessionID] = struct{}{}
	}
	matches := make(map[id.ID]bool)
	if query == "" {
		for sessionID := range wanted {
			matches[sessionID] = true
		}
		return matches, nil
	}
	deps, err := s.currentDeps()
	if err != nil {
		return nil, err
	}
	chatSessions := make(map[id.ID]id.ID)
	if err := chatCollection(deps.Store).Scan(ctx, func(chatRecord domain.Chat) error {
		sessionID := id.ID(chatRecord.SessionID)
		if _, ok := wanted[sessionID]; !ok {
			return nil
		}
		chatSessions[id.ID(chatRecord.ID)] = sessionID
		if strings.Contains(strings.ToLower(chatRecord.Title), query) {
			matches[sessionID] = true
		}
		return nil
	}); err != nil {
		return nil, fmt.Errorf("scan persisted chats: %w", err)
	}
	if len(matches) == len(wanted) {
		return matches, nil
	}
	err = timelineCollection(deps.Store).Scan(ctx, func(item domain.TimelineItem) error {
		sessionID, ok := chatSessions[id.ID(item.ChatID)]
		if !ok || matches[sessionID] {
			return nil
		}
		content, err := json.Marshal(item.Content)
		if err != nil {
			return fmt.Errorf("encode timeline item %s: %w", item.ID, err)
		}
		if strings.Contains(strings.ToLower(string(content)), query) {
			matches[sessionID] = true
			if len(matches) == len(wanted) {
				return errSearchComplete
			}
		}
		return nil
	})
	if err != nil && !errors.Is(err, errSearchComplete) {
		return nil, fmt.Errorf("scan persisted timelines: %w", err)
	}
	return matches, nil
}

// SessionMatches is the single-session form of SearchSessions.
func (s *Source) SessionMatches(ctx context.Context, sessionID id.ID, query string) (bool, error) {
	matches, err := s.SearchSessions(ctx, []id.ID{sessionID}, query)
	return matches[sessionID], err
}
