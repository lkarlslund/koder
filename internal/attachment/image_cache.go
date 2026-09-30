package attachment

import (
	"os"
	"slices"
	"sync"
	"time"
)

// preparedImages remembers LoadImage results so re-rendering a chat history
// does not re-read and fully decode every image on each model step. Entries
// are revalidated against the file's size and modification time.
var preparedImages = &preparedImageCache{limit: 64 << 20, entries: map[string]preparedImage{}}

type preparedImage struct {
	size    int64
	modTime time.Time
	data    []byte
	mime    string
}

type preparedImageCache struct {
	mu      sync.Mutex
	limit   int
	used    int
	entries map[string]preparedImage
	order   []string // insertion order, oldest first
}

func (c *preparedImageCache) get(path string, info os.FileInfo) ([]byte, string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[path]
	if !ok || entry.size != info.Size() || !entry.modTime.Equal(info.ModTime()) {
		return nil, "", false
	}
	return entry.data, entry.mime, true
}

func (c *preparedImageCache) put(path string, info os.FileInfo, data []byte, mime string) {
	if len(data) > c.limit {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.remove(path)
	for c.used+len(data) > c.limit && len(c.order) > 0 {
		c.remove(c.order[0])
	}
	c.entries[path] = preparedImage{size: info.Size(), modTime: info.ModTime(), data: data, mime: mime}
	c.order = append(c.order, path)
	c.used += len(data)
}

func (c *preparedImageCache) remove(path string) {
	entry, ok := c.entries[path]
	if !ok {
		return
	}
	delete(c.entries, path)
	c.used -= len(entry.data)
	c.order = slices.DeleteFunc(c.order, func(candidate string) bool { return candidate == path })
}
