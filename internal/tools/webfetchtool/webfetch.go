package webfetchtool

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
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
func (tool) Definition() (provider.ToolDefinition, bool) {
	return tools.FunctionDefinition(domain.ToolKindWebFetch, "Fetch the contents of a URL", `{"type":"object","properties":{"url":{"type":"string","description":"Fully qualified URL"}},"required":["url"],"additionalProperties":false}`), true
}
func (tool) NormalizeArgs(args map[string]string) (map[string]string, error) {
	rawURL := strings.TrimSpace(args["url"])
	if rawURL == "" {
		return nil, errors.New("url is empty")
	}
	return map[string]string{"url": rawURL}, nil
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
	var buf bytes.Buffer
	if _, err := io.CopyN(&buf, resp.Body, 16*1024); err != nil && !errors.Is(err, io.EOF) {
		return tools.Result{}, err
	}
	return tools.Result{Output: buf.String()}, nil
}
func (tool) SummarizeResult(req tools.Request, result tools.Result) (string, string) {
	return tools.DefaultSummarizeResult(req, result)
}
func (tool) PersistResult(ctx context.Context, st *store.Store, sessionID int64, req tools.Request, result tools.Result) (<-chan domain.Event, error) {
	return tools.PersistStandardResult(ctx, st, sessionID, req, result)
}
