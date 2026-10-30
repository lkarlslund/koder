// Package websearch runs one web search query against any of several search
// providers and returns the results in one shape, whatever the provider.
package websearch

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"
)

// Recency limits results to pages published within a recent period.
type Recency string

const (
	AnyTime   Recency = ""
	PastDay   Recency = "day"
	PastWeek  Recency = "week"
	PastMonth Recency = "month"
	PastYear  Recency = "year"
)

// ParseRecency accepts "", day, week, month or year.
func ParseRecency(text string) (Recency, error) {
	switch r := Recency(strings.ToLower(strings.TrimSpace(text))); r {
	case AnyTime, PastDay, PastWeek, PastMonth, PastYear:
		return r, nil
	default:
		return "", fmt.Errorf("recency must be day, week, month or year, not %q", text)
	}
}

// since is the earliest publish date a recency allows, or the zero time.
func (r Recency) since(now time.Time) time.Time {
	switch r {
	case PastDay:
		return now.AddDate(0, 0, -1)
	case PastWeek:
		return now.AddDate(0, 0, -7)
	case PastMonth:
		return now.AddDate(0, -1, 0)
	case PastYear:
		return now.AddDate(-1, 0, 0)
	default:
		return time.Time{}
	}
}

// Query is one search. Domains are bare host names such as example.com.
type Query struct {
	Text           string
	Limit          int
	AllowedDomains []string
	BlockedDomains []string
	Recency        Recency
}

// Result is one search hit.
type Result struct {
	Title     string
	URL       string
	Snippet   string
	Published time.Time
}

// Settings configures one provider.
type Settings struct {
	APIKey  string
	BaseURL string
}

// MaxLimit bounds how many results one search returns.
const MaxLimit = 20

// Provider describes a search backend and runs searches against it.
type Provider struct {
	ID          string
	Title       string
	Description string
	// Pricing summarizes the free tier and price, as of late 2026.
	Pricing   string
	SignupURL string
	// NeedsKey and NeedsBaseURL say which settings must be filled in.
	NeedsKey     bool
	NeedsBaseURL bool
	// DefaultEnabled providers work with no settings, so they are on until
	// the user turns them off.
	DefaultEnabled bool
	search         func(context.Context, *http.Client, Settings, Query) ([]Result, error)
}

// Ready reports whether settings hold what the provider needs.
func (p Provider) Ready(s Settings) error {
	if p.NeedsKey && strings.TrimSpace(s.APIKey) == "" {
		return fmt.Errorf("%s needs an API key", p.Title)
	}
	if p.NeedsBaseURL && strings.TrimSpace(s.BaseURL) == "" {
		return fmt.Errorf("%s needs a base URL", p.Title)
	}
	return nil
}

// Search runs q. Whatever the provider supports natively, the results are
// then filtered by domain and recency, deduplicated and cut to q.Limit.
func (p Provider) Search(ctx context.Context, client *http.Client, s Settings, q Query) ([]Result, error) {
	if err := p.Ready(s); err != nil {
		return nil, err
	}
	if client == nil {
		client = http.DefaultClient
	}
	q.Limit = min(max(q.Limit, 1), MaxLimit)
	s.APIKey, s.BaseURL = strings.TrimSpace(s.APIKey), strings.TrimRight(strings.TrimSpace(s.BaseURL), "/")
	results, err := p.search(ctx, client, s, q)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", p.Title, err)
	}
	return tidy(results, q, time.Now()), nil
}

func tidy(results []Result, q Query, now time.Time) []Result {
	since := q.Recency.since(now)
	seen := map[string]bool{}
	out := make([]Result, 0, len(results))
	for _, r := range results {
		r.Title, r.URL, r.Snippet = oneLine(r.Title), strings.TrimSpace(r.URL), oneLine(r.Snippet)
		if r.URL == "" || seen[r.URL] || !domainAllowed(r.URL, q.AllowedDomains, q.BlockedDomains) {
			continue
		}
		// A result without a date is kept: most providers date only some pages.
		if !since.IsZero() && !r.Published.IsZero() && r.Published.Before(since) {
			continue
		}
		if r.Title == "" {
			r.Title = r.URL
		}
		seen[r.URL] = true
		out = append(out, r)
		if len(out) == q.Limit {
			break
		}
	}
	return out
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

func domainAllowed(rawURL string, allowed, blocked []string) bool {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return len(allowed) == 0
	}
	host := strings.TrimPrefix(strings.ToLower(parsed.Hostname()), "www.")
	matches := func(domain string) bool { return host == domain || strings.HasSuffix(host, "."+domain) }
	if slices.ContainsFunc(blocked, matches) {
		return false
	}
	return len(allowed) == 0 || slices.ContainsFunc(allowed, matches)
}

