package store

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"github.com/lkarlslund/koder/internal/store/driver"
)

// CollectionSpec describes how one typed collection is stored and indexed.
type CollectionSpec[T any] struct {
	Namespace string
	GetID     func(T) string
	SetID     func(*T, string)
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

// NewCollection returns a typed collection for spec.
func NewCollection[T any](s *Store, spec CollectionSpec[T]) Collection[T] {
	return Collection[T]{store: s, spec: spec}
}

// Get loads one record by durable ID.
func (c Collection[T]) Get(ctx context.Context, id any) (T, error) {
	var zero T
	key, err := collectionIDKey(id)
	if err != nil {
		return zero, err
	}
	raw, err := c.store.backend.Get(ctx, c.spec.Namespace, key)
	if err != nil {
		return zero, err
	}
	var out T
	if err := json.Unmarshal(raw, &out); err != nil {
		return zero, fmt.Errorf("decode %s %v: %w", c.spec.Namespace, id, err)
	}
	return out, nil
}

// Put upserts one record with its existing durable ID.
func (c Collection[T]) Put(ctx context.Context, value T) error {
	id := c.spec.GetID(value)
	if id == "" {
		return fmt.Errorf("put %s: id is required", c.spec.Namespace)
	}
	return c.put(ctx, value)
}

// Insert allocates a durable ID and stores one record.
func (c Collection[T]) Insert(ctx context.Context, value T) (T, error) {
	if c.spec.GetID(value) == "" {
		c.spec.SetID(&value, newID(time.Now().UTC()))
	}
	if err := c.put(ctx, value); err != nil {
		var zero T
		return zero, err
	}
	return value, nil
}

func newID(now time.Time) string {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	ms := uint64(now.UTC().UnixMilli())
	var b [16]byte
	b[0] = byte(ms >> 40)
	b[1] = byte(ms >> 32)
	b[2] = byte(ms >> 24)
	b[3] = byte(ms >> 16)
	b[4] = byte(ms >> 8)
	b[5] = byte(ms)
	if _, err := rand.Read(b[6:]); err != nil {
		nano := uint64(now.UTC().UnixNano())
		for i := 6; i < len(b); i++ {
			b[i] = byte(nano >> ((i - 6) * 8))
		}
	}
	b[6] = (b[6] & 0x0f) | 0x70
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		uint32(b[0])<<24|uint32(b[1])<<16|uint32(b[2])<<8|uint32(b[3]),
		uint16(b[4])<<8|uint16(b[5]),
		uint16(b[6])<<8|uint16(b[7]),
		uint16(b[8])<<8|uint16(b[9]),
		uint64(b[10])<<40|uint64(b[11])<<32|uint64(b[12])<<24|uint64(b[13])<<16|uint64(b[14])<<8|uint64(b[15]),
	)
}

// Delete removes one record by durable ID.
func (c Collection[T]) Delete(ctx context.Context, id any) error {
	key, err := collectionIDKey(id)
	if err != nil {
		return err
	}
	return c.store.backend.Delete(ctx, c.spec.Namespace, key)
}

// List returns records matching query, sorted by ID when the spec has an ID function.
func (c Collection[T]) List(ctx context.Context, query Query[T]) ([]T, error) {
	var lookup *driver.IndexLookup
	if query.Index != "" {
		lookup = &driver.IndexLookup{Name: query.Index, Value: query.Value}
	}
	rawItems, err := c.store.backend.List(ctx, c.spec.Namespace, lookup)
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
	return s.backend.Transaction(ctx, func() error {
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
	return c.store.backend.Put(ctx, c.spec.Namespace, c.spec.GetID(value), data, indexes)
}

func collectionIDKey(id any) (string, error) {
	switch typed := id.(type) {
	case string:
		if typed == "" {
			return "", fmt.Errorf("collection id is required")
		}
		return typed, nil
	default:
		return "", fmt.Errorf("unsupported collection id %T", id)
	}
}

func (c Collection[T]) matchesIndex(value T, name, want string) bool {
	for _, spec := range c.spec.Indexes {
		if spec.Name == name {
			return spec.Value(value) == want
		}
	}
	return false
}
