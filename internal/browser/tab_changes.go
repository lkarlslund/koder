package browser

import (
	"sync"
	"time"
)

// tabChangeSettle groups the changes of one operation, such as opening a
// tab and loading its page, into one report.
const tabChangeSettle = 150 * time.Millisecond

// tabChanges reports that the set of tabs, their selection or their pages
// changed, so the web UI can list a chat's tabs as they open, load and
// close rather than on its next poll.
type tabChanges struct {
	mu    sync.Mutex
	fn    func()
	timer *time.Timer
}

// OnTabsChange sets a function called shortly after tabs open, close, are
// claimed or selected, or load a page.
func (m *Manager) OnTabsChange(fn func()) {
	m.changes.mu.Lock()
	defer m.changes.mu.Unlock()
	m.changes.fn = fn
}

// tabsChanged schedules a report. It takes no Manager lock, so it is safe
// to call with m.mu held.
func (m *Manager) tabsChanged() {
	m.changes.mu.Lock()
	defer m.changes.mu.Unlock()
	if m.changes.fn == nil || m.changes.timer != nil {
		return
	}
	m.changes.timer = time.AfterFunc(tabChangeSettle, func() {
		m.changes.mu.Lock()
		fn := m.changes.fn
		m.changes.timer = nil
		m.changes.mu.Unlock()
		if fn != nil {
			fn()
		}
	})
}
