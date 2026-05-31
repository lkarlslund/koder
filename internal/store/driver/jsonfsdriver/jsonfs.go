package jsonfsdriver

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/lkarlslund/koder/internal/store/driver"
)

const Name = "jsonfs"

type Backend struct {
	root string
	mu   sync.Mutex
}

func Open(stateDir string) (*Backend, error) {
	root := filepath.Join(stateDir, "store-jsonfs-v7")
	if reset, err := needsSchemaReset(root); err != nil {
		return nil, err
	} else if reset {
		if err := os.RemoveAll(root); err != nil {
			return nil, fmt.Errorf("reset jsonfs store: %w", err)
		}
	}
	if err := driver.EnsureDir(filepath.Join(root, "collections")); err != nil {
		return nil, fmt.Errorf("create jsonfs store dir: %w", err)
	}
	b := &Backend{root: root}
	if err := b.init(); err != nil {
		return nil, err
	}
	return b, nil
}

func needsSchemaReset(root string) (bool, error) {
	metaPath := filepath.Join(root, "meta.json")
	if !driver.FileExists(metaPath) {
		return false, nil
	}
	var meta driver.Meta
	if err := driver.ReadJSONFile(metaPath, &meta); err != nil {
		return false, fmt.Errorf("read jsonfs metadata before schema check: %w", err)
	}
	return meta.SchemaVersion != driver.SchemaVersion || meta.Encoding != driver.EncodingJSON || meta.Backend != Name, nil
}

func (b *Backend) init() error {
	metaPath := filepath.Join(b.root, "meta.json")
	if driver.FileExists(metaPath) {
		return nil
	}
	return driver.WriteJSONFile(metaPath, driver.DefaultMeta(Name))
}

func (b *Backend) Close() error { return nil }

func (b *Backend) Get(ctx context.Context, namespace string, id string) ([]byte, error) {
	if err := driver.EnsureContext(ctx); err != nil {
		return nil, err
	}
	path := filepath.Join(b.root, "collections", namespace, id+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("get %s %s: %w", namespace, id, err)
	}
	return data, nil
}

func (b *Backend) Put(ctx context.Context, namespace string, id string, data []byte, indexes map[string]string) error {
	if err := driver.EnsureContext(ctx); err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	dir := filepath.Join(b.root, "collections", namespace)
	if err := driver.EnsureDir(dir); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, id+".json"), append(data, '\n'), 0o644); err != nil {
		return fmt.Errorf("put %s %s: %w", namespace, id, err)
	}
	_ = indexes
	return nil
}

func (b *Backend) Delete(ctx context.Context, namespace string, id string) error {
	if err := driver.EnsureContext(ctx); err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := os.Remove(filepath.Join(b.root, "collections", namespace, id+".json")); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("delete %s %s: %w", namespace, id, err)
	}
	return nil
}

func (b *Backend) List(ctx context.Context, namespace string, lookup *driver.IndexLookup) ([][]byte, error) {
	if err := driver.EnsureContext(ctx); err != nil {
		return nil, err
	}
	dir := filepath.Join(b.root, "collections", namespace)
	paths, err := driver.SortedJSONPaths(dir)
	if err != nil {
		return nil, err
	}
	out := make([][]byte, 0, len(paths))
	for _, path := range paths {
		id := strings.TrimSuffix(filepath.Base(path), ".json")
		if lookup != nil {
			ok, err := b.indexContains(namespace, lookup.Name, lookup.Value, id)
			if err != nil {
				return nil, err
			}
			if !ok {
				continue
			}
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		out = append(out, data)
	}
	return out, nil
}

func (b *Backend) Transaction(ctx context.Context, fn func() error) error {
	if err := driver.EnsureContext(ctx); err != nil {
		return err
	}
	return fn()
}

func (b *Backend) indexContains(namespace, name, value string, id string) (bool, error) {
	_ = namespace
	_ = name
	_ = value
	_ = id
	return true, nil
}
