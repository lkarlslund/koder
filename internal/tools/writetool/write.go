package writetool

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/sergi/go-diff/diffmatchpatch"
	"golang.org/x/text/cases"
	"golang.org/x/text/language"

	"github.com/lkarlslund/koder/internal/domain"
	"github.com/lkarlslund/koder/internal/provider"
	"github.com/lkarlslund/koder/internal/tools"
)

type tool struct{}

func init() { tools.Register(tool{}) }

func (tool) Kind() domain.ToolKind    { return domain.ToolKindWrite }
func (tool) BypassesPermission() bool { return false }
func (tool) Definition(tools.Runtime) (provider.ToolDefinition, bool) {
	return tools.FunctionDefinition(domain.ToolKindWrite, "Create a new file or completely overwrite a file in the workspace. For existing files, prefer Edit or apply_patch whenever possible. Use this tool for new files or intentional full rewrites only. Do not use Write as a fallback just because an Edit or apply_patch attempt failed; first retry with more precise context.", `{"type":"object","properties":{"path":{"type":"string","description":"File to create or completely overwrite"},"content":{"type":"string","description":"Complete contents of the file after overwrite. Use only for new files or intentional full rewrites."}},"required":["path","content"],"additionalProperties":false}`), true
}
func (tool) NormalizeArgs(args map[string]string) (map[string]string, error) {
	path := tools.NormalizePathInput(tools.FirstArg(args, "path", "file", "file_path", "filepath"))
	content := tools.FirstArg(args, "content", "text", "body")
	if path == "" {
		return nil, errors.New("path is empty")
	}
	return map[string]string{"path": path, "content": content}, nil
}
func (tool) LegacyArgs(raw string) map[string]string { return map[string]string{"path": raw} }
func (tool) Preview(req tools.Request) string        { return req.Args["path"] }
func (tool) Execute(_ context.Context, runtime tools.Runtime, req tools.Request) (tools.Result, error) {
	abs, rel, err := tools.WorkspacePath(runtime.Workdir, req.Args["path"])
	if err != nil {
		return tools.Result{}, err
	}
	beforeBytes, readErr := os.ReadFile(abs)
	mode := os.FileMode(0o644)
	action := "created"
	if readErr == nil {
		if info, statErr := os.Stat(abs); statErr == nil {
			mode = info.Mode().Perm()
		}
		action = "overwrote"
	}
	if err := tools.WriteTextFile(abs, req.Args["content"], mode); err != nil {
		return tools.Result{}, err
	}
	dmp := diffmatchpatch.New()
	diffs := dmp.DiffMain(string(beforeBytes), req.Args["content"], false)
	summary := fmt.Sprintf("%s %s", cases.Title(language.English).String(action), rel)
	content, truncated := tools.TruncateText(req.Args["content"], tools.DefaultToolOutputLimit)
	return tools.Result{
		Output:   summary,
		DiffText: dmp.DiffPrettyText(diffs),
		Meta: map[string]string{
			"path":   rel,
			"action": action,
		},
		Stored: tools.WriteStoredResult{
			Path:      rel,
			Action:    action,
			Summary:   summary,
			Content:   content,
			Truncated: truncated,
		},
	}, nil
}
func (tool) SummarizeResult(req tools.Request, result tools.Result) (string, string) {
	return "write", result.Output
}
