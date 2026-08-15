package offeredfile

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/lkarlslund/koder/internal/id"
	"github.com/lkarlslund/koder/internal/store"
)

const tokenBytes = 24

// Record maps an opaque download capability to a live local file.
type Record struct {
	Token     string    `json:"token"`
	SessionID id.ID     `json:"session_id"`
	ChatID    id.ID     `json:"chat_id"`
	Path      string    `json:"path"`
	Name      string    `json:"name"`
	MIME      string    `json:"mime"`
	Size      int64     `json:"size"`
	Title     string    `json:"title,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// Manager owns persistent live-file capabilities.
type Manager struct {
	store *store.Store
}

func NewManager(st *store.Store) *Manager {
	return &Manager{store: st}
}

// Create persists a new unguessable capability for record.Path.
func (m *Manager) Create(ctx context.Context, record Record) (Record, error) {
	if m == nil || m.store == nil {
		return Record{}, fmt.Errorf("offered file store is required")
	}
	if record.SessionID == "" || record.ChatID == "" {
		return Record{}, fmt.Errorf("offered file requires session and chat ownership")
	}
	if strings.TrimSpace(record.Path) == "" || strings.TrimSpace(record.Name) == "" {
		return Record{}, fmt.Errorf("offered file path and name are required")
	}
	token, err := newToken()
	if err != nil {
		return Record{}, err
	}
	record.Token = token
	record.Path = strings.TrimSpace(record.Path)
	record.Name = strings.TrimSpace(record.Name)
	record.MIME = strings.TrimSpace(record.MIME)
	record.Title = strings.TrimSpace(record.Title)
	record.CreatedAt = time.Now().UTC()
	if err := offeredFiles(m.store).Put(ctx, record); err != nil {
		return Record{}, fmt.Errorf("persist offered file: %w", err)
	}
	return record, nil
}

// Resolve returns the capability record for token.
func (m *Manager) Resolve(ctx context.Context, token string) (Record, error) {
	if m == nil || m.store == nil {
		return Record{}, fmt.Errorf("offered file store is required")
	}
	token = strings.TrimSpace(token)
	if len(token) != tokenBytes*2 {
		return Record{}, fmt.Errorf("invalid offered file token")
	}
	if _, err := hex.DecodeString(token); err != nil {
		return Record{}, fmt.Errorf("invalid offered file token")
	}
	record, err := offeredFiles(m.store).Get(ctx, token)
	if err != nil {
		return Record{}, fmt.Errorf("offered file not found: %w", err)
	}
	return record, nil
}

// DeleteSession removes all capabilities owned by a deleted session.
func (m *Manager) DeleteSession(ctx context.Context, sessionID id.ID) error {
	if m == nil || m.store == nil || sessionID == "" {
		return nil
	}
	records, err := offeredFiles(m.store).List(ctx, store.ByIndex[Record]("session", string(sessionID)))
	if err != nil {
		return fmt.Errorf("list offered files for session: %w", err)
	}
	for _, record := range records {
		if err := offeredFiles(m.store).Delete(ctx, record.Token); err != nil {
			return fmt.Errorf("delete offered file %s: %w", record.Token, err)
		}
	}
	return nil
}

func offeredFiles(st *store.Store) store.Collection[Record] {
	return store.NewCollection(st, store.CollectionSpec[Record]{
		Namespace: "offered-files",
		GetID:     func(record Record) string { return record.Token },
		SetID:     func(record *Record, token string) { record.Token = token },
		Indexes: []store.IndexSpec[Record]{
			{Name: "session", Value: func(record Record) string { return string(record.SessionID) }},
		},
	})
}

func newToken() (string, error) {
	var data [tokenBytes]byte
	if _, err := rand.Read(data[:]); err != nil {
		return "", fmt.Errorf("generate offered file token: %w", err)
	}
	return hex.EncodeToString(data[:]), nil
}
