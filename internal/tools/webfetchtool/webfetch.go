package webfetchtool

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
	"strconv"
	"strings"
	"sync"

	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/tools"
)

type tool struct{}

type outputFormat string

const (
	formatMarkdown outputFormat = "markdown"
	formatText     outputFormat = "text"
	formatHTML     outputFormat = "html"
)

var fetchCache sync.Map

func init() {
	tools.Register(tool{}, tools.ToolSpec{
		Title:       "Fetch URL",
		Description: "Fetch a URL and return its rendered contents to the LLM context.",
		Usage:       "Retrieve a known public URL and include the page contents in the model conversation. Use this when you already know the URL and need to read or summarize the page. Use websearch first if you need to discover relevant pages. Do not use this to download files for later local processing; use external tools such as curl or wget through exec_command when the content should be saved to disk or handled outside the LLM context. format controls the returned content: markdown is best for most pages, text is plain extracted text, and html returns the raw response body. max_chars limits the returned body size.",
		Parameters:  `{"type":"object","properties":{"url":{"type":"string","description":"Fully qualified http or https URL to retrieve"},"format":{"type":"string","enum":["markdown","text","html"],"description":"Optional output format. markdown is best for most pages; text returns plain extracted text; html returns raw response text."},"max_chars":{"type":"integer","description":"Optional maximum number of characters to return"}},"required":["url"],"additionalProperties":false}`,
		ExposeToLLM: true,
	})
}

func (tool) Kind() domain.ToolKind    { return domain.ToolKindWebFetch }
func (tool) BypassesPermission() bool { return false }

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
	format := outputFormat(strings.ToLower(strings.TrimSpace(tools.FirstArg(args, "format"))))
	if format == "" {
		format = formatMarkdown
	}
	switch format {
	case formatMarkdown, formatText, formatHTML:
	default:
		return nil, errors.New("format must be markdown, text, or html")
	}
	out := map[string]string{
		"url":    parsed.String(),
		"format": string(format),
	}
	if rawMax := strings.TrimSpace(tools.FirstArg(args, "max_chars", "max_chars_out", "limit")); rawMax != "" {
		value, err := tools.ParseFlexibleInt(rawMax)
		if err != nil || value <= 0 {
			return nil, errors.New("max_chars must be a positive integer")
		}
		if value > tools.DefaultToolOutputLimit {
			value = tools.DefaultToolOutputLimit
		}
		out["max_chars"] = strconv.Itoa(value)
	}
	return out, nil
}

func (tool) Preview(req tools.Request) string { return req.Args["url"] }

func (tool) Execute(ctx context.Context, runtime tools.Runtime, req tools.Request) (tools.Result, error) {
	if cached, ok := fetchCache.Load(cacheKey(req)); ok {
		return cached.(tools.Result), nil
	}
	client := runtime.HTTPClient
	if client == nil {
		client = &http.Client{}
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, req.Args["url"], nil)
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
		return tools.Result{}, fmt.Errorf("fetch status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	limit := maxChars(req)
	var buf bytes.Buffer
	if _, err := io.CopyN(&buf, resp.Body, int64(limit+1)); err != nil && !errors.Is(err, io.EOF) {
		return tools.Result{}, err
	}
	contentType := normalizedContentType(resp.Header.Get("Content-Type"))
	rendered, err := renderBody(req.Args["format"], contentType, buf.String())
	if err != nil {
		return tools.Result{}, err
	}
	rendered, truncated := tools.TruncateText(strings.TrimSpace(rendered), limit)
	finalURL := req.Args["url"]
	if resp.Request != nil && resp.Request.URL != nil {
		finalURL = resp.Request.URL.String()
	}
	result := tools.Result{
		Output: rendered,
		Meta: map[string]string{
			"url":          req.Args["url"],
			"final_url":    finalURL,
			"format":       req.Args["format"],
			"status":       strconv.Itoa(resp.StatusCode),
			"content_type": contentType,
			"truncated":    tools.BoolString(truncated),
		},
		Stored: tools.WebFetchStoredResult{
			URL:         req.Args["url"],
			FinalURL:    finalURL,
			Format:      req.Args["format"],
			Status:      resp.StatusCode,
			ContentType: contentType,
			Body:        rendered,
			Truncated:   truncated,
		},
	}
	fetchCache.Store(cacheKey(req), result)
	return result, nil
}

func cacheKey(req tools.Request) string {
	return req.Args["url"] + "\x00" + req.Args["format"] + "\x00" + req.Args["max_chars"]
}

func maxChars(req tools.Request) int {
	if raw := strings.TrimSpace(req.Args["max_chars"]); raw != "" {
		if value, err := strconv.Atoi(raw); err == nil && value > 0 {
			return value
		}
	}
	return tools.DefaultToolOutputLimit
}

func normalizedContentType(raw string) string {
	value := strings.ToLower(strings.TrimSpace(raw))
	if idx := strings.IndexByte(value, ';'); idx >= 0 {
		value = strings.TrimSpace(value[:idx])
	}
	return value
}

func renderBody(format string, contentType string, body string) (string, error) {
	switch {
	case strings.Contains(contentType, "text/html"):
		switch outputFormat(format) {
		case formatHTML:
			return body, nil
		case formatText:
			return renderHTMLAsText(body), nil
		default:
			return renderHTMLAsMarkdown(body), nil
		}
	case strings.HasPrefix(contentType, "text/"),
		strings.Contains(contentType, "json"),
		strings.Contains(contentType, "xml"),
		strings.Contains(contentType, "javascript"),
		contentType == "":
		return body, nil
	default:
		return "", fmt.Errorf("unsupported content type %q", contentType)
	}
}

var (
	htmlScriptPattern   = regexp.MustCompile(`(?is)<(script|style|noscript)[^>]*>.*?</(script|style|noscript)>`)
	htmlTitlePattern    = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)
	htmlLinkPattern     = regexp.MustCompile(`(?is)<a[^>]*href=["']([^"']+)["'][^>]*>(.*?)</a>`)
	htmlHeadingPattern  = regexp.MustCompile(`(?is)<h([1-6])[^>]*>(.*?)</h[1-6]>`)
	htmlListItemPattern = regexp.MustCompile(`(?is)<li[^>]*>(.*?)</li>`)
	htmlBreakPattern    = regexp.MustCompile(`(?is)<br\s*/?>`)
	htmlBlockPattern    = regexp.MustCompile(`(?is)</?(p|div|section|article|main|header|footer|aside|nav|table|tr|td|th|ul|ol|pre|blockquote)[^>]*>`)
	htmlTagPattern      = regexp.MustCompile(`(?s)<[^>]+>`)
	spacePattern        = regexp.MustCompile(`[ \t\r\f\v]+`)
	blankLinePattern    = regexp.MustCompile(`\n{3,}`)
)

