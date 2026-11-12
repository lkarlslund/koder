package webui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"

	"github.com/lkarlslund/koder/internal/id"
)

const (
	// maxWatchedFileDirs bounds the folders one file browser page can watch.
	maxWatchedFileDirs = 256
	// fileWatchSettle groups the events of one burst into one message, and
	// fileWatchMinGap keeps a file that is written continuously from
	// reloading the page more than about once a second.
	fileWatchSettle = 250 * time.Millisecond
	fileWatchMinGap = time.Second
)

type watchFilesParams struct {
	SessionID id.ID    `json:"session_id"`
	ChatID    id.ID    `json:"chat_id,omitempty"`
	Paths     []string `json:"paths"`
}

type watchFilesResult struct {
	Paths []string `json:"paths"`
}

// filesChanged lists the watched folders whose entries changed and the
// files and folders the changes touched, relative to the project root.
type filesChanged struct {
	SessionID id.ID    `json:"session_id"`
	Dirs      []string `json:"dirs"`
	Paths     []string `json:"paths"`
}

// fileWatch watches the folders one file browser page has open, without
// recursion, so a page only pays for what it shows.
type fileWatch struct {
	send func(filesChanged)

	mu        sync.Mutex
	watcher   *fsnotify.Watcher
	sessionID id.ID
	dirs      map[string]string // absolute path -> path relative to root
	dirty     map[string]struct{}
	paths     map[string]struct{}
	timer     *time.Timer
	lastSent  time.Time
	closed    bool
}

func newFileWatch(send func(filesChanged)) *fileWatch {
	return &fileWatch{send: send}
}

// handleWatchFiles replaces the page's watched folders with the given ones.
func (s *Server) handleWatchFiles(ctx context.Context, watch *fileWatch, raw json.RawMessage) (any, error) {
	var params watchFilesParams
	if err := json.Unmarshal(raw, &params); err != nil {
		return nil, fmt.Errorf("decode watch_files: %w", err)
	}
	if params.SessionID == "" {
		return nil, errors.New("session_id is required")
	}
	if len(params.Paths) > maxWatchedFileDirs {
		return nil, fmt.Errorf("too many folders to watch: %d, limit %d", len(params.Paths), maxWatchedFileDirs)
	}
	dirs := map[string]string{}
	for _, raw := range params.Paths {
		_, rel, full, err := s.sessionFilePath(ctx, params.SessionID, raw, params.ChatID)
		if err != nil {
			return nil, err
		}
		if info, err := os.Stat(full); err != nil || !info.IsDir() {
			continue // removed since the page loaded it; the parent reports that
		}
		dirs[full] = rel
	}
	if err := watch.set(params.SessionID, dirs); err != nil {
		return nil, err
	}
	watched := make([]string, 0, len(dirs))
	for _, rel := range dirs {
		watched = append(watched, rel)
	}
	slices.Sort(watched)
	return watchFilesResult{Paths: watched}, nil
}

func (w *fileWatch) set(sessionID id.ID, dirs map[string]string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return errors.New("connection closed")
	}
	if w.watcher != nil && w.sessionID != sessionID {
		_ = w.watcher.Close()
		w.watcher = nil
		w.dirs = nil
	}
	if w.watcher == nil {
		if len(dirs) == 0 {
			return nil
		}
		watcher, err := fsnotify.NewWatcher()
		if err != nil {
			return fmt.Errorf("start file watcher: %w", err)
		}
		w.watcher = watcher
		w.dirs = map[string]string{}
		go w.run(watcher)
	}
	w.sessionID = sessionID
	for full := range w.dirs {
		if _, keep := dirs[full]; !keep {
			_ = w.watcher.Remove(full)
			delete(w.dirs, full)
		}
	}
	for full, rel := range dirs {
		if _, ok := w.dirs[full]; ok {
			w.dirs[full] = rel
			continue
		}
		if err := w.watcher.Add(full); err != nil {
			return fmt.Errorf("watch %s: %w", rel, err)
		}
		w.dirs[full] = rel
	}
	return nil
}

func (w *fileWatch) run(watcher *fsnotify.Watcher) {
	for {
		select {
		case event, ok := <-watcher.Events:
			if !ok {
				return
			}
			w.note(watcher, event)
		case _, ok := <-watcher.Errors:
			if !ok {
				return
			}
			// An overflow loses events; have the page reload every folder.
			w.noteAll(watcher)
		}
	}
}

func (w *fileWatch) note(watcher *fsnotify.Watcher, event fsnotify.Event) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.watcher != watcher {
		return
	}
	// A watched folder that is removed or renamed loses its watch. Forget
	// it, so the page's next list adds it again if it comes back.
	if rel, ok := w.dirs[event.Name]; ok && event.Op&(fsnotify.Remove|fsnotify.Rename) != 0 {
		_ = watcher.Remove(event.Name)
		delete(w.dirs, event.Name)
		w.mark(rel, "")
	}
	dirRel, ok := w.dirs[filepath.Dir(event.Name)]
	if !ok {
		return
	}
	w.mark(dirRel, path.Join(dirRel, filepath.Base(event.Name)))
}

func (w *fileWatch) noteAll(watcher *fsnotify.Watcher) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.watcher != watcher {
		return
	}
	for _, rel := range w.dirs {
		w.mark(rel, "")
	}
}

// mark records a change and schedules a message. The caller holds w.mu.
func (w *fileWatch) mark(dir, path string) {
	if w.dirty == nil {
		w.dirty = map[string]struct{}{}
		w.paths = map[string]struct{}{}
	}
	w.dirty[dir] = struct{}{}
	if path != "" {
		w.paths[path] = struct{}{}
	}
	if w.timer != nil {
		return
	}
	wait := fileWatchSettle
	if gap := fileWatchMinGap - time.Since(w.lastSent); gap > wait {
		wait = gap
	}
	w.timer = time.AfterFunc(wait, w.flush)
}

func (w *fileWatch) flush() {
	w.mu.Lock()
	w.timer = nil
	if w.closed || len(w.dirty) == 0 {
		w.mu.Unlock()
		return
	}
	msg := filesChanged{SessionID: w.sessionID, Dirs: sortedKeys(w.dirty), Paths: sortedKeys(w.paths)}
	w.dirty, w.paths = nil, nil
	w.lastSent = time.Now()
	w.mu.Unlock()
	w.send(msg)
}

func (w *fileWatch) Close() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.closed = true
	if w.timer != nil {
		w.timer.Stop()
		w.timer = nil
	}
	if w.watcher != nil {
		_ = w.watcher.Close()
		w.watcher = nil
	}
}

func sortedKeys(set map[string]struct{}) []string {
	return slices.Sorted(maps.Keys(set))
}
