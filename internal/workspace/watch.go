package workspace

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/fsnotify/fsnotify"
)

type Watcher struct {
	root   string
	limits watchLimits
	watch  *fsnotify.Watcher
	events chan struct{}
	done   chan struct{}
	once   sync.Once
}

var ErrWatchTreeTooLarge = errors.New("workspace tree too large to watch")

type watchLimits struct {
	MaxDirs    int
	MaxEntries int
}

var defaultWatchLimits = watchLimits{
	MaxDirs:    256,
	MaxEntries: 10000,
}

func Watch(ctx context.Context, root string) (*Watcher, error) {
	return watchWithLimits(ctx, root, defaultWatchLimits)
}

func watchWithLimits(ctx context.Context, root string, limits watchLimits) (*Watcher, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return nil, errors.New("workspace root is required")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if isBroadWatchRoot(abs) {
		return nil, ErrWatchTreeTooLarge
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
		limits: limits,
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

func isBroadWatchRoot(root string) bool {
	root = filepath.Clean(strings.TrimSpace(root))
	if root == string(filepath.Separator) {
		return true
	}
	home, err := os.UserHomeDir()
	if err == nil && filepath.Clean(home) == root {
		return true
	}
	return false
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
			_ = w.addDirTree(event.Name, &watchCounts{})
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
	var counts watchCounts
	return w.addDirTree(w.root, &counts)
}

type watchCounts struct {
	Dirs    int
	Entries int
}

func (w *Watcher) addDirTree(root string, counts *watchCounts) error {
	if counts == nil {
		counts = &watchCounts{}
	}
	return filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		counts.Entries++
		if w.limits.MaxEntries > 0 && counts.Entries > w.limits.MaxEntries {
			return ErrWatchTreeTooLarge
		}
		if !entry.IsDir() {
			return nil
		}
		if shouldSkipWatchDir(path, entry.Name()) {
			return filepath.SkipDir
		}
		counts.Dirs++
		if w.limits.MaxDirs > 0 && counts.Dirs > w.limits.MaxDirs {
			return ErrWatchTreeTooLarge
		}
		if err := w.watch.Add(path); err != nil && !errors.Is(err, fsnotify.ErrClosed) {
			return fmt.Errorf("watch %s: %w", path, err)
		}
		return nil
	})
}

func shouldSkipWatchDir(path string, name string) bool {
	switch name {
	case ".git", "node_modules", ".cache", ".direnv":
		return true
	case "objects", "logs", "modules":
		return strings.Contains(filepath.ToSlash(path), "/.git/")
	default:
		return false
	}
}