func renderHTMLAsText(body string) string {
	body = htmlScriptPattern.ReplaceAllString(body, " ")
	body = htmlBreakPattern.ReplaceAllString(body, "\n")
	body = htmlBlockPattern.ReplaceAllString(body, "\n")
	body = htmlTagPattern.ReplaceAllString(body, " ")
	return cleanText(body)
}

func renderHTMLAsMarkdown(body string) string {
	body = htmlScriptPattern.ReplaceAllString(body, " ")
	title := ""
	if match := htmlTitlePattern.FindStringSubmatch(body); len(match) > 1 {
		title = strings.TrimSpace(cleanInlineText(match[1]))
	}
	body = htmlLinkPattern.ReplaceAllStringFunc(body, func(raw string) string {
		match := htmlLinkPattern.FindStringSubmatch(raw)
		if len(match) < 3 {
			return raw
		}
		text := strings.TrimSpace(cleanInlineText(match[2]))
		href := strings.TrimSpace(html.UnescapeString(match[1]))
		if text == "" {
			return href
		}
		return "[" + text + "](" + href + ")"
	})
	body = htmlHeadingPattern.ReplaceAllStringFunc(body, func(raw string) string {
		match := htmlHeadingPattern.FindStringSubmatch(raw)
		if len(match) < 3 {
			return raw
		}
		level, _ := strconv.Atoi(match[1])
		if level < 1 {
			level = 1
		}
		return "\n" + strings.Repeat("#", level) + " " + strings.TrimSpace(cleanInlineText(match[2])) + "\n"
	})
	body = htmlListItemPattern.ReplaceAllStringFunc(body, func(raw string) string {
		match := htmlListItemPattern.FindStringSubmatch(raw)
		if len(match) < 2 {
			return raw
		}
		return "\n- " + strings.TrimSpace(cleanInlineText(match[1]))
	})
	body = htmlBreakPattern.ReplaceAllString(body, "\n")
	body = htmlBlockPattern.ReplaceAllString(body, "\n")
	body = htmlTagPattern.ReplaceAllString(body, " ")
	body = cleanText(body)
	if title != "" && !strings.HasPrefix(body, "# ") {
		body = "# " + title + "\n\n" + body
	}
	return strings.TrimSpace(body)
}

func cleanInlineText(value string) string {
	value = htmlTagPattern.ReplaceAllString(value, " ")
	value = html.UnescapeString(value)
	return strings.TrimSpace(strings.Join(strings.Fields(spacePattern.ReplaceAllString(value, " ")), " "))
}

func cleanText(value string) string {
	value = html.UnescapeString(value)
	lines := strings.Split(value, "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(spacePattern.ReplaceAllString(line, " "))
		if line == "" {
			if len(out) > 0 && out[len(out)-1] != "" {
				out = append(out, "")
			}
			continue
		}
		out = append(out, line)
	}
	return strings.TrimSpace(blankLinePattern.ReplaceAllString(strings.Join(out, "\n"), "\n\n"))
}
