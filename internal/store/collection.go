package store

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/lkarlslund/koder/internal/domain"
)

// CollectionSpec describes how one typed collection is stored and indexed.
type CollectionSpec[T any] struct {
	Namespace string
	NextID    string
	GetID     func(T) int64
	SetID     func(*T, int64)
	Indexes   []IndexSpec[T]
}

// IndexSpec describes one secondary index for a typed collection.
type IndexSpec[T any] struct {
	Name  string
	Value func(T) string
}

// Query selects all records or the records matching one secondary index.
type Query[T any] struct {
	Index  string
	Value  string
	Filter func(T) bool
}

// All returns a collection query that scans every record.
func All[T any]() Query[T] {
	return Query[T]{}
}

// ByIndex returns a collection query for one secondary index value.
func ByIndex[T any](name, value string) Query[T] {
	return Query[T]{Index: name, Value: value}
}

// Collection is a typed view over a generic store namespace.
type Collection[T any] struct {
	store *Store
	spec  CollectionSpec[T]
}

type collectionBackend interface {
	allocateCollectionID(context.Context, string) (int64, error)
	getCollectionRecord(context.Context, string, int64) ([]byte, error)
	putCollectionRecord(context.Context, string, int64, []byte, map[string]string) error
	deleteCollectionRecord(context.Context, string, int64) error
	listCollectionRecords(context.Context, string, *indexLookup) ([][]byte, error)
	transaction(context.Context, func() error) error
}

type indexLookup struct {
	name  string
	value string
}

// NewCollection returns a typed collection for spec.
func NewCollection[T any](s *Store, spec CollectionSpec[T]) Collection[T] {
	return Collection[T]{store: s, spec: spec}
}

// Sessions returns the generic sessions collection.
func (s *Store) Sessions() Collection[domain.Session] {
	return NewCollection(s, CollectionSpec[domain.Session]{
		Namespace: "sessions",
		NextID:    "session",
		GetID:     func(v domain.Session) int64 { return v.ID },
		SetID:     func(v *domain.Session, id int64) { v.ID = id },
	})
}

// Chats returns the generic chats collection.
func (s *Store) Chats() Collection[domain.Chat] {
	return NewCollection(s, CollectionSpec[domain.Chat]{
		Namespace: "chats",
		NextID:    "chat",
		GetID:     func(v domain.Chat) int64 { return v.ID },
		SetID:     func(v *domain.Chat, id int64) { v.ID = id },
		Indexes: []IndexSpec[domain.Chat]{
			{Name: "session", Value: func(v domain.Chat) string { return fmt.Sprint(v.SessionID) }},
		},
	})
}

// Messages returns the generic messages collection.
func (s *Store) Messages() Collection[domain.Message] {
	return NewCollection(s, CollectionSpec[domain.Message]{
		Namespace: "messages",
		NextID:    "message",
		GetID:     func(v domain.Message) int64 { return v.ID },
		SetID:     func(v *domain.Message, id int64) { v.ID = id },
		Indexes: []IndexSpec[domain.Message]{
			{Name: "session", Value: func(v domain.Message) string { return fmt.Sprint(v.SessionID) }},
			{Name: "chat", Value: func(v domain.Message) string { return fmt.Sprint(v.ChatID) }},
		},
	})
}

// Parts returns the generic parts collection.
func (s *Store) Parts() Collection[domain.Part] {
	return NewCollection(s, CollectionSpec[domain.Part]{
		Namespace: "parts",
		NextID:    "part",
		GetID:     func(v domain.Part) int64 { return v.ID },
		SetID:     func(v *domain.Part, id int64) { v.ID = id },
		Indexes: []IndexSpec[domain.Part]{
			{Name: "message", Value: func(v domain.Part) string { return fmt.Sprint(v.MessageID) }},
		},
	})
}

// Approvals returns the generic approvals collection.
func (s *Store) Approvals() Collection[Approval] {
	return NewCollection(s, CollectionSpec[Approval]{
		Namespace: "approvals",
		NextID:    "approval",
		GetID:     func(v Approval) int64 { return v.ID },
		SetID:     func(v *Approval, id int64) { v.ID = id },
		Indexes: []IndexSpec[Approval]{
			{Name: "session", Value: func(v Approval) string { return fmt.Sprint(v.SessionID) }},
			{Name: "chat", Value: func(v Approval) string { return fmt.Sprint(v.ChatID) }},
			{Name: "status", Value: func(v Approval) string { return string(v.Status) }},
		},
	})
}

