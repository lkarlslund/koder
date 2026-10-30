package websearch

import (
	"context"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/lkarlslund/koder/internal/textutil"
)

// The provider catalog. Each adapter maps Query onto the provider's own
// parameters where it has them; Provider.Search filters the rest. Request and
// response fields follow each provider's API reference as of October 2026.
var catalog = []Provider{
	{
		ID: "exa", Title: "Exa", NeedsKey: true, SignupURL: "https://dashboard.exa.ai/api-keys",
		Description: "Neural web search built for AI agents, with domain and date filters.",
		Pricing:     "$10 of free credit each month, no card; about $7 per 1,000 searches after that.",
		search:      searchExa,
	},
	{
		ID: "brave", Title: "Brave Search", NeedsKey: true, SignupURL: "https://api-dashboard.search.brave.com/",
		Description: "Brave's own independent web index.",
		Pricing:     "$5 per 1,000 searches with $5 of monthly credit; a card is required.",
		search:      searchBrave,
	},
	{
		ID: "tavily", Title: "Tavily", NeedsKey: true, SignupURL: "https://app.tavily.com/",
		Description: "Search API for AI agents, with domain and date filters.",
		Pricing:     "1,000 free searches a month, no card; about $8 per 1,000 after that.",
		search:      searchTavily,
	},
	{
		ID: "parallel", Title: "Parallel", NeedsKey: true, SignupURL: "https://platform.parallel.ai/",
		Description: "Search API returning long, model-ready excerpts of each page.",
		Pricing:     "Free signup credit and $5 a month; $1 per 1,000 searches in fast mode.",
		search:      searchParallel,
	},
	{
		ID: "perplexity", Title: "Perplexity Search", NeedsKey: true, SignupURL: "https://www.perplexity.ai/account/api",
		Description: "Ranked web results from Perplexity's index.",
		Pricing:     "$5 per 1,000 searches; no free tier.",
		search:      searchPerplexity,
	},
	{
		ID: "serper", Title: "Serper", NeedsKey: true, SignupURL: "https://serper.dev/",
		Description: "Google results through a cheap SERP API.",
		Pricing:     "2,500 free searches once, no card; $0.30-$1 per 1,000 after that.",
		search:      searchSerper,
	},
	{
		ID: "you", Title: "You.com", NeedsKey: true, SignupURL: "https://you.com/platform",
		Description: "You.com's web index with domain and freshness filters.",
		Pricing:     "Free credit at signup; paid per search after that.",
		search:      searchYou,
	},
	{
		ID: "firecrawl", Title: "Firecrawl", NeedsKey: true, SignupURL: "https://www.firecrawl.dev/app/api-keys",
		Description: "Web search from the Firecrawl scraping service.",
		Pricing:     "1,000 free credits a month, no card; 2 credits per 10 results.",
		search:      searchFirecrawl,
	},
	{
		ID: "linkup", Title: "Linkup", NeedsKey: true, SignupURL: "https://app.linkup.so/",
		Description: "Search API for AI agents, with domain and date filters.",
		Pricing:     "Monthly free credit (card required); about $5 per 1,000 searches.",
		search:      searchLinkup,
	},
	{
		ID: "mojeek", Title: "Mojeek", NeedsKey: true, SignupURL: "https://www.mojeek.com/services/search/web-search-api/",
		Description: "Mojeek's own independent, privacy-focused web index.",
		Pricing:     "About £2-3 per 1,000 searches; the free trial needs a card.",
		search:      searchMojeek,
	},
	{
		ID: "jina", Title: "Jina Search", NeedsKey: true, SignupURL: "https://jina.ai/api-dashboard/",
		Description: "Jina's s.jina.ai search endpoint.",
		Pricing:     "10 million free tokens per new key; each search costs about 10,000 tokens.",
		search:      searchJina,
	},
	{
		ID: "ollama", Title: "Ollama web search", NeedsKey: true, SignupURL: "https://ollama.com/settings/keys",
		Description: "Ollama's hosted web search, free with an Ollama account. No filters.",
		Pricing:     "Free with an account, within undocumented limits.",
		search:      searchOllama,
	},
	{
		ID: "searxng", Title: "SearXNG", NeedsBaseURL: true, SignupURL: "https://docs.searxng.org/admin/installation.html",
		Description: "Your own SearXNG metasearch instance. Its settings.yml must list json under search.formats.",
		Pricing:     "Free; self-hosted.",
		search:      searchSearXNG,
	},
	{
		ID: "duckduckgo", Title: "DuckDuckGo", DefaultEnabled: true,
		Description: "DuckDuckGo's HTML results page. Works without setup, but is unofficial and rate-limits busy or datacenter addresses.",
		Pricing:     "Free.",
		search:      searchDuckDuckGo,
	},
}

