package globtool

import (
	"context"
	"errors"
	"path/filepath"
	"strings"

	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/provider"
	"github.com/lkarlslund/koder/internal/store"
	"github.com/lkarlslund/koder/internal/tools"
)

type tool struct{}

func init() { tools.Register(tool{}) }

func (tool) Kind() domain.ToolKind    { return domain.ToolKindGlob }
func (tool) BypassesPermission() bool { return false }
func (tool) Definition() (provider.ToolDefinition, bool) {
	return tools.FunctionDefinition(domain.ToolKindGlob, "Find workspace paths matching a glob pattern", `{"type":"object","properties":{"pattern":{"type":"string","description":"Glob pattern relative to the workspace"}},"required":["pattern"],"additionalProperties":false}`), true
}
func (tool) NormalizeArgs(args map[string]string) (map[string]string, error) {
	pattern := strings.TrimSpace(args["pattern"])
	if pattern == "" {
		return nil, errors.New("pattern is empty")
	}
	return map[string]string{"pattern": pattern}, nil
}
func (tool) LegacyArgs(raw string) map[string]string { return map[string]string{"pattern": raw} }
func (tool) Preview(req tools.Request) string        { return req.Args["pattern"] }
func (tool) PresentationForPreview(preview string) tools.Presentation {
	preview = strings.TrimSpace(preview)
	if preview != "" {
		preview = "Pattern: " + preview
	}
	return tools.Presentation{Title: "Find files", Subtitle: preview, Preview: strings.TrimPrefix(preview, "Pattern: ")}
}
func (tool) Presentation(req tools.Request) tools.Presentation {
	return tool{}.PresentationForPreview(req.Args["pattern"])
}
func (tool) Execute(_ context.Context, runtime tools.Runtime, req tools.Request) (tools.Result, error) {
	matches, err := filepath.Glob(filepath.Join(runtime.Workdir, req.Args["pattern"]))
	if err != nil {
		return tools.Result{}, err
	}
	for i, item := range matches {
		rel, relErr := filepath.Rel(runtime.Workdir, item)
		if relErr == nil {
			matches[i] = rel
		}
	}
	return tools.Result{Output: strings.Join(matches, "\n")}, nil
}
func (tool) SummarizeResult(req tools.Request, result tools.Result) (string, string) {
	return tools.DefaultSummarizeResult(req, result)
}
func (tool) PersistResult(ctx context.Context, st *store.Store, sessionID int64, req tools.Request, result tools.Result) (<-chan domain.Event, error) {
	return tools.PersistStandardResult(ctx, st, sessionID, req, result)
}
