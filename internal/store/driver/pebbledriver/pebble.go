package pebbledriver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/cockroachdb/pebble"

	"github.com/lkarlslund/koder/internal/store/driver"
)

const Name = "pebble"

type Backend struct {
	db     *pebble.DB
	mu     sync.RWMutex
	closed bool
}

func Open(stateDir string) (*Backend, error) {
	dir := filepath.Join(stateDir, "store-pebble-v7")
	if err := driver.EnsureDir(dir); err != nil {
		return nil, fmt.Errorf("create pebble store dir: %w", err)
	}
	db, err := pebble.Open(dir, &pebble.Options{Logger: silentLogger{}})
	if err != nil {
		return nil, fmt.Errorf("open pebble: %w", err)
	}
	b := &Backend{db: db}
	if err := b.init(); err != nil {
		_ = db.Close()
		return nil, err
	}
	if reset, err := b.needsSchemaReset(); err != nil {
		_ = db.Close()
		return nil, err
	} else if reset {
		_ = db.Close()
		if err := os.RemoveAll(dir); err != nil {
			return nil, fmt.Errorf("reset pebble store: %w", err)
		}
		if err := driver.EnsureDir(dir); err != nil {
			return nil, fmt.Errorf("recreate pebble store: %w", err)
		}
		db, err = pebble.Open(dir, &pebble.Options{Logger: silentLogger{}})
		if err != nil {
			return nil, fmt.Errorf("reopen pebble after reset: %w", err)
		}
		b = &Backend{db: db}
		if err := b.init(); err != nil {
			_ = db.Close()
			return nil, err
		}
	}
	return b, nil
}

type silentLogger struct{}

func (silentLogger) Infof(string, ...interface{}) {}

func (silentLogger) Fatalf(format string, args ...interface{}) {
	log.Printf("pebble fatal: "+format, args...)
}

func (b *Backend) init() error {
	_, closer, err := b.db.Get([]byte("meta/store"))
	if err == nil {
		return closer.Close()
	}
	if !errors.Is(err, pebble.ErrNotFound) {
		return fmt.Errorf("read pebble metadata: %w", err)
	}
	metaBytes, err := driver.EncodeJSON(driver.DefaultMeta(Name))
	if err != nil {
		return fmt.Errorf("encode pebble metadata: %w", err)
	}
	return b.db.Set([]byte("meta/store"), metaBytes, pebble.Sync)
}

func (b *Backend) needsSchemaReset() (bool, error) {
	var meta driver.Meta
	data, closer, err := b.db.Get([]byte("meta/store"))
	if err != nil {
		return false, fmt.Errorf("read pebble metadata: %w", err)
	}
	defer func() { _ = closer.Close() }()
	if err := json.Unmarshal(data, &meta); err != nil {
		return false, fmt.Errorf("decode pebble metadata: %w", err)
	}
	return meta.SchemaVersion != driver.SchemaVersion || meta.Encoding != driver.EncodingJSON || meta.Backend != Name, nil
}

func (b *Backend) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return nil
	}
	b.closed = true
	return b.db.Close()
}

func (b *Backend) Get(ctx context.Context, namespace string, id string) ([]byte, error) {
	if err := driver.EnsureContext(ctx); err != nil {
		return nil, err
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	if b.closed {
		return nil, pebble.ErrClosed
	}
	data, closer, err := b.db.Get([]byte(driver.RecordKey(namespace, id)))
	if err != nil {
		return nil, fmt.Errorf("get %s %s: %w", namespace, id, err)
	}
	defer func() { _ = closer.Close() }()
	return driver.CloneBytes(data), nil
}

func (b *Backend) Put(ctx context.Context, namespace string, id string, data []byte, indexes map[string]driver.IndexValue) error {
	if err := driver.EnsureContext(ctx); err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return pebble.ErrClosed
	}
	batch := b.db.NewBatch()
	defer func() { _ = batch.Close() }()
	if err := b.deleteIndexEntries(batch, namespace, id); err != nil {
		return err
	}
	if err := batch.Set([]byte(driver.RecordKey(namespace, id)), data, nil); err != nil {
		return fmt.Errorf("put %s %s: %w", namespace, id, err)
	}
	for name, index := range indexes {
		if err := batch.Set([]byte(driver.OrderedIndexKey(namespace, name, index.Value, index.Order, id)), nil, nil); err != nil {
			return fmt.Errorf("index %s %s: %w", namespace, id, err)
		}
	}
	return batch.Commit(pebble.Sync)
}

func (b *Backend) Delete(ctx context.Context, namespace string, id string) error {
	if err := driver.EnsureContext(ctx); err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return pebble.ErrClosed
	}
	batch := b.db.NewBatch()
	defer func() { _ = batch.Close() }()
	if err := b.deleteIndexEntries(batch, namespace, id); err != nil {
		return err
	}
	if err := batch.Delete([]byte(driver.RecordKey(namespace, id)), nil); err != nil && !errors.Is(err, pebble.ErrNotFound) {
		return err
	}
	return batch.Commit(pebble.Sync)
}

