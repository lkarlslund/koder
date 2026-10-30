package websearch

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"sync"
	"time"
)

// Backoff for each kind of refusal: the first wait, doubled on each refusal
// in a row up to the limit. A Retry-After hint replaces the first wait.
var backoffPolicies = []struct {
	err         error
	first, most time.Duration
}{
	{ErrQuotaExhausted, time.Hour, 24 * time.Hour},
	{ErrUnauthorized, time.Hour, 24 * time.Hour},
	{ErrRateLimited, time.Minute, 30 * time.Minute},
}

// backoff keeps providers that refused for quota, rate or key reasons out of
// rotation for a while, so web_search does not keep hammering them. It is
// keyed by provider and settings, so a new key or base URL starts afresh.
type backoff struct {
	mu      sync.Mutex
	entries map[string]backoffEntry
}

type backoffEntry struct {
	until   time.Time
	strikes int
	reason  error
}

var providerBackoff = &backoff{entries: map[string]backoffEntry{}}

func backoffKey(id string, s Settings) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(s.APIKey) + "\x00" + strings.TrimRight(strings.TrimSpace(s.BaseURL), "/")))
	return id + ":" + hex.EncodeToString(sum[:8])
}

// record updates the backoff after a search: success clears it, a refusal
// extends it, and any other failure leaves it alone.
func (b *backoff) record(key string, err error, now time.Time) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if err == nil {
		delete(b.entries, key)
		return
	}
	for _, policy := range backoffPolicies {
		if !errors.Is(err, policy.err) {
			continue
		}
		entry := b.entries[key]
		wait := policy.first << min(entry.strikes, 16)
		if hint := retryAfter(err); hint > 0 && entry.strikes == 0 {
			wait = hint
		}
		entry.strikes++
		entry.until, entry.reason = now.Add(min(wait, policy.most)), policy.err
		b.entries[key] = entry
		return
	}
}

// waiting reports when a backed-off provider may be tried again, and why.
func (b *backoff) waiting(key string, now time.Time) (time.Time, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	entry, ok := b.entries[key]
	if !ok || !now.Before(entry.until) {
		return time.Time{}, nil
	}
	return entry.until, entry.reason
}

// BackingOff reports whether the backend recently refused for quota, rate or
// key reasons, and if so until when and why.
func (b Backend) BackingOff(now time.Time) (time.Time, error) {
	return providerBackoff.waiting(backoffKey(b.Provider.ID, b.Settings), now)
}
