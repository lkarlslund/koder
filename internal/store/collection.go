package store

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"log/slog"
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
	Order func(T) string
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

// Page is a bounded collection result in durable-ID order.
type Page[T any] struct {
	Items     []T
	Total     int
	HasBefore bool
	HasAfter  bool
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
	if err := c.store.backend.Delete(ctx, c.spec.Namespace, key); err != nil {
		return err
	}
	slog.Debug("store delete", "namespace", c.spec.Namespace, "id", key)
	return nil
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

// ListIndexPage returns one bounded page for an exact secondary-index value.
// Cursors are exclusive durable record IDs.
func (c Collection[T]) ListIndexPage(ctx context.Context, name, value, before, after string, limit int, tail bool) (Page[T], error) {
	if name == "" || value == "" {
		return Page[T]{}, fmt.Errorf("page %s: index name and value are required", c.spec.Namespace)
	}
	if limit <= 0 {
		return Page[T]{}, fmt.Errorf("page %s: positive limit is required", c.spec.Namespace)
	}
	if before != "" && after != "" {
		return Page[T]{}, fmt.Errorf("page %s: before and after are mutually exclusive", c.spec.Namespace)
	}
	rawPage, err := c.store.backend.ListIndexPage(ctx, c.spec.Namespace, driver.IndexPageRequest{
		Name: name, Value: value, Before: before, After: after, Limit: limit, Tail: tail,
	})
	if err != nil {
		return Page[T]{}, err
	}
	out := Page[T]{
		Items:     make([]T, 0, len(rawPage.Items)),
		Total:     rawPage.Total,
		HasBefore: rawPage.HasBefore,
		HasAfter:  rawPage.HasAfter,
	}
	for _, raw := range rawPage.Items {
		var item T
		if err := json.Unmarshal(raw, &item); err != nil {
			return Page[T]{}, fmt.Errorf("decode %s page item: %w", c.spec.Namespace, err)
		}
		if !c.matchesIndex(item, name, value) {
			continue
		}
		out.Items = append(out.Items, item)
	}
	return out, nil
}

func (c Collection[T]) put(ctx context.Context, value T) error {
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode %s: %w", c.spec.Namespace, err)
	}
	indexes := make(map[string]driver.IndexValue, len(c.spec.Indexes))
	for _, spec := range c.spec.Indexes {
		index := driver.IndexValue{Value: spec.Value(value)}
		if spec.Order != nil {
			index.Order = spec.Order(value)
		}
		indexes[spec.Name] = index
	}
	id := c.spec.GetID(value)
	if err := c.store.backend.Put(ctx, c.spec.Namespace, id, data, indexes); err != nil {
		return err
	}
	slog.Debug("store put", "namespace", c.spec.Namespace, "id", id, "bytes", len(data), "indexes", len(indexes))
	return nil
}

// AddIndexEntries builds one secondary index for existing records without
// rewriting the records themselves.
func (c Collection[T]) AddIndexEntries(ctx context.Context, name, value string, items []T) error {
	var indexSpec *IndexSpec[T]
	for idx := range c.spec.Indexes {
		if c.spec.Indexes[idx].Name == name {
			indexSpec = &c.spec.Indexes[idx]
			break
		}
	}
	if indexSpec == nil || indexSpec.Order == nil {
		return fmt.Errorf("build %s index %q: ordered index is not configured", c.spec.Namespace, name)
	}
	entries := make([]driver.OrderedIndexEntry, 0, len(items))
	for _, item := range items {
		if indexSpec.Value(item) != value {
			continue
		}
		entries = append(entries, driver.OrderedIndexEntry{ID: c.spec.GetID(item), Order: indexSpec.Order(item)})
	}
	return c.store.backend.AddIndexEntries(ctx, c.spec.Namespace, name, value, entries)
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