func (b *Backend) List(ctx context.Context, namespace string, lookup *driver.IndexLookup) ([][]byte, error) {
	if err := driver.EnsureContext(ctx); err != nil {
		return nil, err
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	if b.closed {
		return nil, pebble.ErrClosed
	}
	if lookup != nil {
		return b.listByIndex(namespace, lookup)
	}
	return b.listByPrefix(driver.RecordPrefix(namespace))
}

func (b *Backend) ListIndexPage(ctx context.Context, namespace string, req driver.IndexPageRequest) (driver.IndexPage, error) {
	if err := driver.EnsureContext(ctx); err != nil {
		return driver.IndexPage{}, err
	}
	if req.Limit <= 0 {
		return driver.IndexPage{}, fmt.Errorf("list %s index page: positive limit is required", namespace)
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	if b.closed {
		return driver.IndexPage{}, pebble.ErrClosed
	}
	prefix := driver.IndexPrefix(namespace, req.Name, req.Value)
	iter, err := b.db.NewIter(&pebble.IterOptions{
		LowerBound: []byte(prefix),
		UpperBound: nextPrefix([]byte(prefix)),
	})
	if err != nil {
		return driver.IndexPage{}, err
	}
	defer func() { _ = iter.Close() }()

	type selectedID struct {
		id       string
		position int
	}
	selected := make([]selectedID, 0, req.Limit)
	total := 0
	for ok := iter.First(); ok; ok = iter.Next() {
		if err := driver.EnsureContext(ctx); err != nil {
			return driver.IndexPage{}, err
		}
		cursor := strings.TrimPrefix(string(iter.Key()), prefix)
		itemID := driver.IDFromIndexCursor(cursor)
		position := total
		total++
		if req.Before != "" && cursor >= req.Before {
			continue
		}
		if req.After != "" && cursor <= req.After {
			continue
		}
		if req.Tail {
			if len(selected) == req.Limit {
				copy(selected, selected[1:])
				selected = selected[:len(selected)-1]
			}
			selected = append(selected, selectedID{id: itemID, position: position})
			continue
		}
		if len(selected) < req.Limit {
			selected = append(selected, selectedID{id: itemID, position: position})
		}
	}
	if err := iter.Error(); err != nil {
		return driver.IndexPage{}, err
	}

	page := driver.IndexPage{Total: total}
	if len(selected) == 0 {
		page.HasBefore = total > 0 && req.After != ""
		page.HasAfter = total > 0 && req.Before != ""
		return page, nil
	}
	page.HasBefore = selected[0].position > 0
	page.HasAfter = selected[len(selected)-1].position < total-1
	page.Items = make([][]byte, 0, len(selected))
	for _, entry := range selected {
		data, closer, err := b.db.Get([]byte(driver.RecordKey(namespace, entry.id)))
		if err != nil {
			if errors.Is(err, pebble.ErrNotFound) {
				continue
			}
			return driver.IndexPage{}, err
		}
		page.Items = append(page.Items, driver.CloneBytes(data))
		if err := closer.Close(); err != nil {
			return driver.IndexPage{}, err
		}
	}
	return page, nil
}

func (b *Backend) AddIndexEntries(ctx context.Context, namespace, name, value string, entries []driver.OrderedIndexEntry) error {
	if err := driver.EnsureContext(ctx); err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return pebble.ErrClosed
	}
	batch := b.db.NewBatch()
	defer func() { _ = batch.Close() }()
	for _, entry := range entries {
		if err := driver.EnsureContext(ctx); err != nil {
			return err
		}
		if entry.ID == "" {
			continue
		}
		key := driver.OrderedIndexKey(namespace, name, value, entry.Order, entry.ID)
		if err := batch.Set([]byte(key), nil, nil); err != nil {
			return fmt.Errorf("build index %s %s: %w", namespace, entry.ID, err)
		}
	}
	return batch.Commit(pebble.Sync)
}

func (b *Backend) listByPrefix(prefix string) ([][]byte, error) {
	iter, err := b.db.NewIter(&pebble.IterOptions{
		LowerBound: []byte(prefix),
		UpperBound: nextPrefix([]byte(prefix)),
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = iter.Close() }()
	var out [][]byte
	for ok := iter.First(); ok; ok = iter.Next() {
		out = append(out, driver.CloneBytes(iter.Value()))
	}
	return out, iter.Error()
}

func (b *Backend) listByIndex(namespace string, lookup *driver.IndexLookup) ([][]byte, error) {
	prefix := driver.IndexPrefix(namespace, lookup.Name, lookup.Value)
	iter, err := b.db.NewIter(&pebble.IterOptions{
		LowerBound: []byte(prefix),
		UpperBound: nextPrefix([]byte(prefix)),
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = iter.Close() }()
	var out [][]byte
	for ok := iter.First(); ok; ok = iter.Next() {
		id := driver.IDFromIndexCursor(strings.TrimPrefix(string(iter.Key()), prefix))
		data, closer, err := b.db.Get([]byte(driver.RecordKey(namespace, id)))
		if err != nil {
			if errors.Is(err, pebble.ErrNotFound) {
				continue
			}
			return nil, err
		}
		out = append(out, driver.CloneBytes(data))
		if err := closer.Close(); err != nil {
			return nil, err
		}
	}
	return out, iter.Error()
}

func (b *Backend) deleteIndexEntries(batch *pebble.Batch, namespace, id string) error {
	prefix := []byte("collection-index/" + namespace + "/")
	iter, err := b.db.NewIter(&pebble.IterOptions{
		LowerBound: prefix,
		UpperBound: nextPrefix(prefix),
	})
	if err != nil {
		return err
	}
	defer func() { _ = iter.Close() }()
	suffix := []byte("/" + id)
	orderedSuffix := []byte("~" + id)
	for ok := iter.First(); ok; ok = iter.Next() {
		if bytes.HasSuffix(iter.Key(), suffix) || bytes.HasSuffix(iter.Key(), orderedSuffix) {
			if err := batch.Delete(iter.Key(), nil); err != nil && !errors.Is(err, pebble.ErrNotFound) {
				return err
			}
		}
	}
	return iter.Error()
}

func nextPrefix(prefix []byte) []byte {
	out := append([]byte(nil), prefix...)
	for i := len(out) - 1; i >= 0; i-- {
		if out[i] < 0xff {
			out[i]++
			return out[:i+1]
		}
	}
	return nil
}
