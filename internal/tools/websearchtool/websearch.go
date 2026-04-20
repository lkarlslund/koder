package websearchtool

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/provider"
	"github.com/lkarlslund/koder/internal/store"
	"github.com/lkarlslund/koder/internal/tools"
)

type tool struct{}

type resultItem struct {
	Title   string
	URL     string
	Snippet string
}

func init() { tools.Register(tool{}) }

func (tool) Kind() domain.ToolKind    { return domain.ToolKindWebSearch }
func (tool) BypassesPermission() bool { return false }
func (tool) Definition() (provider.ToolDefinition, bool) {
	return tools.FunctionDefinition(domain.ToolKindWebSearch, "Search the web for recent public information", `{"type":"object","properties":{"query":{"type":"string","description":"Search query"},"limit":{"type":"integer","description":"Optional maximum result count"}},"required":["query"],"additionalProperties":false}`), true
}
func (tool) NormalizeArgs(args map[string]string) (map[string]string, error) {
	query := strings.TrimSpace(tools.FirstArg(args, "query", "q", "search"))
	if query == "" {
		return nil, errors.New("query is empty")
	}
	out := map[string]string{"query": query}
	if limit := strings.TrimSpace(tools.FirstArg(args, "limit", "count")); limit != "" {
		out["limit"] = limit
	}
	return out, nil
}
func (tool) LegacyArgs(raw string) map[string]string { return map[string]string{"query": raw} }
func (tool) Preview(req tools.Request) string        { return req.Args["query"] }
func (tool) PresentationForPreview(preview string) tools.Presentation {
	preview = strings.TrimSpace(preview)
	subtitle := preview
	if subtitle != "" {
		subtitle = "Query: " + subtitle
	}
	return tools.Presentation{Title: "Search web", Subtitle: subtitle, Preview: preview}
}
func (tool) Presentation(req tools.Request) tools.Presentation {
	return tool{}.PresentationForPreview(req.Args["query"])
}
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
		if value < limit {
			limit = value
		}
	}
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
	results := parseResults(buf.String(), limit)
	if len(results) == 0 {
		return tools.Result{
			Output: "No results found",
			Meta: map[string]string{
				"query":   req.Args["query"],
				"results": "0",
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
	return tools.Result{
		Output: strings.TrimSpace(strings.Join(lines, "\n")),
		Meta: map[string]string{
			"query":   req.Args["query"],
			"results": strconv.Itoa(len(results)),
		},
	}, nil
}
func (tool) SummarizeResult(req tools.Request, result tools.Result) (string, string) {
	return tools.DefaultSummarizeResult(req, result)
}
func (tool) PersistResult(ctx context.Context, st *store.Store, sessionID int64, req tools.Request, result tools.Result) (<-chan domain.Event, error) {
	return tools.PersistStandardResult(ctx, st, sessionID, req, result)
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
		href := htmlDecode(body[match[2]:match[3]])
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
	return strings.TrimSpace(strings.Join(strings.Fields(htmlDecode(value)), " "))
}

func htmlDecode(value string) string {
	value = strings.ReplaceAll(value, "&amp;", "&")
	value = strings.ReplaceAll(value, "&quot;", `"`)
	value = strings.ReplaceAll(value, "&#x27;", "'")
	value = strings.ReplaceAll(value, "&lt;", "<")
	value = strings.ReplaceAll(value, "&gt;", ">")
	return value
}
