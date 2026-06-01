package workspace

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/fsnotify/fsnotify"
)

type Watcher struct {
	root   string
	watch  *fsnotify.Watcher
	events chan struct{}
	done   chan struct{}
	once   sync.Once
}

func Watch(ctx context.Context, root string) (*Watcher, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return nil, errors.New("workspace root is required")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, errors.New("workspace root is not a directory")
	}
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	out := &Watcher{
		root:   abs,
		watch:  w,
		events: make(chan struct{}, 1),
		done:   make(chan struct{}),
	}
	if err := out.addExistingDirs(); err != nil {
		_ = w.Close()
		return nil, err
	}
	go out.run(ctx)
	return out, nil
}

func (w *Watcher) Events() <-chan struct{} {
	if w == nil {
		ch := make(chan struct{})
		close(ch)
		return ch
	}
	return w.events
}

func (w *Watcher) Close() error {
	if w == nil {
		return nil
	}
	var err error
	w.once.Do(func() {
		err = w.watch.Close()
		<-w.done
	})
	return err
}

func (w *Watcher) run(ctx context.Context) {
	defer close(w.events)
	defer close(w.done)
	for {
		select {
		case <-ctx.Done():
			_ = w.watch.Close()
			return
		case event, ok := <-w.watch.Events:
			if !ok {
				return
			}
			w.handle(event)
		case _, ok := <-w.watch.Errors:
			if !ok {
				return
			}
			w.emit()
		}
	}
}

func (w *Watcher) handle(event fsnotify.Event) {
	if event.Name == "" {
		return
	}
	if event.Has(fsnotify.Create) {
		if info, err := os.Stat(event.Name); err == nil && info.IsDir() {
			_ = w.addDirTree(event.Name)
		}
	}
	w.emit()
}

func (w *Watcher) emit() {
	select {
	case w.events <- struct{}{}:
	default:
	}
}

func (w *Watcher) addExistingDirs() error {
	return w.addDirTree(w.root)
}

func (w *Watcher) addDirTree(root string) error {
	return filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !entry.IsDir() {
			return nil
		}
		if shouldSkipWatchDir(path, entry.Name()) {
			return filepath.SkipDir
		}
		if err := w.watch.Add(path); err != nil && !errors.Is(err, fsnotify.ErrClosed) {
			return nil
		}
		return nil
	})
}

func shouldSkipWatchDir(path string, name string) bool {
	switch name {
	case "node_modules", ".cache", ".direnv":
		return true
	case "objects", "logs", "modules":
		return strings.Contains(filepath.ToSlash(path), "/.git/")
	default:
		return false
	}
}
