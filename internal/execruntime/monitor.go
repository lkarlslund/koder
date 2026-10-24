package execruntime

import (
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const (
	monitorLineBytes   = 500
	monitorBatchBytes  = 3000
	monitorBatchDelay  = 200 * time.Millisecond
	monitorBucketSize  = 10
	monitorRefill      = 2 * time.Second
	monitorFloodLimit  = 30 * time.Second
	monitorPartialMax  = 1 << 20
	monitorTruncSuffix = " …(truncated)"
)

// lineMonitor turns a lines-mode session's output into batched events. Lines
// that arrive within monitorBatchDelay share one event. A token bucket limits
// the event rate: events over the limit are dropped and counted, and output
// that stays over the limit for monitorFloodLimit calls onFlood.
type lineMonitor struct {
	mu       sync.Mutex
	now      func() time.Time
	schedule func(time.Duration, func()) func()
	deliver  func(lines string, suppressedBefore int)
	onFlood  func()

	partial     string
	lines       []string
	cancelFlush func()
	closed      bool
	flooded     bool

	tokens     float64
	refilledAt time.Time

	delivered        int
	suppressed       int
	suppressedSince  int
	suppressingSince time.Time
	lastSuppressed   time.Time
}

func newLineMonitor(deliver func(string, int), onFlood func()) *lineMonitor {
	return &lineMonitor{
		now: time.Now,
		schedule: func(delay time.Duration, fn func()) func() {
			timer := time.AfterFunc(delay, fn)
			return func() { timer.Stop() }
		},
		deliver: deliver,
		onFlood: onFlood,
		tokens:  monitorBucketSize,
	}
}

func (l *lineMonitor) feed(data string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return
	}
	l.partial += data
	for {
		idx := strings.IndexByte(l.partial, '\n')
		if idx < 0 {
			break
		}
		l.addLineLocked(l.partial[:idx])
		l.partial = l.partial[idx+1:]
	}
	if len(l.partial) > monitorPartialMax {
		l.partial = l.partial[len(l.partial)-monitorPartialMax:]
	}
	if len(l.lines) > 0 && l.cancelFlush == nil {
		l.cancelFlush = l.schedule(monitorBatchDelay, l.flush)
	}
}

func (l *lineMonitor) addLineLocked(line string) {
	line = strings.TrimSpace(strings.TrimRight(line, "\r"))
	if line == "" {
		return
	}
	l.lines = append(l.lines, truncateBytes(line, monitorLineBytes))
}

// takeLocked returns the pending lines as one capped batch.
func (l *lineMonitor) takeLocked() string {
	if l.cancelFlush != nil {
		l.cancelFlush()
		l.cancelFlush = nil
	}
	if len(l.lines) == 0 {
		return ""
	}
	batch := truncateBytes(strings.Join(l.lines, "\n"), monitorBatchBytes)
	l.lines = nil
	return batch
}

func (l *lineMonitor) flush() {
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return
	}
	batch := l.takeLocked()
	if batch == "" {
		l.mu.Unlock()
		return
	}
	now := l.now()
	if l.tryConsumeLocked(now) {
		l.delivered++
		before := l.suppressedSince
		l.suppressedSince = 0
		if !l.lastSuppressed.IsZero() && now.Sub(l.lastSuppressed) > 3*monitorRefill {
			l.suppressingSince = time.Time{}
		}
		l.mu.Unlock()
		l.deliver(batch, before)
		return
	}
	l.suppressed++
	l.suppressedSince++
	l.lastSuppressed = now
	if l.suppressingSince.IsZero() {
		l.suppressingSince = now
	}
	flood := !l.flooded && now.Sub(l.suppressingSince) > monitorFloodLimit
	if flood {
		l.flooded = true
	}
	l.mu.Unlock()
	if flood {
		l.onFlood()
	}
}

func (l *lineMonitor) tryConsumeLocked(now time.Time) bool {
	if !l.refilledAt.IsZero() {
		l.tokens = min(monitorBucketSize, l.tokens+float64(now.Sub(l.refilledAt))/float64(monitorRefill))
	}
	l.refilledAt = now
	if l.tokens < 1 {
		return false
	}
	l.tokens--
	return true
}

// close stops delivery and returns the lines not yet delivered, including a
// final line without a newline.
func (l *lineMonitor) close() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return ""
	}
	l.closed = true
	l.addLineLocked(l.partial)
	l.partial = ""
	return l.takeLocked()
}

func (l *lineMonitor) counts() (delivered, suppressed int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.delivered, l.suppressed
}

func truncateBytes(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(text[cut]) {
		cut--
	}
	return text[:cut] + monitorTruncSuffix
}
