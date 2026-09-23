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
	LatestTimeline  id.ID
	LatestUpdatedAt time.Time
	Bloom           []byte
}

type chatSearchDocument struct {
	ChatID id.ID
	Text   string
}

func chatSearchIndexCollection(st *store.Store) store.Collection[chatSearchIndex] {
	return store.NewCollection(st, store.CollectionSpec[chatSearchIndex]{
		Namespace: "chat-search-index-v2",
		GetID:     func(value chatSearchIndex) string { return value.ChatID },
		SetID:     func(value *chatSearchIndex, recordID string) { value.ChatID = id.ID(recordID) },
		Indexes: []store.IndexSpec[chatSearchIndex]{
			{Name: "chat", Value: func(value chatSearchIndex) string { return value.ChatID }},
		},
	})
}

func chatSearchDocumentCollection(st *store.Store) store.Collection[chatSearchDocument] {
	return store.NewCollection(st, store.CollectionSpec[chatSearchDocument]{
		Namespace: "chat-search-document-v2",
		GetID:     func(value chatSearchDocument) string { return value.ChatID },
		SetID:     func(value *chatSearchDocument, recordID string) { value.ChatID = id.ID(recordID) },
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
		matched, err := searchChatIndex(ctx, deps.Store, chatRecord, query)
		if err != nil {
			return nil, err
		}
		if matched {
			matches[sessionID] = true
		}
	}
	return matches, nil
}

func searchChatIndex(ctx context.Context, st *store.Store, chatRecord domain.Chat, query string) (bool, error) {
	fingerprint, err := chatSearchFingerprint(ctx, st, chatRecord)
	if err != nil {
		return false, err
	}
	stored, err := chatSearchIndexCollection(st).List(ctx, store.ByIndex[chatSearchIndex]("chat", string(chatRecord.ID)))
	if err != nil {
		return false, fmt.Errorf("load chat search index %s: %w", chatRecord.ID, err)
	}
	var index chatSearchIndex
	if len(stored) == 1 && stored[0].sameSource(fingerprint) {
		index = stored[0]
	} else {
		var document chatSearchDocument
		index, document, err = buildChatSearchIndex(ctx, st, fingerprint)
		if err != nil {
			return false, err
		}
		if err := chatSearchDocumentCollection(st).Put(ctx, document); err != nil {
			return false, fmt.Errorf("save chat search document %s: %w", chatRecord.ID, err)
		}
		if err := chatSearchIndexCollection(st).Put(ctx, index); err != nil {
			return false, fmt.Errorf("save chat search index %s: %w", chatRecord.ID, err)
		}
	}
	if !chatSearchBloomMayContain(index.Bloom, query) {
		return false, nil
	}
	document, err := chatSearchDocumentCollection(st).Get(ctx, chatRecord.ID)
	if err != nil {
		return false, fmt.Errorf("load chat search document %s: %w", chatRecord.ID, err)
	}
	return strings.Contains(document.Text, query), nil
}

func chatSearchFingerprint(ctx context.Context, st *store.Store, chatRecord domain.Chat) (chatSearchIndex, error) {
	if err := ensureTimelineSequenceIndex(ctx, st, chatRecord.ID); err != nil {
		return chatSearchIndex{}, err
	}
	latestItems, err := timelineCollection(st).TailIndex(ctx, "chat-seq", string(chatRecord.ID), 1)
	if err != nil {
		return chatSearchIndex{}, fmt.Errorf("fingerprint chat search index %s: %w", chatRecord.ID, err)
	}
	fingerprint := chatSearchIndex{ChatID: chatRecord.ID, ChatUpdatedAt: chatRecord.UpdatedAt}
	if len(latestItems) > 0 {
		latest := latestItems[len(latestItems)-1]
		fingerprint.LatestTimeline = latest.ID
		fingerprint.LatestUpdatedAt = latest.UpdatedAt
	}
	return fingerprint, nil
}

func buildChatSearchIndex(ctx context.Context, st *store.Store, index chatSearchIndex) (chatSearchIndex, chatSearchDocument, error) {
	var text strings.Builder
	after := ""
	for {
		page, err := timelineCollection(st).ListIndexPage(ctx, "chat-seq", string(index.ChatID), "", after, chatSearchIndexPageSize, false)
		if err != nil {
			return chatSearchIndex{}, chatSearchDocument{}, fmt.Errorf("build chat search index %s: %w", index.ChatID, err)
		}
		for _, item := range page.Items {
			content, err := json.Marshal(item.Content)
			if err != nil {
				return chatSearchIndex{}, chatSearchDocument{}, fmt.Errorf("encode timeline item %s for search: %w", item.ID, err)
			}
			text.Write(content)
			text.WriteByte('\n')
		}
		if !page.HasAfter || len(page.Items) == 0 {
			break
		}
		after = timelineIndexCursor(page.Items[len(page.Items)-1])
	}
	document := chatSearchDocument{ChatID: index.ChatID, Text: strings.ToLower(text.String())}
	index.Bloom = buildChatSearchBloom(document.Text)
	return index, document, nil
}

func (index chatSearchIndex) sameSource(other chatSearchIndex) bool {
	return index.ChatID == other.ChatID &&
		index.ChatUpdatedAt.Equal(other.ChatUpdatedAt) &&
		index.LatestTimeline == other.LatestTimeline &&
		index.LatestUpdatedAt.Equal(other.LatestUpdatedAt)
}

const chatSearchBloomBytes = 256 * 1024

func buildChatSearchBloom(text string) []byte {
	bloom := make([]byte, chatSearchBloomBytes)
	for index := 0; index+2 < len(text); index++ {
		chatSearchBloomAdd(bloom, text[index:index+3])
	}
	return bloom
}

func chatSearchBloomMayContain(bloom []byte, query string) bool {
	if len(query) < 3 || len(bloom) == 0 {
		return true
	}
	for index := 0; index+2 < len(query); index++ {
		if !chatSearchBloomContains(bloom, query[index:index+3]) {
			return false
		}
	}
	return true
}

func chatSearchBloomAdd(bloom []byte, gram string) {
	first, second := chatSearchBloomHashes(gram)
	for index := uint64(0); index < 4; index++ {
		bit := (first + index*second) % uint64(len(bloom)*8)
		bloom[bit/8] |= 1 << (bit % 8)
	}
}

func chatSearchBloomContains(bloom []byte, gram string) bool {
	first, second := chatSearchBloomHashes(gram)
	for index := uint64(0); index < 4; index++ {
		bit := (first + index*second) % uint64(len(bloom)*8)
		if bloom[bit/8]&(1<<(bit%8)) == 0 {
			return false
		}
	}
	return true
}

func chatSearchBloomHashes(gram string) (uint64, uint64) {
	value := uint64(gram[0])<<16 | uint64(gram[1])<<8 | uint64(gram[2])
	first := value + 0x9e3779b97f4a7c15
	first = (first ^ (first >> 30)) * 0xbf58476d1ce4e5b9
	first = (first ^ (first >> 27)) * 0x94d049bb133111eb
	first ^= first >> 31
	second := first ^ 0x517cc1b727220a95
	return first, second | 1
}

// SessionMatches is the single-session form of SearchSessions.
func (s *Source) SessionMatches(ctx context.Context, sessionID id.ID, query string) (bool, error) {
	matches, err := s.SearchSessions(ctx, []id.ID{sessionID}, query)
	return matches[sessionID], err
}