// Providers returns every provider, in the default order of preference:
// keyed APIs first, then the keyless fallbacks.
func Providers() []Provider {
	return append([]Provider(nil), catalog...)
}

// Lookup finds a provider by ID.
func Lookup(id string) (Provider, bool) {
	for _, p := range catalog {
		if p.ID == id {
			return p, true
		}
	}
	return Provider{}, false
}

func searchExa(ctx context.Context, client *http.Client, s Settings, q Query) ([]Result, error) {
	include, exclude := pickDomains(q)
	body := map[string]any{
		"query": q.Text, "numResults": q.Limit, "type": "auto",
		"contents": map[string]any{"highlights": map[string]any{"maxCharacters": 400}},
	}
	if len(include) > 0 {
		body["includeDomains"] = include
	}
	if len(exclude) > 0 {
		body["excludeDomains"] = exclude
	}
	if since := dateSince(q.Recency, time.RFC3339); since != "" {
		body["startPublishedDate"] = since
	}
	var resp struct {
		Results []struct {
			Title, URL, Text, PublishedDate string
			Highlights                      []string
		}
	}
	if err := doJSON(ctx, client, http.MethodPost, firstNonEmpty(s.BaseURL, "https://api.exa.ai")+"/search", header("x-api-key", s.APIKey), body, &resp); err != nil {
		return nil, err
	}
	out := make([]Result, 0, len(resp.Results))
	for _, r := range resp.Results {
		out = append(out, Result{Title: r.Title, URL: r.URL, Snippet: firstNonEmpty(strings.Join(r.Highlights, " … "), r.Text), Published: parseDate(r.PublishedDate)})
	}
	return out, nil
}

var braveFreshness = map[Recency]string{PastDay: "pd", PastWeek: "pw", PastMonth: "pm", PastYear: "py"}

func searchBrave(ctx context.Context, client *http.Client, s Settings, q Query) ([]Result, error) {
	params := url.Values{"q": {withSiteOperators(q)}, "count": {strconv.Itoa(q.Limit)}}
	if freshness := braveFreshness[q.Recency]; freshness != "" {
		params.Set("freshness", freshness)
	}
	var resp struct {
		Web struct {
			Results []struct {
				Title, URL, Description string
				ExtraSnippets           []string `json:"extra_snippets"`
				PageAge                 string   `json:"page_age"`
			}
		}
	}
	endpoint := firstNonEmpty(s.BaseURL, "https://api.search.brave.com") + "/res/v1/web/search?" + params.Encode()
	if err := doJSON(ctx, client, http.MethodGet, endpoint, header("X-Subscription-Token", s.APIKey), nil, &resp); err != nil {
		return nil, err
	}
	out := make([]Result, 0, len(resp.Web.Results))
	for _, r := range resp.Web.Results {
		out = append(out, Result{Title: stripTags(r.Title), URL: r.URL, Snippet: stripTags(r.Description), Published: parseDate(r.PageAge)})
	}
	return out, nil
}