// withSiteOperators adds site: operators to the query text, for providers
// that take domain filters only in the query.
func withSiteOperators(q Query) string {
	parts := []string{q.Text}
	if len(q.AllowedDomains) > 0 {
		sites := make([]string, len(q.AllowedDomains))
		for i, domain := range q.AllowedDomains {
			sites[i] = "site:" + domain
		}
		if len(sites) == 1 {
			parts = append(parts, sites[0])
		} else {
			parts = append(parts, "("+strings.Join(sites, " OR ")+")")
		}
	}
	for _, domain := range q.BlockedDomains {
		parts = append(parts, "-site:"+domain)
	}
	return strings.Join(parts, " ")
}

// errorBodyBytes bounds how much of an error response is quoted.
const errorBodyBytes = 300

// maxResponseBytes bounds a provider's response.
const maxResponseBytes = 4 << 20

// doJSON sends a request with an optional JSON body and decodes a JSON
// response into out.
func doJSON(ctx context.Context, client *http.Client, method, endpoint string, header http.Header, body, out any) error {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, reader)
	if err != nil {
		return err
	}
	for key, values := range header {
		req.Header[key] = values
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	if req.Header.Get("User-Agent") == "" {
		req.Header.Set("User-Agent", "koder/1.0")
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return err
	}
	if err := statusError(resp.StatusCode, data); err != nil {
		return err
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}

// ErrRateLimited marks a provider refusing because of rate or quota limits.
var ErrRateLimited = errors.New("rate limited or out of quota")

// ErrUnauthorized marks a provider rejecting the API key.
var ErrUnauthorized = errors.New("API key rejected")

func statusError(status int, body []byte) error {
	if status >= 200 && status < 300 && status != http.StatusAccepted {
		return nil
	}
	detail := oneLine(string(body))
	if len(detail) > errorBodyBytes {
		detail = detail[:errorBodyBytes] + "…"
	}
	switch {
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return fmt.Errorf("%w (status %d): %s", ErrUnauthorized, status, detail)
	// 202 is DuckDuckGo's soft rate limit; 402 and 432 are quota errors.
	case status == http.StatusTooManyRequests || status == http.StatusPaymentRequired || status == 432 || status == http.StatusAccepted:
		return fmt.Errorf("%w (status %d): %s", ErrRateLimited, status, detail)
	default:
		return fmt.Errorf("status %d: %s", status, detail)
	}
}

func bearer(key string) http.Header {
	return http.Header{"Authorization": {"Bearer " + key}}
}

func header(name, value string) http.Header {
	return http.Header{http.CanonicalHeaderKey(name): {value}}
}

// parseDate reads the date formats providers use; it returns the zero time
// for anything else.
func parseDate(text string) time.Time {
	text = strings.TrimSpace(text)
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05", "2006-01-02 15:04:05", "2006-01-02", time.RFC1123, time.RFC1123Z, "Jan 2, 2006", "2 Jan 2006"} {
		if t, err := time.Parse(layout, text); err == nil {
			return t
		}
	}
	return time.Time{}
}

// firstNonEmpty returns the first argument that is not blank.
func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

// pickDomains splits domain filters for providers that take either an
// allow list or a block list but not both: the allow list wins, and the
// block list is then applied to the results.
func pickDomains(q Query) (include, exclude []string) {
	if len(q.AllowedDomains) > 0 {
		return q.AllowedDomains, nil
	}
	return nil, q.BlockedDomains
}

// dateSince formats the earliest allowed publish date, or "" for any time.
func dateSince(r Recency, layout string) string {
	since := r.since(time.Now())
	if since.IsZero() {
		return ""
	}
	return since.Format(layout)
}
