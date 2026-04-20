package greptool

import (
	"context"
	"errors"
	"os/exec"
	"strings"

	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/provider"
	"github.com/lkarlslund/koder/internal/store"
	"github.com/lkarlslund/koder/internal/tools"
)

type tool struct{}

func init() { tools.Register(tool{}) }

func (tool) Kind() domain.ToolKind    { return domain.ToolKindGrep }
func (tool) BypassesPermission() bool { return false }
func (tool) Definition() (provider.ToolDefinition, bool) {
	return tools.FunctionDefinition(domain.ToolKindGrep, "Search for text within workspace files", `{"type":"object","properties":{"pattern":{"type":"string","description":"Text or regex to search for"}},"required":["pattern"],"additionalProperties":false}`), true
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
	subtitle := preview
	if subtitle != "" {
		subtitle = "Query: " + subtitle
	}
	return tools.Presentation{Title: "Search text", Subtitle: subtitle, Preview: preview}
}
func (tool) Presentation(req tools.Request) tools.Presentation {
	return tool{}.PresentationForPreview(req.Args["pattern"])
}
func (tool) Execute(ctx context.Context, runtime tools.Runtime, req tools.Request) (tools.Result, error) {
	if _, err := exec.LookPath("rg"); err == nil {
		cmd := exec.CommandContext(ctx, "rg", "-n", req.Args["pattern"], ".")
		cmd.Dir = runtime.Workdir
		output, err := cmd.CombinedOutput()
		if err != nil && len(output) == 0 {
			return tools.Result{}, err
		}
		return tools.Result{Output: string(output)}, nil
	}
	return tools.Result{}, errors.New("rg is required for grep fallback in this build")
}
func (tool) SummarizeResult(req tools.Request, result tools.Result) (string, string) {
	return tools.DefaultSummarizeResult(req, result)
}
func (tool) PersistResult(ctx context.Context, st *store.Store, sessionID int64, req tools.Request, result tools.Result) (<-chan domain.Event, error) {
	return tools.PersistStandardResult(ctx, st, sessionID, req, result)
}