func searchTavily(ctx context.Context, client *http.Client, s Settings, q Query) ([]Result, error) {
	// search_depth is set so Tavily never upgrades to the costlier depth.
	body := map[string]any{"query": q.Text, "max_results": q.Limit, "search_depth": "basic"}
	if len(q.AllowedDomains) > 0 {
		body["include_domains"] = q.AllowedDomains
	}
	if len(q.BlockedDomains) > 0 {
		body["exclude_domains"] = q.BlockedDomains
	}
	if q.Recency != AnyTime {
		body["time_range"] = string(q.Recency)
	}
	var resp struct {
		Results []struct {
			Title, URL, Content string
			PublishedDate       string `json:"published_date"`
		}
	}
	if err := doJSON(ctx, client, http.MethodPost, firstNonEmpty(s.BaseURL, "https://api.tavily.com")+"/search", bearer(s.APIKey), body, &resp); err != nil {
		return nil, err
	}
	out := make([]Result, 0, len(resp.Results))
	for _, r := range resp.Results {
		out = append(out, Result{Title: r.Title, URL: r.URL, Snippet: r.Content, Published: parseDate(r.PublishedDate)})
	}
	return out, nil
}

// parallelExcerptChars keeps Parallel's excerpts snippet-sized; they are
// whole page sections otherwise.
const parallelExcerptChars = 600

func searchParallel(ctx context.Context, client *http.Client, s Settings, q Query) ([]Result, error) {
	// Parallel rejects unknown fields, so only documented ones are sent.
	settings := map[string]any{
		"max_results":      q.Limit,
		"excerpt_settings": map[string]any{"max_chars_per_result": parallelExcerptChars},
	}
	policy := map[string]any{}
	include, exclude := pickDomains(q)
	if len(include) > 0 {
		policy["include_domains"] = include
	}
	if len(exclude) > 0 {
		policy["exclude_domains"] = exclude
	}
	if since := dateSince(q.Recency, time.DateOnly); since != "" {
		policy["after_date"] = since
	}
	if len(policy) > 0 {
		settings["source_policy"] = policy
	}
	body := map[string]any{"objective": q.Text, "search_queries": []string{q.Text}, "mode": "fast", "advanced_settings": settings}
	var resp struct {
		Results []struct {
			Title, URL  string
			PublishDate string `json:"publish_date"`
			Excerpts    []string
		}
	}
	if err := doJSON(ctx, client, http.MethodPost, firstNonEmpty(s.BaseURL, "https://api.parallel.ai")+"/v1/search", header("x-api-key", s.APIKey), body, &resp); err != nil {
		return nil, err
	}
	out := make([]Result, 0, len(resp.Results))
	for _, r := range resp.Results {
		out = append(out, Result{Title: r.Title, URL: r.URL, Snippet: strings.Join(r.Excerpts, " … "), Published: parseDate(r.PublishDate)})
	}
	return out, nil
}

func searchPerplexity(ctx context.Context, client *http.Client, s Settings, q Query) ([]Result, error) {
	body := map[string]any{"query": q.Text, "max_results": q.Limit}
	// The domain filter is an allow list of up to 20; blocked domains are
	// filtered from the results instead.
	if len(q.AllowedDomains) > 0 {
		body["search_domain_filter"] = q.AllowedDomains[:min(len(q.AllowedDomains), 20)]
	}
	if q.Recency != AnyTime {
		body["search_recency_filter"] = string(q.Recency)
	}
	var resp struct {
		Results []struct{ Title, URL, Snippet, Date string }
	}
	if err := doJSON(ctx, client, http.MethodPost, firstNonEmpty(s.BaseURL, "https://api.perplexity.ai")+"/search", bearer(s.APIKey), body, &resp); err != nil {
		return nil, err
	}
	out := make([]Result, 0, len(resp.Results))
	for _, r := range resp.Results {
		out = append(out, Result{Title: r.Title, URL: r.URL, Snippet: r.Snippet, Published: parseDate(r.Date)})
	}
	return out, nil
}

