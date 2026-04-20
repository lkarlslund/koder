package websearchtool

import (
	"context"
	"errors"
	"strings"

	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/provider"
	"github.com/lkarlslund/koder/internal/store"
	"github.com/lkarlslund/koder/internal/tools"
)

type tool struct{}

func init() { tools.Register(tool{}) }

func (tool) Kind() domain.ToolKind                       { return domain.ToolKindWebSearch }
func (tool) BypassesPermission() bool                    { return false }
func (tool) Definition() (provider.ToolDefinition, bool) { return provider.ToolDefinition{}, false }
func (tool) NormalizeArgs(args map[string]string) (map[string]string, error) {
	query := strings.TrimSpace(args["query"])
	if query == "" {
		return nil, errors.New("query is empty")
	}
	return map[string]string{"query": query}, nil
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
func (tool) Execute(_ context.Context, _ tools.Runtime, req tools.Request) (tools.Result, error) {
	return tools.Result{}, errors.New("websearch is not implemented yet")
}
func (tool) SummarizeResult(req tools.Request, result tools.Result) (string, string) {
	return tools.DefaultSummarizeResult(req, result)
}
func (tool) PersistResult(ctx context.Context, st *store.Store, sessionID int64, req tools.Request, result tools.Result) (<-chan domain.Event, error) {
	return tools.PersistStandardResult(ctx, st, sessionID, req, result)
}
