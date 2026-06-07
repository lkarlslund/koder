package websearchtool

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/tools"
)

type tool struct{}

type resultItem struct {
	Title   string
	URL     string
	Snippet string
}

func init() {
	tools.Register(tool{}, tools.ToolSpec{
		Title:       "Search web",
		Description: "Search the public web for current or external information.",
		Usage:       "Search the public web for current or external information beyond the local workspace. Use this to discover relevant pages, news, docs, or references when you do not already know the URL. Use webfetch once you know the page you want to read. Prefer specific queries, and include the current year when looking for recent information. Do not use this for local repository search; use file_grep or file_glob instead. allowed_domains and blocked_domains are optional comma-separated domain lists.",
		Parameters:  `{"type":"object","properties":{"query":{"type":"string","description":"Search query"},"limit":{"type":"integer","description":"Optional maximum result count"},"allowed_domains":{"type":"string","description":"Optional comma-separated domains to include, such as example.com,docs.example.com"},"blocked_domains":{"type":"string","description":"Optional comma-separated domains to exclude"}},"required":["query"],"additionalProperties":false}`,
		ExposeToLLM: true,
	})
}

func (tool) ID() tools.ID             { return domain.ToolKindWebSearch }
func (tool) BypassesPermission() bool { return false }
func (tool) NormalizeArgs(args map[string]string) (map[string]string, error) {
	query := strings.TrimSpace(tools.FirstArg(args, "query", "q", "search"))
	if query == "" {
		return nil, errors.New("query is empty")
	}
	out := map[string]string{"query": query}
	if limit := strings.TrimSpace(tools.FirstArg(args, "limit", "count")); limit != "" {
		value, err := tools.ParseFlexibleInt(limit)
		if err != nil || value <= 0 {
			return nil, errors.New("limit must be a positive integer")
		}
		out["limit"] = strconv.Itoa(value)
	}
	if domains := normalizeDomainList(tools.FirstArg(args, "allowed_domains", "domains", "domain")); domains != "" {
		out["allowed_domains"] = domains
	}
	if domains := normalizeDomainList(tools.FirstArg(args, "blocked_domains", "exclude_domains")); domains != "" {
		out["blocked_domains"] = domains
	}
	return out, nil
}
func (tool) Preview(req tools.Request) string { return req.Args["query"] }
func (tool) Execute(ctx context.Context, runtime tools.Runtime, req tools.Request) (tools.Result, error) {
	client := runtime.HTTPClient
	if client == nil {
		client = &http.Client{}
	}
	limit := 5
	if raw := strings.TrimSpace(req.Args["limit"]); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value <= 0 {
			return tools.Result{}, errors.New("limit must be a positive integer")
		}
		limit = value
	}
	allowedDomains := splitDomainList(req.Args["allowed_domains"])
	blockedDomains := splitDomainList(req.Args["blocked_domains"])
	endpoint := "https://html.duckduckgo.com/html/?q=" + url.QueryEscape(req.Args["query"])
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return tools.Result{}, err
	}
	request.Header.Set("User-Agent", "koder/1.0")
	resp, err := client.Do(request)
	if err != nil {
		return tools.Result{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
		return tools.Result{}, fmt.Errorf("search status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var buf bytes.Buffer
	if _, err := io.CopyN(&buf, resp.Body, int64(tools.DefaultToolOutputLimit)); err != nil && !errors.Is(err, io.EOF) {
		return tools.Result{}, err
	}
	results := parseResults(buf.String(), limit*4)
	results = filterResults(results, allowedDomains, blockedDomains)
	if len(results) > limit {
		results = results[:limit]
	}
	if len(results) == 0 {
		return tools.Result{
			Output: "No results found",
			Meta: map[string]string{
				"query":           req.Args["query"],
				"allowed_domains": req.Args["allowed_domains"],
				"blocked_domains": req.Args["blocked_domains"],
				"results":         "0",
			},
			Stored: tools.WebSearchStoredResult{
				Query:          req.Args["query"],
				AllowedDomains: req.Args["allowed_domains"],
				BlockedDomains: req.Args["blocked_domains"],
			},
		}, nil
	}
	lines := make([]string, 0, len(results)*3)
	for idx, item := range results {
		lines = append(lines, fmt.Sprintf("%d. %s", idx+1, item.Title))
		lines = append(lines, item.URL)
		if item.Snippet != "" {
			lines = append(lines, item.Snippet)
		}
		lines = append(lines, "")
	}
	storedItems := make([]tools.WebSearchStoredItem, 0, len(results))
	for _, item := range results {
		storedItems = append(storedItems, tools.WebSearchStoredItem{
			Title:   item.Title,
			URL:     item.URL,
			Snippet: item.Snippet,
		})
	}
	return tools.Result{
		Output: strings.TrimSpace(strings.Join(lines, "\n")),
		Meta: map[string]string{
			"query":           req.Args["query"],
			"allowed_domains": req.Args["allowed_domains"],
			"blocked_domains": req.Args["blocked_domains"],
			"results":         strconv.Itoa(len(results)),
		},
		Stored: tools.WebSearchStoredResult{
			Query:          req.Args["query"],
			AllowedDomains: req.Args["allowed_domains"],
			BlockedDomains: req.Args["blocked_domains"],
			Items:          storedItems,
		},
	}, nil
}

var (
	resultPattern  = regexp.MustCompile(`(?is)<a[^>]*class="[^"]*result__a[^"]*"[^>]*href="([^"]+)"[^>]*>(.*?)</a>`)
	snippetPattern = regexp.MustCompile(`(?is)<a[^>]*class="[^"]*result__a[^"]*"[^>]*>.*?</a>(.*?)</div>`)
	tagPattern     = regexp.MustCompile(`(?s)<[^>]+>`)
)

func parseResults(body string, limit int) []resultItem {
	matches := resultPattern.FindAllStringSubmatchIndex(body, limit)
	results := make([]resultItem, 0, len(matches))
	for _, match := range matches {
		if len(match) < 6 {
			continue
		}
		href := normalizeResultURL(html.UnescapeString(body[match[2]:match[3]]))
		title := cleanHTML(body[match[4]:match[5]])
		tail := body[match[1]:]
		snippet := ""
		if snippetMatch := snippetPattern.FindStringSubmatch(tail); len(snippetMatch) > 1 {
			snippet = cleanHTML(snippetMatch[1])
		}
		results = append(results, resultItem{
			Title:   title,
			URL:     href,
			Snippet: snippet,
		})
	}
	return results
}

func cleanHTML(value string) string {
	value = tagPattern.ReplaceAllString(value, " ")
	return strings.TrimSpace(strings.Join(strings.Fields(html.UnescapeString(value)), " "))
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

func filterResults(results []resultItem, allowedDomains []string, blockedDomains []string) []resultItem {
	if len(allowedDomains) == 0 && len(blockedDomains) == 0 {
		return results
	}
	filtered := make([]resultItem, 0, len(results))
	for _, item := range results {
		if !domainAllowed(item.URL, allowedDomains, blockedDomains) {
			continue
		}
		filtered = append(filtered, item)
	}
	return filtered
}

func domainAllowed(rawURL string, allowedDomains []string, blockedDomains []string) bool {
	host := normalizedHost(rawURL)
	if host == "" {
		return len(allowedDomains) == 0
	}
	for _, domain := range blockedDomains {
		if hostMatchesDomain(host, domain) {
			return false
		}
	}
	if len(allowedDomains) == 0 {
		return true
	}
	for _, domain := range allowedDomains {
		if hostMatchesDomain(host, domain) {
			return true
		}
	}
	return false
}

func normalizedHost(rawURL string) string {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return ""
	}
	host := strings.ToLower(parsed.Hostname())
	host = strings.TrimPrefix(host, "www.")
	return host
}

func hostMatchesDomain(host string, domain string) bool {
	return host == domain || strings.HasSuffix(host, "."+domain)
}

func normalizeResultURL(rawURL string) string {
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