// googleTBS is Google's time filter, which Serper and Firecrawl pass through.
var googleTBS = map[Recency]string{PastDay: "qdr:d", PastWeek: "qdr:w", PastMonth: "qdr:m", PastYear: "qdr:y"}

func searchSerper(ctx context.Context, client *http.Client, s Settings, q Query) ([]Result, error) {
	body := map[string]any{"q": withSiteOperators(q), "num": q.Limit}
	if tbs := googleTBS[q.Recency]; tbs != "" {
		body["tbs"] = tbs
	}
	var resp struct {
		Organic []struct{ Title, Link, Snippet, Date string }
	}
	if err := doJSON(ctx, client, http.MethodPost, firstNonEmpty(s.BaseURL, "https://google.serper.dev")+"/search", header("X-API-KEY", s.APIKey), body, &resp); err != nil {
		return nil, err
	}
	out := make([]Result, 0, len(resp.Organic))
	for _, r := range resp.Organic {
		out = append(out, Result{Title: r.Title, URL: r.Link, Snippet: r.Snippet, Published: parseDate(r.Date)})
	}
	return out, nil
}

func searchYou(ctx context.Context, client *http.Client, s Settings, q Query) ([]Result, error) {
	body := map[string]any{"query": q.Text, "count": q.Limit}
	// include_domains and exclude_domains together are rejected.
	include, exclude := pickDomains(q)
	if len(include) > 0 {
		body["include_domains"] = include
	}
	if len(exclude) > 0 {
		body["exclude_domains"] = exclude
	}
	if q.Recency != AnyTime {
		body["freshness"] = string(q.Recency)
	}
	var resp struct {
		Results struct {
			Web []struct {
				Title, URL, Description string
				Snippets                []string
				PageAge                 string `json:"page_age"`
			}
		}
	}
	if err := doJSON(ctx, client, http.MethodPost, firstNonEmpty(s.BaseURL, "https://ydc-index.io")+"/v1/search", header("X-API-Key", s.APIKey), body, &resp); err != nil {
		return nil, err
	}
	out := make([]Result, 0, len(resp.Results.Web))
	for _, r := range resp.Results.Web {
		out = append(out, Result{Title: r.Title, URL: r.URL, Snippet: firstNonEmpty(r.Description, strings.Join(r.Snippets, " … ")), Published: parseDate(r.PageAge)})
	}
	return out, nil
}

func searchFirecrawl(ctx context.Context, client *http.Client, s Settings, q Query) ([]Result, error) {
	body := map[string]any{"query": withSiteOperators(q), "limit": q.Limit}
	if tbs := googleTBS[q.Recency]; tbs != "" {
		body["tbs"] = tbs
	}
	var resp struct {
		Data struct {
			Web []struct{ Title, URL, Description string }
		}
	}
	if err := doJSON(ctx, client, http.MethodPost, firstNonEmpty(s.BaseURL, "https://api.firecrawl.dev")+"/v2/search", bearer(s.APIKey), body, &resp); err != nil {
		return nil, err
	}
	out := make([]Result, 0, len(resp.Data.Web))
	for _, r := range resp.Data.Web {
		out = append(out, Result{Title: r.Title, URL: r.URL, Snippet: r.Description})
	}
	return out, nil
}

