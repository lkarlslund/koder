package claudedriver

import (
	"context"
	"time"

	"github.com/lkarlslund/koder/internal/id"
	"github.com/lkarlslund/koder/internal/store"
)

// Binding records the Claude Code session that holds a chat's conversation.
type Binding struct {
	ChatID    id.ID     `json:"chat_id"`
	SessionID string    `json:"session_id"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type bindingStore struct {
	records store.Collection[Binding]
}

func newBindingStore(st *store.Store) bindingStore {
	return bindingStore{records: store.NewCollection(st, store.CollectionSpec[Binding]{
		Namespace: "claude-session-bindings",
		GetID:     func(value Binding) string { return string(value.ChatID) },
		SetID:     func(value *Binding, key string) { value.ChatID = id.ID(key) },
	})}
}

func (s bindingStore) find(ctx context.Context, chatID id.ID) (Binding, bool, error) {
	records, err := s.records.List(ctx, store.Query[Binding]{Filter: func(value Binding) bool { return value.ChatID == chatID }})
	if err != nil || len(records) == 0 {
		return Binding{}, false, err
	}
	return records[0], true, nil
}

func (s bindingStore) put(ctx context.Context, value Binding) error {
	now := time.Now().UTC()
	if value.CreatedAt.IsZero() {
		value.CreatedAt = now
	}
	value.UpdatedAt = now
	return s.records.Put(ctx, value)
}

func (s bindingStore) delete(ctx context.Context, chatID id.ID) error {
	return s.records.Delete(ctx, string(chatID))
}
