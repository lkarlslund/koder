package websearchtool

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/lkarlslund/koder/internal/config"
	"github.com/lkarlslund/koder/internal/tools"
	"github.com/lkarlslund/koder/internal/websearch"
)

type tool struct{}

func init() {
	tools.Register(tool{}, tools.ToolSpec{
		Title:       "Search web",
		Description: "Search the public web for current or external information.",
		Usage:       "Search the public web for current or external information beyond the local workspace. Use this to discover relevant pages, news, docs, or references when you do not already know the URL. Use webfetch once you know the page you want to read. Prefer specific queries, and include the current year when looking for recent information. Do not use this for local repository search; use file_grep or file_glob instead. allowed_domains and blocked_domains are optional comma-separated domain lists. recency (day, week, month or year) limits results to recently published pages.",
		Parameters:  `{"type":"object","properties":{"query":{"type":"string","description":"Search query"},"limit":{"type":"integer","description":"Optional maximum result count"},"allowed_domains":{"type":"string","description":"Optional comma-separated domains to include, such as example.com,docs.example.com"},"blocked_domains":{"type":"string","description":"Optional comma-separated domains to exclude"},"recency":{"type":"string","enum":["day","week","month","year"],"description":"Optional: only pages published within this period"}},"required":["query"],"additionalProperties":false}`,
		ExposeToLLM: true,
	})
}

func (tool) ID() tools.ID             { return tools.WebSearch }
func (tool) BypassesPermission() bool { return false }
func (tool) NormalizeArgs(args map[string]string) (map[string]string, error) {
	query := strings.TrimSpace(args["query"])
	if query == "" {
		return nil, errors.New("query is empty")
	}
	out := map[string]string{"query": query}
	if limit := strings.TrimSpace(args["limit"]); limit != "" {
		value, err := tools.ParseFlexibleInt(limit)
		if err != nil || value <= 0 {
			return nil, errors.New("limit must be a positive integer")
		}
		out["limit"] = strconv.Itoa(value)
	}
	if domains := normalizeDomainList(args["allowed_domains"]); domains != "" {
		out["allowed_domains"] = domains
	}
	if domains := normalizeDomainList(args["blocked_domains"]); domains != "" {
		out["blocked_domains"] = domains
	}
	if raw := strings.TrimSpace(args["recency"]); raw != "" {
		recency, err := websearch.ParseRecency(raw)
		if err != nil {
			return nil, err
		}
		out["recency"] = string(recency)
	}
	return out, nil
}
func (tool) Preview(req tools.Request) string { return req.Args["query"] }
func (tool) Call(ctx context.Context, opts tools.Options) (tools.Result, error) {
	runtime, req := opts.Runtime, opts.Request
	query := websearch.Query{
		Text:           req.Args["query"],
		Limit:          5,
		AllowedDomains: splitDomainList(req.Args["allowed_domains"]),
		BlockedDomains: splitDomainList(req.Args["blocked_domains"]),
	}
	if raw := strings.TrimSpace(req.Args["limit"]); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value <= 0 {
			return tools.Result{}, errors.New("limit must be a positive integer")
		}
		query.Limit = min(value, websearch.MaxLimit)
	}
	recency, err := websearch.ParseRecency(req.Args["recency"])
	if err != nil {
		return tools.Result{}, err
	}
	query.Recency = recency
	backends := runtime.WebSearch
	if backends == nil {
		// A runtime without search settings uses the default providers.
		backends = websearch.Backends(config.Search{})
	}
	if len(backends) == 0 {
		return tools.Result{}, errors.New("no web search provider is enabled; enable one under Settings > Search")
	}
	// Providers are tried in order of preference until one answers, skipping
	// those backing off after refusing for quota, rate or key reasons.
	var failures []string
	for _, backend := range backends {
		if until, reason := backend.BackingOff(time.Now()); reason != nil {
			failures = append(failures, fmt.Sprintf("%s skipped: %v until %s", backend.Provider.Title, reason, until.Local().Format("15:04")))
			continue
		}
		results, err := backend.Provider.Search(ctx, runtime.HTTPClient, backend.Settings, query)
		if err != nil {
			failures = append(failures, err.Error())
			continue
		}
		return searchResult(req, backend.Provider, results, failures), nil
	}
	return tools.Result{}, fmt.Errorf("every web search provider failed:\n%s", strings.Join(failures, "\n"))
}

func searchResult(req tools.Request, provider websearch.Provider, results []websearch.Result, failures []string) tools.Result {
	meta := map[string]string{
		"query":           req.Args["query"],
		"allowed_domains": req.Args["allowed_domains"],
		"blocked_domains": req.Args["blocked_domains"],
		"recency":         req.Args["recency"],
		"provider":        provider.ID,
		"results":         strconv.Itoa(len(results)),
	}
	stored := tools.WebSearchStoredResult{
		Query:          req.Args["query"],
		AllowedDomains: req.Args["allowed_domains"],
		BlockedDomains: req.Args["blocked_domains"],
		Provider:       provider.Title,
	}
	lines := []string{"Results from " + provider.Title + ":"}
	if len(failures) > 0 {
		lines[0] = "Results from " + provider.Title + " (" + strings.Join(failures, "; ") + "):"
	}
	if len(results) == 0 {
		lines = append(lines, "No results found")
	}
	for idx, item := range results {
		lines = append(lines, fmt.Sprintf("%d. %s", idx+1, item.Title), item.URL)
		published := ""
		if !item.Published.IsZero() {
			published = item.Published.Format(time.DateOnly)
			lines = append(lines, "Published "+published)
		}
		if item.Snippet != "" {
			lines = append(lines, item.Snippet)
		}
		lines = append(lines, "")
		stored.Items = append(stored.Items, tools.WebSearchStoredItem{Title: item.Title, URL: item.URL, Snippet: item.Snippet, Published: published})
	}
	return tools.Result{Output: strings.TrimSpace(strings.Join(lines, "\n")), Meta: meta, Stored: stored}
}

func normalizeDomainList(raw string) string {
	domains := splitDomainList(raw)
	if len(domains) == 0 {
		return ""
	}
	slices.Sort(domains)
	return strings.Join(domains, ",")
}

func splitDomainList(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	seen := map[string]struct{}{}
	for _, part := range parts {
		part = strings.ToLower(strings.TrimSpace(part))
		part = strings.TrimPrefix(part, "http://")
		part = strings.TrimPrefix(part, "https://")
		part = strings.TrimPrefix(part, "www.")
		part = strings.TrimSuffix(part, "/")
		if part == "" {
			continue
		}
		if _, ok := seen[part]; ok {
			continue
		}
		seen[part] = struct{}{}
		out = append(out, part)
	}
	return out
}