func searchLinkup(ctx context.Context, client *http.Client, s Settings, q Query) ([]Result, error) {
	body := map[string]any{"q": q.Text, "depth": "standard", "outputType": "searchResults", "maxResults": q.Limit}
	if len(q.AllowedDomains) > 0 {
		body["includeDomains"] = q.AllowedDomains
	}
	if len(q.BlockedDomains) > 0 {
		body["excludeDomains"] = q.BlockedDomains
	}
	if since := dateSince(q.Recency, time.DateOnly); since != "" {
		body["fromDate"] = since
	}
	var resp struct {
		Results []struct{ Name, URL, Content string }
	}
	if err := doJSON(ctx, client, http.MethodPost, firstNonEmpty(s.BaseURL, "https://api.linkup.so")+"/v1/search", bearer(s.APIKey), body, &resp); err != nil {
		return nil, err
	}
	out := make([]Result, 0, len(resp.Results))
	for _, r := range resp.Results {
		out = append(out, Result{Title: r.Name, URL: r.URL, Snippet: snippetOf(r.Content)})
	}
	return out, nil
}

func searchMojeek(ctx context.Context, client *http.Client, s Settings, q Query) ([]Result, error) {
	// The basic plan returns at most 10 results per request.
	params := url.Values{"api_key": {s.APIKey}, "q": {withSiteOperators(q)}, "fmt": {"json"}, "t": {strconv.Itoa(min(q.Limit, 10))}}
	var resp struct {
		Response struct {
			Results []struct {
				Title, URL, Desc string
				PDate            int64 `json:"pdate"`
			}
		}
	}
	endpoint := firstNonEmpty(s.BaseURL, "https://www.mojeek.com") + "/search?" + params.Encode()
	if err := doJSON(ctx, client, http.MethodGet, endpoint, nil, nil, &resp); err != nil {
		return nil, err
	}
	out := make([]Result, 0, len(resp.Response.Results))
	for _, r := range resp.Response.Results {
		result := Result{Title: r.Title, URL: r.URL, Snippet: r.Desc}
		if r.PDate > 0 {
			result.Published = time.Unix(r.PDate, 0)
		}
		out = append(out, result)
	}
	return out, nil
}

func searchJina(ctx context.Context, client *http.Client, s Settings, q Query) ([]Result, error) {
	h := bearer(s.APIKey)
	// Without page content a search costs far fewer tokens.
	h.Set("X-Respond-With", "no-content")
	var resp struct {
		Data []struct{ Title, URL, Description, Content, Date string }
	}
	endpoint := firstNonEmpty(s.BaseURL, "https://s.jina.ai") + "/?" + url.Values{"q": {withSiteOperators(q)}}.Encode()
	if err := doJSON(ctx, client, http.MethodGet, endpoint, h, nil, &resp); err != nil {
		return nil, err
	}
	out := make([]Result, 0, len(resp.Data))
	for _, r := range resp.Data {
		out = append(out, Result{Title: r.Title, URL: r.URL, Snippet: firstNonEmpty(r.Description, snippetOf(r.Content)), Published: parseDate(r.Date)})
	}
	return out, nil
}

func searchOllama(ctx context.Context, client *http.Client, s Settings, q Query) ([]Result, error) {
	// Ollama takes no filters and at most 10 results; the domain filters go
	// into the query.
	body := map[string]any{"query": withSiteOperators(q), "max_results": min(q.Limit, 10)}
	var resp struct {
		Results []struct{ Title, URL, Content string }
	}
	if err := doJSON(ctx, client, http.MethodPost, firstNonEmpty(s.BaseURL, "https://ollama.com")+"/api/web_search", bearer(s.APIKey), body, &resp); err != nil {
		return nil, err
	}
	out := make([]Result, 0, len(resp.Results))
	for _, r := range resp.Results {
		out = append(out, Result{Title: r.Title, URL: r.URL, Snippet: snippetOf(r.Content)})
	}
	return out, nil
}

// SearXNG has no week filter; a week search asks for a month and the dated
// results are then filtered.
var searxngTimeRange = map[Recency]string{PastDay: "day", PastWeek: "month", PastMonth: "month", PastYear: "year"}

