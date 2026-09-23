package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/id"
	"github.com/lkarlslund/koder/internal/store"
)

const chatSearchIndexPageSize = 256

type chatSearchIndex struct {
	ChatID          id.ID
	ChatUpdatedAt   time.Time
	TimelineCount   int
	LatestTimeline  id.ID
	LatestUpdatedAt time.Time
	Text            string
}

func chatSearchIndexCollection(st *store.Store) store.Collection[chatSearchIndex] {
	return store.NewCollection(st, store.CollectionSpec[chatSearchIndex]{
		Namespace: "chat-search-index",
		GetID:     func(value chatSearchIndex) string { return value.ChatID },
		SetID:     func(value *chatSearchIndex, recordID string) { value.ChatID = id.ID(recordID) },
		Indexes: []store.IndexSpec[chatSearchIndex]{
			{Name: "chat", Value: func(value chatSearchIndex) string { return value.ChatID }},
		},
	})
}

// SearchSessions reports which sessions have a persisted chat title or
// timeline item containing query. Each chat's derived text index is rebuilt
// lazily only when its source fingerprint is missing or stale.
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
	chats, err := chatCollection(deps.Store).List(ctx, store.All[domain.Chat]())
	if err != nil {
		return nil, fmt.Errorf("list persisted chats for search: %w", err)
	}
	for _, chatRecord := range chats {
		sessionID := id.ID(chatRecord.SessionID)
		if _, ok := wanted[sessionID]; !ok || matches[sessionID] {
			continue
		}
		if strings.Contains(strings.ToLower(chatRecord.Title), query) {
			matches[sessionID] = true
			continue
		}
		index, err := currentChatSearchIndex(ctx, deps.Store, chatRecord)
		if err != nil {
			return nil, err
		}
		if strings.Contains(index.Text, query) {
			matches[sessionID] = true
		}
	}
	return matches, nil
}

func currentChatSearchIndex(ctx context.Context, st *store.Store, chatRecord domain.Chat) (chatSearchIndex, error) {
	fingerprint, err := chatSearchFingerprint(ctx, st, chatRecord)
	if err != nil {
		return chatSearchIndex{}, err
	}
	stored, err := chatSearchIndexCollection(st).List(ctx, store.ByIndex[chatSearchIndex]("chat", string(chatRecord.ID)))
	if err != nil {
		return chatSearchIndex{}, fmt.Errorf("load chat search index %s: %w", chatRecord.ID, err)
	}
	if len(stored) == 1 && stored[0].sameSource(fingerprint) {
		return stored[0], nil
	}
	built, err := buildChatSearchIndex(ctx, st, fingerprint)
	if err != nil {
		return chatSearchIndex{}, err
	}
	if err := chatSearchIndexCollection(st).Put(ctx, built); err != nil {
		return chatSearchIndex{}, fmt.Errorf("save chat search index %s: %w", chatRecord.ID, err)
	}
	return built, nil
}

func chatSearchFingerprint(ctx context.Context, st *store.Store, chatRecord domain.Chat) (chatSearchIndex, error) {
	if err := ensureTimelineSequenceIndex(ctx, st, chatRecord.ID); err != nil {
		return chatSearchIndex{}, err
	}
	page, err := timelineCollection(st).ListIndexPage(ctx, "chat-seq", string(chatRecord.ID), "", "", 1, true)
	if err != nil {
		return chatSearchIndex{}, fmt.Errorf("fingerprint chat search index %s: %w", chatRecord.ID, err)
	}
	fingerprint := chatSearchIndex{ChatID: chatRecord.ID, ChatUpdatedAt: chatRecord.UpdatedAt, TimelineCount: page.Total}
	if len(page.Items) > 0 {
		latest := page.Items[len(page.Items)-1]
		fingerprint.LatestTimeline = latest.ID
		fingerprint.LatestUpdatedAt = latest.UpdatedAt
	}
	return fingerprint, nil
}

func buildChatSearchIndex(ctx context.Context, st *store.Store, index chatSearchIndex) (chatSearchIndex, error) {
	var text strings.Builder
	after := ""
	for {
		page, err := timelineCollection(st).ListIndexPage(ctx, "chat-seq", string(index.ChatID), "", after, chatSearchIndexPageSize, false)
		if err != nil {
			return chatSearchIndex{}, fmt.Errorf("build chat search index %s: %w", index.ChatID, err)
		}
		for _, item := range page.Items {
			content, err := json.Marshal(item.Content)
			if err != nil {
				return chatSearchIndex{}, fmt.Errorf("encode timeline item %s for search: %w", item.ID, err)
			}
			text.Write(content)
			text.WriteByte('\n')
		}
		if !page.HasAfter || len(page.Items) == 0 {
			break
		}
		after = timelineIndexCursor(page.Items[len(page.Items)-1])
	}
	index.Text = strings.ToLower(text.String())
	return index, nil
}

func (index chatSearchIndex) sameSource(other chatSearchIndex) bool {
	return index.ChatID == other.ChatID &&
		index.ChatUpdatedAt.Equal(other.ChatUpdatedAt) &&
		index.TimelineCount == other.TimelineCount &&
		index.LatestTimeline == other.LatestTimeline &&
		index.LatestUpdatedAt.Equal(other.LatestUpdatedAt)
}

// SessionMatches is the single-session form of SearchSessions.
func (s *Source) SessionMatches(ctx context.Context, sessionID id.ID, query string) (bool, error) {
	matches, err := s.SearchSessions(ctx, []id.ID{sessionID}, query)
	return matches[sessionID], err
}
