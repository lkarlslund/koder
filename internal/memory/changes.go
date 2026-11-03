package memory

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/fsnotify/fsnotify"
)

// Snapshot is what a chat knows about memory: each memory's description and
// a hash of its whole file, keyed by scope and name.
type Snapshot map[string]SnapshotEntry

// SnapshotEntry is one memory in a Snapshot.
type SnapshotEntry struct {
	Scope       Scope
	Name        string
	Description string
	Hash        string
	path        string
}

// Snapshot reads the current state of the global memory and, with a
// project, the project's memory.
func (s *Store) Snapshot(projectRoot string) Snapshot {
	out := Snapshot{}
	for _, scope := range []Scope{ScopeGlobal, ScopeProject} {
		memories, err := s.List(scope, projectRoot)
		if err != nil {
			continue
		}
		dir, _ := s.Dir(scope, projectRoot)
		for _, memory := range memories {
			out[snapshotKey(scope, memory.Name)] = SnapshotEntry{
				Scope:       scope,
				Name:        memory.Name,
				Description: memory.Description,
				Hash:        contentHash(memory),
				path:        filepath.Join(dir, memory.Name+".md"),
			}
		}
	}
	return out
}

// ChangeNotice describes how memory changed between two snapshots, leaving
// out the changes the given chat made itself with the memory tool, since it
// already knows about them. It is empty when nothing else changed.
func (s *Store) ChangeNotice(before, after Snapshot, chatID string) string {
	keys := make([]string, 0, len(before)+len(after))
	for key := range before {
		keys = append(keys, key)
	}
	for key := range after {
		if _, ok := before[key]; !ok {
			keys = append(keys, key)
		}
	}
	slices.Sort(keys)
	var lines []string
	for _, key := range keys {
		old, had := before[key]
		now, has := after[key]
		switch {
		case had && has && old.Hash == now.Hash:
			continue
		case has && s.wroteLast(chatID, now.path, now.Hash):
			continue
		case !has && s.wroteLast(chatID, old.path, ""):
			continue
		case !had:
			lines = append(lines, fmt.Sprintf("- added %s memory %s: %s", now.Scope, now.Name, now.Description))
		case !has:
			lines = append(lines, fmt.Sprintf("- deleted %s memory %s", old.Scope, old.Name))
		case old.Description != now.Description:
			lines = append(lines, fmt.Sprintf("- updated %s memory %s, now: %s", now.Scope, now.Name, now.Description))
		default:
			lines = append(lines, fmt.Sprintf("- updated the content of %s memory %s", now.Scope, now.Name))
		}
	}
	if len(lines) == 0 {
		return ""
	}
	return "Memory changed since the memory index in your instructions was written, by another chat or by hand. This replaces the index entries named below; read a memory with the memory tool before relying on its details.\n" + strings.Join(lines, "\n")
}

// RecordWriter notes that a chat wrote a memory, or deleted it when deleted
// is true, so the change is not reported back to the chat that made it.
func (s *Store) RecordWriter(chatID string, memory Memory, projectRoot string, deleted bool) {
	if strings.TrimSpace(chatID) == "" {
		return
	}
	dir, err := s.Dir(memory.Scope, projectRoot)
	if err != nil {
		return
	}
	hash := ""
	if !deleted {
		hash = contentHash(memory)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.writers == nil {
		s.writers = map[string]memoryWriter{}
	}
	s.writers[filepath.Join(dir, memory.Name+".md")] = memoryWriter{chatID: chatID, hash: hash}
}

// wroteLast reports whether chatID made the change that left path with
// hash ("" when the memory is gone).
func (s *Store) wroteLast(chatID, path, hash string) bool {
	if chatID == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	writer, ok := s.writers[path]
	return ok && writer.chatID == chatID && writer.hash == hash
}

type memoryWriter struct {
	chatID string
	hash   string
}

// Generation counts changes to the memory folders seen by Watch or made
// through this store. It is only meaningful while Watching.
func (s *Store) Generation() uint64 {
	return s.generation.Load()
}

// Watching reports whether Watch is following the memory folders, so an
// unchanged Generation means memory is unchanged.
func (s *Store) Watching() bool {
	return s.watching.Load()
}

// Watch follows the global and project memory folders until ctx ends,
// counting every change in Generation, including files edited by hand.
func (s *Store) Watch(ctx context.Context) error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	for _, dir := range []string{s.globalDir, s.projectsDir} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			_ = watcher.Close()
			return err
		}
		if err := watcher.Add(dir); err != nil {
			_ = watcher.Close()
			return err
		}
	}
	projects, _ := os.ReadDir(s.projectsDir)
	for _, entry := range projects {
		if entry.IsDir() {
			_ = watcher.Add(filepath.Join(s.projectsDir, entry.Name()))
		}
	}
	s.watching.Store(true)
	go func() {
		defer func() {
			s.watching.Store(false)
			_ = watcher.Close()
		}()
		for {
			select {
			case <-ctx.Done():
				return
			case event, ok := <-watcher.Events:
				if !ok {
					return
				}
				if filepath.Dir(event.Name) == s.projectsDir && event.Has(fsnotify.Create) {
					if info, err := os.Stat(event.Name); err == nil && info.IsDir() {
						_ = watcher.Add(event.Name)
					}
				}
				s.generation.Add(1)
			case err, ok := <-watcher.Errors:
				if !ok {
					return
				}
				// Events may have been lost: stop vouching for Generation.
				slog.Warn("memory folder watch failed", "error", err)
				return
			}
		}
	}()
	return nil
}

func snapshotKey(scope Scope, name string) string {
	return string(scope) + "/" + name
}

func contentHash(memory Memory) string {
	sum := sha256.Sum256([]byte(memory.Description + "\x00" + strings.TrimSpace(memory.Content)))
	return hex.EncodeToString(sum[:8])
}