func searchSearXNG(ctx context.Context, client *http.Client, s Settings, q Query) ([]Result, error) {
	params := url.Values{"q": {withSiteOperators(q)}, "format": {"json"}}
	if timeRange := searxngTimeRange[q.Recency]; timeRange != "" {
		params.Set("time_range", timeRange)
	}
	h := http.Header{}
	if s.APIKey != "" {
		// For an instance behind an authenticating proxy.
		h = bearer(s.APIKey)
	}
	var resp struct {
		Results []struct {
			Title, URL, Content string
			PublishedDate       string `json:"publishedDate"`
		}
	}
	if err := doJSON(ctx, client, http.MethodGet, s.BaseURL+"/search?"+params.Encode(), h, nil, &resp); err != nil {
		if errors.Is(err, ErrUnauthorized) {
			return nil, fmt.Errorf("%w; check that settings.yml lists json under search.formats", err)
		}
		return nil, err
	}
	out := make([]Result, 0, len(resp.Results))
	for _, r := range resp.Results {
		out = append(out, Result{Title: r.Title, URL: r.URL, Snippet: r.Content, Published: parseDate(r.PublishedDate)})
	}
	return out, nil
}

var duckDuckGoRecency = map[Recency]string{PastDay: "d", PastWeek: "w", PastMonth: "m", PastYear: "y"}

func searchDuckDuckGo(ctx context.Context, client *http.Client, s Settings, q Query) ([]Result, error) {
	params := url.Values{"q": {withSiteOperators(q)}}
	if df := duckDuckGoRecency[q.Recency]; df != "" {
		params.Set("df", df)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, firstNonEmpty(s.BaseURL, "https://html.duckduckgo.com")+"/html/?"+params.Encode(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "koder/1.0")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return nil, err
	}
	if err := statusError(resp, nil); err != nil {
		return nil, err
	}
	results := parseDuckDuckGo(string(body))
	if len(results) == 0 && strings.Contains(string(body), "anomaly") {
		return nil, fmt.Errorf("%w: DuckDuckGo asked for a captcha", ErrRateLimited)
	}
	return results, nil
}

var (
	ddgResultPattern  = regexp.MustCompile(`(?is)<a[^>]*class="[^"]*result__a[^"]*"[^>]*href="([^"]+)"[^>]*>(.*?)</a>`)
	ddgSnippetPattern = regexp.MustCompile(`(?is)^.*?class="[^"]*result__snippet[^"]*"[^>]*>(.*?)</a>`)
	tagPattern        = regexp.MustCompile(`(?s)<[^>]+>`)
)

func parseDuckDuckGo(body string) []Result {
	matches := ddgResultPattern.FindAllStringSubmatchIndex(body, MaxLimit*4)
	results := make([]Result, 0, len(matches))
	for _, match := range matches {
		result := Result{URL: duckDuckGoTarget(html.UnescapeString(body[match[2]:match[3]])), Title: stripTags(body[match[4]:match[5]])}
		// The snippet follows the title link, before the next result.
		rest := body[match[1]:]
		if next := ddgResultPattern.FindStringIndex(rest); next != nil {
			rest = rest[:next[0]]
		}
		if snippet := ddgSnippetPattern.FindStringSubmatch(rest); len(snippet) > 1 {
			result.Snippet = stripTags(snippet[1])
		}
		results = append(results, result)
	}
	return results
}

// duckDuckGoTarget unwraps DuckDuckGo's redirect links.
func duckDuckGoTarget(rawURL string) string {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return strings.TrimSpace(rawURL)
	}
	if parsed.Host == "duckduckgo.com" && parsed.Path == "/l/" {
		if target := parsed.Query().Get("uddg"); strings.TrimSpace(target) != "" {
			return target
		}
	}
	return parsed.String()
}

func stripTags(value string) string {
	return oneLine(html.UnescapeString(tagPattern.ReplaceAllString(value, " ")))
}

// snippetBytes bounds snippets cut from page content.
const snippetBytes = 400

func snippetOf(content string) string {
	return textutil.Ellipsize(oneLine(content), snippetBytes)
}