// Get loads one record by durable ID.
func (c Collection[T]) Get(ctx context.Context, id int64) (T, error) {
	var zero T
	raw, err := c.backend().getCollectionRecord(ctx, c.spec.Namespace, id)
	if err != nil {
		return zero, err
	}
	var out T
	if err := json.Unmarshal(raw, &out); err != nil {
		return zero, fmt.Errorf("decode %s %d: %w", c.spec.Namespace, id, err)
	}
	return out, nil
}

// Put upserts one record with its existing durable ID.
func (c Collection[T]) Put(ctx context.Context, value T) error {
	id := c.spec.GetID(value)
	if id <= 0 {
		return fmt.Errorf("put %s: id is required", c.spec.Namespace)
	}
	return c.put(ctx, value)
}

// Insert allocates a durable ID and stores one record.
func (c Collection[T]) Insert(ctx context.Context, value T) (T, error) {
	if c.spec.GetID(value) <= 0 {
		id, err := c.backend().allocateCollectionID(ctx, c.spec.NextID)
		if err != nil {
			var zero T
			return zero, err
		}
		c.spec.SetID(&value, id)
	}
	if err := c.put(ctx, value); err != nil {
		var zero T
		return zero, err
	}
	return value, nil
}

// Delete removes one record by durable ID.
func (c Collection[T]) Delete(ctx context.Context, id int64) error {
	return c.backend().deleteCollectionRecord(ctx, c.spec.Namespace, id)
}

// List returns records matching query, sorted by ID when the spec has an ID function.
func (c Collection[T]) List(ctx context.Context, query Query[T]) ([]T, error) {
	rawItems, err := c.backend().listCollectionRecords(ctx, c.spec.Namespace, nil)
	if err != nil {
		return nil, err
	}
	out := make([]T, 0, len(rawItems))
	for _, raw := range rawItems {
		var value T
		if err := json.Unmarshal(raw, &value); err != nil {
			return nil, fmt.Errorf("decode %s list item: %w", c.spec.Namespace, err)
		}
		if query.Index != "" && !c.matchesIndex(value, query.Index, query.Value) {
			continue
		}
		if query.Filter != nil && !query.Filter(value) {
			continue
		}
		out = append(out, value)
	}
	if c.spec.GetID != nil {
		slices.SortFunc(out, func(a, b T) int {
			switch {
			case c.spec.GetID(a) < c.spec.GetID(b):
				return -1
			case c.spec.GetID(a) > c.spec.GetID(b):
				return 1
			default:
				return 0
			}
		})
	}
	return out, nil
}

// Transaction groups multiple collection writes behind one store-facing operation.
func (s *Store) Transaction(ctx context.Context, fn func(*Tx) error) error {
	rb, ok := s.backend.(collectionBackend)
	if !ok {
		return fmt.Errorf("store backend does not support generic transactions")
	}
	return rb.transaction(ctx, func() error {
		return fn(&Tx{})
	})
}

// Tx marks collection writes as part of a transaction.
type Tx struct{}

// PutTx upserts one record inside a Store transaction.
func (c Collection[T]) PutTx(_ *Tx, ctx context.Context, value T) error {
	return c.Put(ctx, value)
}

// InsertTx inserts one record inside a Store transaction.
func (c Collection[T]) InsertTx(_ *Tx, ctx context.Context, value T) (T, error) {
	return c.Insert(ctx, value)
}

func (c Collection[T]) put(ctx context.Context, value T) error {
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode %s: %w", c.spec.Namespace, err)
	}
	indexes := make(map[string]string, len(c.spec.Indexes))
	for _, spec := range c.spec.Indexes {
		indexes[spec.Name] = spec.Value(value)
	}
	return c.backend().putCollectionRecord(ctx, c.spec.Namespace, c.spec.GetID(value), data, indexes)
}

func (c Collection[T]) backend() collectionBackend {
	rb, ok := c.store.backend.(collectionBackend)
	if !ok {
		panic("store backend does not support generic collections")
	}
	return rb
}

func (c Collection[T]) matchesIndex(value T, name, want string) bool {
	for _, spec := range c.spec.Indexes {
		if spec.Name == name {
			return spec.Value(value) == want
		}
	}
	return false
}
