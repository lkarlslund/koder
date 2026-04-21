package webfetchtool

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

func init() { tools.Register(tool{}) }

func (tool) Kind() domain.ToolKind    { return domain.ToolKindWebFetch }
func (tool) BypassesPermission() bool { return false }
func (tool) Definition(tools.Runtime) (provider.ToolDefinition, bool) {
	return tools.FunctionDefinition(domain.ToolKindWebFetch, "Fetch the contents of a URL", `{"type":"object","properties":{"url":{"type":"string","description":"Fully qualified URL"},"format":{"type":"string","description":"Optional response format such as text or markdown"}},"required":["url"],"additionalProperties":false}`), true
}
func (tool) NormalizeArgs(args map[string]string) (map[string]string, error) {
	rawURL := strings.TrimSpace(tools.FirstArg(args, "url", "href"))
	if rawURL == "" {
		return nil, errors.New("url is empty")
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return nil, err
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, errors.New("url must use http or https")
	}
	out := map[string]string{"url": parsed.String()}
	if format := strings.TrimSpace(tools.FirstArg(args, "format")); format != "" {
		out["format"] = format
	}
	return out, nil
}
func (tool) LegacyArgs(raw string) map[string]string { return map[string]string{"url": raw} }
func (tool) Preview(req tools.Request) string        { return req.Args["url"] }
func (tool) PresentationForPreview(preview string) tools.Presentation {
	preview = strings.TrimSpace(preview)
	return tools.Presentation{Title: "Fetch URL", Subtitle: preview, Preview: preview}
}
func (tool) Presentation(req tools.Request) tools.Presentation {
	return tool{}.PresentationForPreview(req.Args["url"])
}
func (tool) Execute(ctx context.Context, runtime tools.Runtime, req tools.Request) (tools.Result, error) {
	client := runtime.HTTPClient
	if client == nil {
		client = &http.Client{}
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, req.Args["url"], nil)
	if err != nil {
		return tools.Result{}, err
	}
	resp, err := client.Do(request)
	if err != nil {
		return tools.Result{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
		return tools.Result{}, fmt.Errorf("fetch status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var buf bytes.Buffer
	if _, err := io.CopyN(&buf, resp.Body, int64(tools.DefaultToolOutputLimit)); err != nil && !errors.Is(err, io.EOF) {
		return tools.Result{}, err
	}
	contentType := strings.ToLower(strings.TrimSpace(resp.Header.Get("Content-Type")))
	body := buf.String()
	switch {
	case strings.Contains(contentType, "text/html"):
		body = renderHTMLAsText(body)
	case strings.HasPrefix(contentType, "text/"), strings.Contains(contentType, "json"), contentType == "":
	default:
		return tools.Result{}, fmt.Errorf("unsupported content type %q", contentType)
	}
	body, truncated := tools.TruncateText(body, tools.DefaultToolOutputLimit)
	return tools.Result{
		Output: body,
		Meta: map[string]string{
			"url":          req.Args["url"],
			"status":       strconv.Itoa(resp.StatusCode),
			"content_type": contentType,
			"truncated":    tools.BoolString(truncated),
		},
		Stored: tools.WebFetchStoredResult{
			URL:         req.Args["url"],
			Status:      resp.StatusCode,
			ContentType: contentType,
			Body:        body,
			Truncated:   truncated,
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
	htmlScriptPattern = regexp.MustCompile(`(?is)<(script|style)[^>]*>.*?</(script|style)>`)
	htmlTagPattern    = regexp.MustCompile(`(?s)<[^>]+>`)
	spacePattern      = regexp.MustCompile(`[ \t\r\f\v]+`)
)

func renderHTMLAsText(body string) string {
	body = htmlScriptPattern.ReplaceAllString(body, " ")
	body = htmlTagPattern.ReplaceAllString(body, "\n")
	body = strings.ReplaceAll(body, "&nbsp;", " ")
	body = strings.ReplaceAll(body, "&amp;", "&")
	body = strings.ReplaceAll(body, "&lt;", "<")
	body = strings.ReplaceAll(body, "&gt;", ">")
	lines := strings.Split(body, "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(spacePattern.ReplaceAllString(line, " "))
		if line == "" {
			continue
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}
