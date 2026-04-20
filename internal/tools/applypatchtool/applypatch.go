package applypatchtool

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/sergi/go-diff/diffmatchpatch"

	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/provider"
	"github.com/lkarlslund/koder/internal/store"
	"github.com/lkarlslund/koder/internal/tools"
)

type tool struct{}

func init() { tools.Register(tool{}) }

func (tool) Kind() domain.ToolKind    { return domain.ToolKindApplyPatch }
func (tool) BypassesPermission() bool { return false }
func (tool) Definition() (provider.ToolDefinition, bool) {
	return tools.FunctionDefinition(domain.ToolKindApplyPatch, "Write file content directly to a workspace path", `{"type":"object","properties":{"path":{"type":"string","description":"Relative file path to overwrite"},"content":{"type":"string","description":"Full new file contents"}},"required":["path","content"],"additionalProperties":false}`), true
}
func (tool) NormalizeArgs(args map[string]string) (map[string]string, error) {
	path := strings.TrimSpace(args["path"])
	if path == "" {
		return nil, errors.New("path is empty")
	}
	return map[string]string{"path": path, "content": args["content"]}, nil
}
func (tool) LegacyArgs(raw string) map[string]string { return map[string]string{"path": raw} }
func (tool) Preview(req tools.Request) string        { return req.Args["path"] }
func (tool) PresentationForPreview(preview string) tools.Presentation {
	preview = strings.TrimSpace(preview)
	return tools.Presentation{Title: "Apply patch", Subtitle: preview, Preview: preview}
}
func (tool) Presentation(req tools.Request) tools.Presentation {
	return tool{}.PresentationForPreview(req.Args["path"])
}
func (tool) Execute(_ context.Context, runtime tools.Runtime, req tools.Request) (tools.Result, error) {
	fullPath := filepath.Join(runtime.Workdir, req.Args["path"])
	before, _ := os.ReadFile(fullPath)
	if err := os.WriteFile(fullPath, []byte(req.Args["content"]), 0o644); err != nil {
		return tools.Result{}, err
	}
	dmp := diffmatchpatch.New()
	diffs := dmp.DiffMain(string(before), req.Args["content"], false)
	return tools.Result{
		Output:   "patched " + req.Args["path"],
		DiffText: dmp.DiffPrettyText(diffs),
	}, nil
}
func (tool) SummarizeResult(req tools.Request, result tools.Result) (string, string) {
	return tools.DefaultSummarizeResult(req, result)
}
func (tool) PersistResult(ctx context.Context, st *store.Store, sessionID int64, req tools.Request, result tools.Result) (<-chan domain.Event, error) {
	return tools.PersistStandardResult(ctx, st, sessionID, req, result)
}
