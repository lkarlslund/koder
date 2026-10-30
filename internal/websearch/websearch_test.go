package websearch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

type captured struct {
	method, path, query string
	header              http.Header
	body                map[string]any
}

// fakeProvider serves response and records the request.
func fakeProvider(t *testing.T, status int, response string) (*httptest.Server, *captured) {
	t.Helper()
	got := &captured{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.method, got.path, got.query, got.header = r.Method, r.URL.Path, r.URL.RawQuery, r.Header.Clone()
		if data, _ := io.ReadAll(r.Body); len(data) > 0 {
			if err := json.Unmarshal(data, &got.body); err != nil {
				t.Errorf("request body is not JSON: %s", data)
			}
		}
		w.WriteHeader(status)
		_, _ = io.WriteString(w, response)
	}))
	t.Cleanup(server.Close)
	return server, got
}

func TestEveryProviderMapsQueryAndResults(t *testing.T) {
	recent := time.Now().AddDate(0, 0, -2).Format(time.DateOnly)
	for _, tc := range []struct {
		id, response string
		// check inspects the request the adapter sent.
		check func(t *testing.T, got *captured)
	}{
		{"exa", `{"results":[{"title":"Go docs","url":"https://go.dev/doc","highlights":["Go is a language"],"publishedDate":"` + recent + `"}]}`, func(t *testing.T, got *captured) {
			wantHeader(t, got, "X-Api-Key", "secret")
			wantBody(t, got, "query", "golang generics")
			wantBody(t, got, "numResults", float64(3))
			wantBody(t, got, "includeDomains", []any{"go.dev"})
			if got.body["startPublishedDate"] == nil {
				t.Error("no date filter")
			}
		}},
		{"brave", `{"web":{"results":[{"title":"<strong>Go</strong> docs","url":"https://go.dev/doc","description":"Go is a <strong>language</strong>","page_age":"` + recent + `T10:00:00"}]}}`, func(t *testing.T, got *captured) {
			wantHeader(t, got, "X-Subscription-Token", "secret")
			wantQuery(t, got, "q=golang+generics+site%3Ago.dev")
			wantQuery(t, got, "freshness=pw")
		}},
		{"tavily", `{"results":[{"title":"Go docs","url":"https://go.dev/doc","content":"Go is a language","published_date":"` + recent + `"}]}`, func(t *testing.T, got *captured) {
			wantHeader(t, got, "Authorization", "Bearer secret")
			wantBody(t, got, "search_depth", "basic")
			wantBody(t, got, "time_range", "week")
			wantBody(t, got, "include_domains", []any{"go.dev"})
		}},
		{"parallel", `{"search_id":"s","results":[{"title":"Go docs","url":"https://go.dev/doc","excerpts":["Go is a language"],"publish_date":"` + recent + `"}]}`, func(t *testing.T, got *captured) {
			wantHeader(t, got, "X-Api-Key", "secret")
			wantBody(t, got, "search_queries", []any{"golang generics"})
			settings, _ := got.body["advanced_settings"].(map[string]any)
			policy, _ := settings["source_policy"].(map[string]any)
			if settings["max_results"] != float64(3) || policy["after_date"] == nil {
				t.Errorf("advanced_settings = %v", settings)
			}
		}},
		{"perplexity", `{"results":[{"title":"Go docs","url":"https://go.dev/doc","snippet":"Go is a language","date":"` + recent + `"}]}`, func(t *testing.T, got *captured) {
			wantBody(t, got, "search_domain_filter", []any{"go.dev"})
			wantBody(t, got, "search_recency_filter", "week")
		}},
		{"serper", `{"organic":[{"title":"Go docs","link":"https://go.dev/doc","snippet":"Go is a language"}]}`, func(t *testing.T, got *captured) {
			wantHeader(t, got, "X-Api-Key", "secret")
			wantBody(t, got, "q", "golang generics site:go.dev")
			wantBody(t, got, "tbs", "qdr:w")
		}},
		{"you", `{"results":{"web":[{"title":"Go docs","url":"https://go.dev/doc","description":"Go is a language","page_age":"` + recent + `T00:00:00"}]}}`, func(t *testing.T, got *captured) {
			wantHeader(t, got, "X-Api-Key", "secret")
			wantBody(t, got, "freshness", "week")
			wantBody(t, got, "include_domains", []any{"go.dev"})
			if _, both := got.body["exclude_domains"]; both {
				t.Error("sent include and exclude domains together")
			}
		}},
		{"firecrawl", `{"success":true,"data":{"web":[{"title":"Go docs","url":"https://go.dev/doc","description":"Go is a language"}]}}`, func(t *testing.T, got *captured) {
			wantBody(t, got, "query", "golang generics site:go.dev")
			wantBody(t, got, "limit", float64(3))
		}},
		{"linkup", `{"results":[{"type":"text","name":"Go docs","url":"https://go.dev/doc","content":"Go is a language"}]}`, func(t *testing.T, got *captured) {
			wantBody(t, got, "outputType", "searchResults")
			wantBody(t, got, "includeDomains", []any{"go.dev"})
		}},
		{"mojeek", `{"response":{"status":"OK","results":[{"title":"Go docs","url":"https://go.dev/doc","desc":"Go is a language","pdate":` + itoa(time.Now().AddDate(0, 0, -2).Unix()) + `}]}}`, func(t *testing.T, got *captured) {
			wantQuery(t, got, "api_key=secret")
			wantQuery(t, got, "fmt=json")
		}},
		{"jina", `{"code":200,"data":[{"title":"Go docs","url":"https://go.dev/doc","description":"Go is a language"}]}`, func(t *testing.T, got *captured) {
			wantHeader(t, got, "X-Respond-With", "no-content")
			wantQuery(t, got, "q=golang+generics+site%3Ago.dev")
		}},
		{"ollama", `{"results":[{"title":"Go docs","url":"https://go.dev/doc","content":"Go is a language"}]}`, func(t *testing.T, got *captured) {
			wantBody(t, got, "max_results", float64(3))
		}},
		{"searxng", `{"results":[{"title":"Go docs","url":"https://go.dev/doc","content":"Go is a language","publishedDate":"` + recent + `T00:00:00"}]}`, func(t *testing.T, got *captured) {
			wantQuery(t, got, "format=json")
			wantQuery(t, got, "time_range=month")
		}},
		{"duckduckgo", `<a class="result__a" href="https://duckduckgo.com/l/?uddg=https%3A%2F%2Fgo.dev%2Fdoc">Go docs</a></h2><a class="result__snippet" href="//duckduckgo.com/l/?uddg=x"><b>Go</b> is a language</a></div>`, func(t *testing.T, got *captured) {
			wantQuery(t, got, "df=w")
		}},
	} {
		t.Run(tc.id, func(t *testing.T) {
			provider, ok := Lookup(tc.id)
			if !ok {
				t.Fatalf("no provider %q", tc.id)
			}
			server, got := fakeProvider(t, http.StatusOK, tc.response)
			results, err := provider.Search(context.Background(), server.Client(), Settings{APIKey: "secret", BaseURL: server.URL}, Query{
				Text: "golang generics", Limit: 3, AllowedDomains: []string{"go.dev"}, Recency: PastWeek,
			})
			if err != nil {
				t.Fatal(err)
			}
			if len(results) != 1 || results[0].Title != "Go docs" || results[0].URL != "https://go.dev/doc" || !strings.Contains(results[0].Snippet, "Go is a") {
				t.Fatalf("results = %+v", results)
			}
			tc.check(t, got)
		})
	}
	if len(catalog) != 14 {
		t.Fatalf("the table covers 14 providers, the catalog has %d", len(catalog))
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

func wantHeader(t *testing.T, got *captured, name, want string) {
	t.Helper()
	if value := got.header.Get(name); value != want {
		t.Errorf("header %s = %q, want %q", name, value, want)
	}
}

func wantQuery(t *testing.T, got *captured, part string) {
	t.Helper()
	if !strings.Contains(got.query, part) {
		t.Errorf("query %q lacks %q", got.query, part)
	}
}

func wantBody(t *testing.T, got *captured, key string, want any) {
	t.Helper()
	gotJSON, _ := json.Marshal(got.body[key])
	wantJSON, _ := json.Marshal(want)
	if string(gotJSON) != string(wantJSON) {
		t.Errorf("body %s = %s, want %s", key, gotJSON, wantJSON)
	}
}

func TestSearchFiltersDomainsDatesAndDuplicates(t *testing.T) {
	old := time.Now().AddDate(-2, 0, 0)
	results := tidy([]Result{
		{Title: "a", URL: "https://docs.example.com/a"},
		{Title: "dup", URL: "https://docs.example.com/a"},
		{Title: "blocked", URL: "https://ads.example.com/x"},
		{Title: "other", URL: "https://other.org/"},
		{Title: "old", URL: "https://example.com/old", Published: old},
		{Title: "  spaced\n title ", URL: "https://www.example.com/b"},
		{URL: "https://example.com/untitled"},
	}, Query{Limit: 10, AllowedDomains: []string{"example.com"}, BlockedDomains: []string{"ads.example.com"}, Recency: PastMonth}, time.Now())
	var titles []string
	for _, r := range results {
		titles = append(titles, r.Title)
	}
	if got := strings.Join(titles, "|"); got != "a|spaced title|https://example.com/untitled" {
		t.Fatalf("titles = %q", got)
	}
}

func TestProviderErrorsSayWhy(t *testing.T) {
	tavily, _ := Lookup("tavily")
	if _, err := tavily.Search(context.Background(), nil, Settings{}, Query{Text: "x"}); err == nil || !strings.Contains(err.Error(), "needs an API key") {
		t.Fatalf("missing key: %v", err)
	}
	for status, want := range map[int]error{http.StatusUnauthorized: ErrUnauthorized, http.StatusTooManyRequests: ErrRateLimited, http.StatusPaymentRequired: ErrQuotaExhausted, 432: ErrQuotaExhausted} {
		server, _ := fakeProvider(t, status, `{"detail":"no"}`)
		_, err := tavily.Search(context.Background(), server.Client(), Settings{APIKey: "k", BaseURL: server.URL}, Query{Text: "x"})
		if !errors.Is(err, want) || !strings.HasPrefix(err.Error(), "Tavily: ") {
			t.Errorf("status %d: %v", status, err)
		}
	}
	searxng, _ := Lookup("searxng")
	if err := searxng.Ready(Settings{}); err == nil {
		t.Fatal("SearXNG without a base URL is ready")
	}
}

func TestSiteOperators(t *testing.T) {
	q := Query{Text: "x", AllowedDomains: []string{"a.com", "b.org"}, BlockedDomains: []string{"c.net"}}
	if got := withSiteOperators(q); got != "x (site:a.com OR site:b.org) -site:c.net" {
		t.Fatalf("query = %q", got)
	}
}

func TestBackoffGrowsAndClears(t *testing.T) {
	b := &backoff{entries: map[string]backoffEntry{}}
	now := time.Now()
	key := backoffKey("exa", Settings{APIKey: "k"})
	quota := fmt.Errorf("Exa: %w", ErrQuotaExhausted)

	b.record(key, quota, now)
	if until, reason := b.waiting(key, now); !errors.Is(reason, ErrQuotaExhausted) || !until.Equal(now.Add(time.Hour)) {
		t.Fatalf("first refusal: %v until %v", reason, until)
	}
	b.record(key, quota, now)
	if until, _ := b.waiting(key, now); !until.Equal(now.Add(2 * time.Hour)) {
		t.Fatalf("second refusal waits until %v", until)
	}
	for range 10 {
		b.record(key, quota, now)
	}
	if until, _ := b.waiting(key, now); !until.Equal(now.Add(24 * time.Hour)) {
		t.Fatalf("waits are capped, got %v", until)
	}
	if _, reason := b.waiting(backoffKey("exa", Settings{APIKey: "new key"}), now); reason != nil {
		t.Fatal("a new key inherited the old key's backoff")
	}
	if _, reason := b.waiting(key, now.Add(25*time.Hour)); reason != nil {
		t.Fatal("the backoff did not expire")
	}
	b.record(key, errors.New("connection reset"), now)
	if _, reason := b.waiting(key, now); reason == nil {
		t.Fatal("an ordinary failure cleared the backoff")
	}
	b.record(key, nil, now)
	if _, reason := b.waiting(key, now); reason != nil {
		t.Fatal("success did not clear the backoff")
	}
}

func TestBackoffHonoursRetryAfter(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "90")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	t.Cleanup(server.Close)
	tavily, _ := Lookup("tavily")
	backend := Backend{Provider: tavily, Settings: Settings{APIKey: "k", BaseURL: server.URL}}
	before := time.Now()
	if _, err := tavily.Search(context.Background(), server.Client(), backend.Settings, Query{Text: "x"}); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("err = %v", err)
	}
	until, reason := backend.BackingOff(before)
	if !errors.Is(reason, ErrRateLimited) || until.Before(before.Add(90*time.Second)) || until.After(time.Now().Add(90*time.Second)) {
		t.Fatalf("backing off for %v until %v", reason, until)
	}
}
