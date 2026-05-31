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
	mu     sync.Mutex
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
	defer closer.Close()
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
	data, closer, err := b.db.Get([]byte(driver.RecordKey(namespace, id)))
	if err != nil {
		return nil, fmt.Errorf("get %s %s: %w", namespace, id, err)
	}
	defer closer.Close()
	return driver.CloneBytes(data), nil
}

func (b *Backend) Put(ctx context.Context, namespace string, id string, data []byte, indexes map[string]string) error {
	if err := driver.EnsureContext(ctx); err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	batch := b.db.NewBatch()
	defer batch.Close()
	if err := b.deleteIndexEntries(batch, namespace, id); err != nil {
		return err
	}
	if err := batch.Set([]byte(driver.RecordKey(namespace, id)), data, nil); err != nil {
		return fmt.Errorf("put %s %s: %w", namespace, id, err)
	}
	for name, value := range indexes {
		if err := batch.Set([]byte(driver.IndexKey(namespace, name, value, id)), nil, nil); err != nil {
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
	batch := b.db.NewBatch()
	defer batch.Close()
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
	if lookup != nil {
		return b.listByIndex(namespace, lookup)
	}
	return b.listByPrefix(driver.RecordPrefix(namespace))
}

func (b *Backend) Transaction(ctx context.Context, fn func() error) error {
	if err := driver.EnsureContext(ctx); err != nil {
		return err
	}
	return fn()
}

func (b *Backend) listByPrefix(prefix string) ([][]byte, error) {
	iter, err := b.db.NewIter(&pebble.IterOptions{
		LowerBound: []byte(prefix),
		UpperBound: nextPrefix([]byte(prefix)),
	})
	if err != nil {
		return nil, err
	}
	defer iter.Close()
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
	defer iter.Close()
	var out [][]byte
	for ok := iter.First(); ok; ok = iter.Next() {
		id := strings.TrimPrefix(string(iter.Key()), prefix)
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
	defer iter.Close()
	suffix := []byte("/" + id)
	for ok := iter.First(); ok; ok = iter.Next() {
		if bytes.HasSuffix(iter.Key(), suffix) {
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
